// Package api implements the unified /v1/streams handlers per ADR 0009 §D5.
//
// The three endpoints in this file collapse the 25 old per-protocol session/
// connection endpoints into a single canonical Stream surface. The conversion
// from per-protocol session shapes (APIRTSPSession, APIRTMPConn, APISRTConn,
// APIWebRTCSession, APIHLSSession) to the canonical defs.Stream is delegated
// to the Phase 1 translators in internal/defs/stream_translate.go. This file
// is purely glue: walk each protocol cluster, call APISessionsList /
// APIConnsList, hand the result to the translator, optionally filter,
// paginate, redact, return.
//
// The route registration (group.GET("/v1/streams", a.onV1StreamsList) etc.)
// is the orchestrator's responsibility per the Phase 2B contract; this file
// only defines the handlers.
package api //nolint:revive

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/bluenviron/mediamtx/internal/servers/rtmp"
	"github.com/bluenviron/mediamtx/internal/servers/rtsp"
	"github.com/bluenviron/mediamtx/internal/servers/srt"
	"github.com/bluenviron/mediamtx/internal/servers/webrtc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// streamCameraIDFromPath maps a path-name string to a stable canonical
// CameraID UUID. The canonical Stream needs to reference Camera by UUID, but
// the existing protocol session shapes carry path-name strings. Subagent 2A
// owns a centralized helper (likely PathManager.NameToCameraID) that
// supersedes this; until that lands here, we emit a deterministic UUIDv5 in
// the OID namespace so two streams on the same path round-trip to the same
// camera_id without coupling to 2A's not-yet-merged code.
//
// TODO(2A): replace with the centralized name-to-camera-id resolver once
// available. The orchestrator can deduplicate.
func streamCameraIDFromPath(pathName string) string {
	if pathName == "" {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(pathName)).String()
}

// streamFilters captures the query-string filters accepted by GET /v1/streams.
type streamFilters struct {
	protocol  defs.StreamProtocol // empty = no filter
	cameraID  string              // empty = no filter
	direction defs.StreamDirection
	state     defs.StreamState
}

// parseStreamFilters extracts and validates the four canonical filters from
// the query string.
func parseStreamFilters(ctx *gin.Context) (streamFilters, error) {
	var f streamFilters

	if v := ctx.Query("protocol"); v != "" {
		switch defs.StreamProtocol(v) {
		case defs.StreamProtocolRTSP,
			defs.StreamProtocolRTSPS,
			defs.StreamProtocolRTMP,
			defs.StreamProtocolRTMPS,
			defs.StreamProtocolSRT,
			defs.StreamProtocolWebRTC,
			defs.StreamProtocolHLS:
			f.protocol = defs.StreamProtocol(v)
		default:
			return f, fmt.Errorf("invalid protocol: %s", v)
		}
	}

	if v := ctx.Query("camera_id"); v != "" {
		// Validate the form. We compare as strings against the canonical
		// camera_id strings stamped on streams (UUID strings), so the parse
		// is a guard, not a transform.
		if _, err := uuid.Parse(v); err != nil {
			return f, fmt.Errorf("invalid camera_id: %w", err)
		}
		f.cameraID = v
	}

	if v := ctx.Query("direction"); v != "" {
		switch defs.StreamDirection(v) {
		case defs.StreamDirectionPublish, defs.StreamDirectionRead:
			f.direction = defs.StreamDirection(v)
		default:
			return f, fmt.Errorf("invalid direction: %s", v)
		}
	}

	if v := ctx.Query("state"); v != "" {
		switch defs.StreamState(v) {
		case defs.StreamStateConnecting,
			defs.StreamStateActive,
			defs.StreamStateStalled,
			defs.StreamStateEnded,
			defs.StreamStateErrored:
			f.state = defs.StreamState(v)
		default:
			return f, fmt.Errorf("invalid state: %s", v)
		}
	}

	return f, nil
}

// matchesFilters returns true when the supplied stream passes every set
// filter. Unset filters (empty string) are wildcards.
func (f streamFilters) match(s *defs.Stream) bool {
	if f.protocol != "" && s.Protocol != f.protocol {
		return false
	}
	if f.cameraID != "" && s.CameraID != f.cameraID {
		return false
	}
	if f.direction != "" && s.Direction != f.direction {
		return false
	}
	if f.state != "" && s.State != f.state {
		return false
	}
	return true
}

// redactStream applies the API-boundary redaction policy to a single stream
// in place. RemoteAddr is PII (D4 / canonical-divergences.md) and is replaced
// with the literal "redacted" when non-empty; QueryString is Sensitive (D12)
// and is rewritten via redactQueryString.
func (a *API) redactStream(s *defs.Stream) {
	if s.RemoteAddr != "" {
		s.RemoteAddr = "redacted"
	}
	if s.QueryString != nil {
		q := redactQueryString(*s.QueryString)
		s.QueryString = &q
	}
	// Per-protocol redaction: TransportConnections carry their own
	// remote_addr; redact them too.
	if rt, ok := s.ProtocolSpecific.(*defs.ProtocolSpecificRTSP); ok {
		for i := range rt.TransportConnections {
			if rt.TransportConnections[i].RemoteAddr != "" {
				rt.TransportConnections[i].RemoteAddr = "redacted"
			}
		}
	}
	// WebRTC ICE candidate descriptors carry IPs (PII); blank them.
	if w, ok := s.ProtocolSpecific.(*defs.ProtocolSpecificWebRTC); ok {
		if len(w.LocalCandidates) > 0 {
			for i := range w.LocalCandidates {
				w.LocalCandidates[i] = "redacted"
			}
		}
		if len(w.RemoteCandidates) > 0 {
			for i := range w.RemoteCandidates {
				w.RemoteCandidates[i] = "redacted"
			}
		}
	}
}

// streamListResponse is the on-wire shape of GET /v1/streams. Mirrors the
// existing per-protocol list responses (item_count, page_count, items) so
// clients see a consistent envelope across the recorder API.
type streamListResponse struct {
	ItemCount int           `json:"item_count"`
	PageCount int           `json:"page_count"`
	Items     []defs.Stream `json:"items"`
}

// gatherStreams walks every protocol cluster, converts each session/conn to
// a canonical Stream via the Phase 1 translators, and returns the merged
// slice. Each cluster is gated on the corresponding interfaceIsEmpty check
// so that a recorder configured without (e.g.) RTMP simply emits no RTMP
// streams instead of erroring.
//
// Errors from a single cluster propagate up; the orchestrator can choose to
// short-circuit or aggregate later. We short-circuit here for parity with
// the old per-protocol handlers.
func (a *API) gatherStreams(tenantID string) ([]defs.Stream, error) {
	var out []defs.Stream

	// RTSP. Conns fold into Stream.protocol_specific.transport_connections,
	// keyed by the session's Conns UUID list.
	if !interfaceIsEmpty(a.RTSPServer) {
		streams, err := a.gatherRTSPStreams(a.RTSPServer, defs.StreamProtocolRTSP, tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}
	if !interfaceIsEmpty(a.RTSPSServer) {
		streams, err := a.gatherRTSPStreams(a.RTSPSServer, defs.StreamProtocolRTSPS, tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}

	// RTMP / RTMPS.
	if !interfaceIsEmpty(a.RTMPServer) {
		streams, err := a.gatherRTMPStreams(a.RTMPServer, defs.StreamProtocolRTMP, tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}
	if !interfaceIsEmpty(a.RTMPSServer) {
		streams, err := a.gatherRTMPStreams(a.RTMPSServer, defs.StreamProtocolRTMPS, tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}

	// SRT.
	if !interfaceIsEmpty(a.SRTServer) {
		streams, err := a.gatherSRTStreams(tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}

	// WebRTC.
	if !interfaceIsEmpty(a.WebRTCServer) {
		streams, err := a.gatherWebRTCStreams(tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}

	// HLS.
	if !interfaceIsEmpty(a.HLSServer) {
		streams, err := a.gatherHLSStreams(tenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, streams...)
	}

	// Sort for deterministic output: by StartedAt then ID. This is purely a
	// cosmetic tie-break for clients; pagination relies on this stability.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})

	return out, nil
}

// gatherRTSPStreams handles both rtsp and rtsps clusters (the two share an
// interface). The protocol parameter selects which translator variant to
// apply.
func (a *API) gatherRTSPStreams(
	server defs.APIRTSPServer,
	protocol defs.StreamProtocol,
	tenantID string,
) ([]defs.Stream, error) {
	sessions, err := server.APISessionsList()
	if err != nil {
		return nil, err
	}

	// Pull the conn list once; we look up each session's conns from this
	// in-memory map rather than calling APIConnsGet per UUID. The conn list
	// is small in practice (one per active session, plus stragglers).
	connsByID := map[uuid.UUID]*defs.APIRTSPConn{}
	if conns, err := server.APIConnsList(); err == nil && conns != nil {
		for i := range conns.Items {
			c := conns.Items[i]
			connsByID[c.ID] = &c
		}
	}
	// APIConnsList errors are non-fatal: the translator emits stub
	// transport_connections from the session's conn-uuid list when the
	// resolved conns slice is empty.

	out := make([]defs.Stream, 0, len(sessions.Items))
	for i := range sessions.Items {
		s := sessions.Items[i]
		matched := make([]*defs.APIRTSPConn, 0, len(s.Conns))
		for _, cid := range s.Conns {
			if c, ok := connsByID[cid]; ok {
				matched = append(matched, c)
			}
		}
		cameraID := streamCameraIDFromPath(s.Path)
		var stream defs.Stream
		if protocol == defs.StreamProtocolRTSPS {
			stream = defs.StreamFromRTSPSSession(&s, matched, cameraID, tenantID)
		} else {
			stream = defs.StreamFromRTSPSession(&s, matched, cameraID, tenantID)
		}
		out = append(out, stream)
	}
	return out, nil
}

// gatherRTMPStreams handles both rtmp and rtmps clusters.
func (a *API) gatherRTMPStreams(
	server defs.APIRTMPServer,
	protocol defs.StreamProtocol,
	tenantID string,
) ([]defs.Stream, error) {
	conns, err := server.APIConnsList()
	if err != nil {
		return nil, err
	}
	out := make([]defs.Stream, 0, len(conns.Items))
	for i := range conns.Items {
		c := conns.Items[i]
		cameraID := streamCameraIDFromPath(c.Path)
		var stream defs.Stream
		if protocol == defs.StreamProtocolRTMPS {
			stream = defs.StreamFromRTMPSConn(&c, cameraID, tenantID)
		} else {
			stream = defs.StreamFromRTMPConn(&c, cameraID, tenantID)
		}
		out = append(out, stream)
	}
	return out, nil
}

func (a *API) gatherSRTStreams(tenantID string) ([]defs.Stream, error) {
	conns, err := a.SRTServer.APIConnsList()
	if err != nil {
		return nil, err
	}
	out := make([]defs.Stream, 0, len(conns.Items))
	for i := range conns.Items {
		c := conns.Items[i]
		out = append(out, defs.StreamFromSRTConn(&c, streamCameraIDFromPath(c.Path), tenantID))
	}
	return out, nil
}

func (a *API) gatherWebRTCStreams(tenantID string) ([]defs.Stream, error) {
	sessions, err := a.WebRTCServer.APISessionsList()
	if err != nil {
		return nil, err
	}
	out := make([]defs.Stream, 0, len(sessions.Items))
	for i := range sessions.Items {
		s := sessions.Items[i]
		out = append(out, defs.StreamFromWebRTCSession(&s, streamCameraIDFromPath(s.Path), tenantID))
	}
	return out, nil
}

func (a *API) gatherHLSStreams(tenantID string) ([]defs.Stream, error) {
	sessions, err := a.HLSServer.APISessionsList()
	if err != nil {
		return nil, err
	}
	out := make([]defs.Stream, 0, len(sessions.Items))
	for i := range sessions.Items {
		s := sessions.Items[i]
		out = append(out, defs.StreamFromHLSSession(&s, streamCameraIDFromPath(s.Path), tenantID))
	}
	return out, nil
}

// onV1StreamsList serves GET /v1/streams. It walks every protocol cluster,
// converts each session/conn to a canonical Stream, applies the optional
// filters from the query string, paginates, redacts PII, and returns.
func (a *API) onV1StreamsList(ctx *gin.Context) {
	filters, err := parseStreamFilters(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	tenantID := a.tenantID()

	streams, err := a.gatherStreams(tenantID)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	// Filter post-conversion. The list is small (typically dozens, not
	// thousands), so a linear filter is fine and keeps the gather code
	// protocol-agnostic.
	filtered := make([]defs.Stream, 0, len(streams))
	for i := range streams {
		if filters.match(&streams[i]) {
			filtered = append(filtered, streams[i])
		}
	}

	resp := streamListResponse{
		Items: filtered,
	}
	resp.ItemCount = len(resp.Items)

	pageCount, err := paginate(&resp.Items, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	resp.PageCount = pageCount

	for i := range resp.Items {
		a.redactStream(&resp.Items[i])
	}

	ctx.JSON(http.StatusOK, resp)
}

// onV1StreamsGet serves GET /v1/streams/:id. It tries each protocol cluster's
// APISessionGet (or APIConnsGet) until one finds a match, then converts via
// the matching Phase 1 translator.
func (a *API) onV1StreamsGet(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	tenantID := a.tenantID()

	// Try each protocol cluster in turn. The session/conn UUID space is
	// sparse across protocols (collisions are vanishingly unlikely with
	// uuid.New) so first-match wins is fine.
	if !interfaceIsEmpty(a.RTSPServer) {
		if s, err := a.RTSPServer.APISessionsGet(id); err == nil && s != nil {
			conns := a.fetchRTSPConns(a.RTSPServer, s.Conns)
			out := defs.StreamFromRTSPSession(s, conns, streamCameraIDFromPath(s.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.RTSPSServer) {
		if s, err := a.RTSPSServer.APISessionsGet(id); err == nil && s != nil {
			conns := a.fetchRTSPConns(a.RTSPSServer, s.Conns)
			out := defs.StreamFromRTSPSSession(s, conns, streamCameraIDFromPath(s.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.RTMPServer) {
		if c, err := a.RTMPServer.APIConnsGet(id); err == nil && c != nil {
			out := defs.StreamFromRTMPConn(c, streamCameraIDFromPath(c.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, rtmp.ErrConnNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.RTMPSServer) {
		if c, err := a.RTMPSServer.APIConnsGet(id); err == nil && c != nil {
			out := defs.StreamFromRTMPSConn(c, streamCameraIDFromPath(c.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, rtmp.ErrConnNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.SRTServer) {
		if c, err := a.SRTServer.APIConnsGet(id); err == nil && c != nil {
			out := defs.StreamFromSRTConn(c, streamCameraIDFromPath(c.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, srt.ErrConnNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.WebRTCServer) {
		if s, err := a.WebRTCServer.APISessionsGet(id); err == nil && s != nil {
			out := defs.StreamFromWebRTCSession(s, streamCameraIDFromPath(s.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, webrtc.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.HLSServer) {
		if s, err := a.HLSServer.APISessionsGet(id); err == nil && s != nil {
			out := defs.StreamFromHLSSession(s, streamCameraIDFromPath(s.Path), tenantID)
			a.redactStream(&out)
			ctx.JSON(http.StatusOK, out)
			return
		} else if err != nil && !errors.Is(err, hls.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}

	a.writeError(ctx, http.StatusNotFound, fmt.Errorf("stream not found"))
}

// fetchRTSPConns resolves a session's Conns UUIDs to full APIRTSPConn
// entries via the server's APIConnsList. Errors are swallowed and an empty
// slice is returned; the translator handles the empty case by emitting stub
// transport_connections from the UUIDs alone.
func (a *API) fetchRTSPConns(server defs.APIRTSPServer, ids []uuid.UUID) []*defs.APIRTSPConn {
	if len(ids) == 0 {
		return nil
	}
	all, err := server.APIConnsList()
	if err != nil || all == nil {
		return nil
	}
	byID := map[uuid.UUID]*defs.APIRTSPConn{}
	for i := range all.Items {
		c := all.Items[i]
		byID[c.ID] = &c
	}
	out := make([]*defs.APIRTSPConn, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out
}

// onV1StreamsDelete serves DELETE /v1/streams/:id. It tries each protocol
// cluster's APISessionsKick / APIConnsKick until one succeeds. Returns 404
// if no cluster has the id.
func (a *API) onV1StreamsDelete(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	if !interfaceIsEmpty(a.RTSPServer) {
		if err := a.RTSPServer.APISessionsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.RTSPSServer) {
		if err := a.RTSPSServer.APISessionsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, rtsp.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.RTMPServer) {
		if err := a.RTMPServer.APIConnsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, rtmp.ErrConnNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.RTMPSServer) {
		if err := a.RTMPSServer.APIConnsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, rtmp.ErrConnNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.SRTServer) {
		if err := a.SRTServer.APIConnsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, srt.ErrConnNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.WebRTCServer) {
		if err := a.WebRTCServer.APISessionsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, webrtc.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	if !interfaceIsEmpty(a.HLSServer) {
		if err := a.HLSServer.APISessionsKick(id); err == nil {
			a.writeOK(ctx)
			return
		} else if !errors.Is(err, hls.ErrSessionNotFound) {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}

	a.writeError(ctx, http.StatusNotFound, fmt.Errorf("stream not found"))
}
