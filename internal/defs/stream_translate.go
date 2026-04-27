package defs

import (
	"strconv"
)

// streamDirectionFromRTSPState picks the canonical Stream direction from
// an RTSP-session state. RTSP idle is mapped to "read" by convention
// (the session has not yet committed to either role; it is more often
// a reader at this stage).
func streamDirectionFromRTSPState(s APIRTSPSessionState) StreamDirection {
	if s == APIRTSPSessionStatePublish {
		return StreamDirectionPublish
	}
	return StreamDirectionRead
}

// streamDirectionFromRTMPState picks the canonical direction for an
// RTMP/RTMPS connection. Idle defaults to "read" by convention.
func streamDirectionFromRTMPState(s APIRTMPConnState) StreamDirection {
	if s == APIRTMPConnStatePublish {
		return StreamDirectionPublish
	}
	return StreamDirectionRead
}

// streamDirectionFromSRTState picks the canonical direction for an SRT
// connection. Idle defaults to "read" by convention.
func streamDirectionFromSRTState(s APISRTConnState) StreamDirection {
	if s == APISRTConnStatePublish {
		return StreamDirectionPublish
	}
	return StreamDirectionRead
}

// streamDirectionFromWebRTCState picks the canonical direction for a
// WebRTC session.
func streamDirectionFromWebRTCState(s APIWebRTCSessionState) StreamDirection {
	if s == APIWebRTCSessionStatePublish {
		return StreamDirectionPublish
	}
	return StreamDirectionRead
}

// transportConnectionsFromRTSP folds RTSPConn / RTSPSConn data into the
// transport_connections[] array on a Stream's protocol_specific block.
//
// The session carries only conn UUIDs; the caller resolves them to full
// APIRTSPConn entries and supplies them via conns. If conns is nil the
// returned slice contains stub entries with only the IDs populated, so
// callers that haven't wired conn lookup yet still produce a coherent
// shape.
func transportConnectionsFromRTSP(session *APIRTSPSession, conns []*APIRTSPConn) []TransportConnection {
	if session == nil {
		return nil
	}
	if len(conns) > 0 {
		out := make([]TransportConnection, 0, len(conns))
		for _, c := range conns {
			if c == nil {
				continue
			}
			tc := TransportConnection{
				ID:            c.ID.String(),
				RemoteAddr:    c.RemoteAddr,
				BytesInbound:  int64(c.InboundBytes),
				BytesOutbound: int64(c.OutboundBytes),
			}
			if c.Tunnel != "" {
				tn := c.Tunnel
				tc.Tunnel = &tn
			}
			out = append(out, tc)
		}
		return out
	}
	// Fallback: emit stub entries keyed by the session's conn-uuid list.
	if len(session.Conns) == 0 {
		return nil
	}
	out := make([]TransportConnection, 0, len(session.Conns))
	for _, id := range session.Conns {
		out = append(out, TransportConnection{ID: id.String()})
	}
	return out
}

// derefStringPtr returns a string from a *string, "" when nil.
func derefStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// rtspProfileFromPtr returns a profile string, "" when nil.
func rtspProfileFromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// rtspTransportFromString maps the per-session RTSP transport string
// onto the canonical RTSPTransport enum.
func rtspTransportFromString(s string) RTSPTransport {
	switch s {
	case "udp":
		return RTSPTransportUDP
	case "multicast", "udp_multicast":
		return RTSPTransportUDPMulticast
	default:
		return RTSPTransportTCP
	}
}

// streamFromRTSPInternal is the shared body for RTSP and RTSPS conversion;
// the difference is only the canonical Protocol enum value.
func streamFromRTSPInternal(
	s *APIRTSPSession,
	conns []*APIRTSPConn,
	cameraID string,
	tenantID string,
	protocol StreamProtocol,
) Stream {
	if s == nil {
		return Stream{}
	}
	transport := rtspTransportFromString(derefStringPtr(s.Transport))
	profile := rtspProfileFromPtr(s.Profile)
	ps := &ProtocolSpecificRTSP{
		Transport:              transport,
		Profile:                profile,
		TransportConnections:   transportConnectionsFromRTSP(s, conns),
		RTPPacketsInbound:      int64(s.InboundRTPPackets),
		RTPPacketsLost:         int64(s.InboundRTPPacketsLost),
		RTPPacketsInError:      int64(s.InboundRTPPacketsInError),
		RTPJitter:              int64(s.InboundRTPPacketsJitter),
		RTCPPacketsInbound:     int64(s.InboundRTCPPackets),
		RTCPPacketsInError:     int64(s.InboundRTCPPacketsInError),
		RTPPacketsOutbound:     int64(s.OutboundRTPPackets),
		RTPPacketsReportedLost: int64(s.OutboundRTPPacketsReportedLost),
		RTPPacketsDiscarded:    int64(s.OutboundRTPPacketsDiscarded),
		RTCPPacketsOutbound:    int64(s.OutboundRTCPPackets),
	}

	out := Stream{
		ID:               s.ID.String(),
		TenantID:         tenantID,
		CameraID:         cameraID,
		Protocol:         protocol,
		Direction:        streamDirectionFromRTSPState(s.State),
		State:            StreamStateActive,
		RemoteAddr:       s.RemoteAddr,
		BytesInbound:     int64(s.InboundBytes),
		BytesOutbound:    int64(s.OutboundBytes),
		ProtocolSpecific: ps,
		StartedAt:        s.Created,
	}
	if s.User != "" {
		u := s.User
		out.UserID = &u
	}
	if s.Query != "" {
		q := s.Query
		out.QueryString = &q
	}
	return out
}

// StreamFromRTSPSession converts a defs.APIRTSPSession (the existing
// per-protocol shape produced by the RTSP server) into a canonical Stream
// per ADR 0009 §D2. The caller supplies any RTSPConn entries belonging to
// the session via conns; these fold into protocol_specific.transport_connections.
//
// query_string and remote_addr are passed through as-is; the API handler
// is responsible for redacting them before serialization (existing
// redactQueryString / redactSourceURL D4/D12 fixes).
func StreamFromRTSPSession(s *APIRTSPSession, conns []*APIRTSPConn, cameraID, tenantID string) Stream {
	return streamFromRTSPInternal(s, conns, cameraID, tenantID, StreamProtocolRTSP)
}

// StreamFromRTSPSSession is the RTSPS variant; identical to
// StreamFromRTSPSession but stamps protocol=rtsps on the Stream so the
// TLS distinction is preserved.
func StreamFromRTSPSSession(s *APIRTSPSession, conns []*APIRTSPConn, cameraID, tenantID string) Stream {
	return streamFromRTSPInternal(s, conns, cameraID, tenantID, StreamProtocolRTSPS)
}

// streamFromRTMPInternal is the shared body for RTMP / RTMPS conversion.
func streamFromRTMPInternal(
	c *APIRTMPConn,
	cameraID string,
	tenantID string,
	protocol StreamProtocol,
) Stream {
	if c == nil {
		return Stream{}
	}
	out := Stream{
		ID:                      c.ID.String(),
		TenantID:                tenantID,
		CameraID:                cameraID,
		Protocol:                protocol,
		Direction:               streamDirectionFromRTMPState(c.State),
		State:                   StreamStateActive,
		RemoteAddr:              c.RemoteAddr,
		BytesInbound:            int64(c.InboundBytes),
		BytesOutbound:           int64(c.OutboundBytes),
		FramesDiscardedOutbound: int(c.OutboundFramesDiscarded),
		ProtocolSpecific: &ProtocolSpecificRTMP{
			State: RTMPState(c.State),
		},
		StartedAt: c.Created,
	}
	if c.User != "" {
		u := c.User
		out.UserID = &u
	}
	if c.Query != "" {
		q := c.Query
		out.QueryString = &q
	}
	return out
}

// StreamFromRTMPConn converts a defs.APIRTMPConn into a canonical Stream.
func StreamFromRTMPConn(c *APIRTMPConn, cameraID, tenantID string) Stream {
	return streamFromRTMPInternal(c, cameraID, tenantID, StreamProtocolRTMP)
}

// StreamFromRTMPSConn is the RTMPS variant.
func StreamFromRTMPSConn(c *APIRTMPConn, cameraID, tenantID string) Stream {
	return streamFromRTMPInternal(c, cameraID, tenantID, StreamProtocolRTMPS)
}

// StreamFromSRTConn converts a defs.APISRTConn into a canonical Stream.
//
// SRT carries a great many counters; the canonical Stream picks the
// summary set ADR 0009 calls out (packets_received, packets_lost,
// packets_dropped, retransmits) plus the Stream-level byte counters.
// Encryption_state is left empty until Phase 2 surfaces a per-connection
// crypto state on APISRTConn.
func StreamFromSRTConn(c *APISRTConn, cameraID, tenantID string) Stream {
	if c == nil {
		return Stream{}
	}
	out := Stream{
		ID:                      c.ID.String(),
		TenantID:                tenantID,
		CameraID:                cameraID,
		Protocol:                StreamProtocolSRT,
		Direction:               streamDirectionFromSRTState(c.State),
		State:                   StreamStateActive,
		RemoteAddr:              c.RemoteAddr,
		BytesInbound:            int64(c.BytesReceived),
		BytesOutbound:           int64(c.BytesSent),
		FramesDiscardedOutbound: int(c.OutboundFramesDiscarded),
		ProtocolSpecific: &ProtocolSpecificSRT{
			PacketsReceived: int64(c.PacketsReceived),
			PacketsLost:     int64(c.PacketsReceivedLoss),
			PacketsDropped:  int64(c.PacketsReceivedDrop),
			Retransmits:     int64(c.PacketsRetrans),
			// EncryptionState is not surfaced by APISRTConn today.
			EncryptionState: "",
		},
		StartedAt: c.Created,
	}
	if c.User != "" {
		u := c.User
		out.UserID = &u
	}
	if c.Query != "" {
		q := c.Query
		out.QueryString = &q
	}
	return out
}

// StreamFromWebRTCSession converts a defs.APIWebRTCSession into a
// canonical Stream.
//
// peer_connection_state is approximated from the boolean
// PeerConnectionEstablished today; ICE state is not surfaced separately
// by the existing API shape, so it stays empty until Phase 2 refines.
// Local/remote candidate descriptors arrive as single strings on
// APIWebRTCSession; the canonical shape expects []string for future
// candidate-list expansion, so we wrap each in a one-element slice when
// non-empty.
func StreamFromWebRTCSession(s *APIWebRTCSession, cameraID, tenantID string) Stream {
	if s == nil {
		return Stream{}
	}
	pcState := "new"
	if s.PeerConnectionEstablished {
		pcState = "connected"
	}
	ps := &ProtocolSpecificWebRTC{
		PeerConnectionState: pcState,
		ICEState:            "",
	}
	if s.LocalCandidate != "" {
		ps.LocalCandidates = []string{s.LocalCandidate}
	}
	if s.RemoteCandidate != "" {
		ps.RemoteCandidates = []string{s.RemoteCandidate}
	}

	out := Stream{
		ID:                      s.ID.String(),
		TenantID:                tenantID,
		CameraID:                cameraID,
		Protocol:                StreamProtocolWebRTC,
		Direction:               streamDirectionFromWebRTCState(s.State),
		State:                   StreamStateActive,
		RemoteAddr:              s.RemoteAddr,
		BytesInbound:            int64(s.InboundBytes),
		BytesOutbound:           int64(s.OutboundBytes),
		FramesDiscardedOutbound: int(s.OutboundFramesDiscarded),
		ProtocolSpecific:        ps,
		StartedAt:               s.Created,
	}
	if s.User != "" {
		u := s.User
		out.UserID = &u
	}
	if s.Query != "" {
		q := s.Query
		out.QueryString = &q
	}
	return out
}

// StreamFromHLSSession converts a defs.APIHLSSession into a canonical
// Stream. HLS sessions are read-only by definition (HTTP-pull); direction
// is always "read".
//
// HLS doesn't expose a per-session "last_request_at" today; the existing
// last-request timestamp lives on APIHLSMuxer (one per camera path), not
// on APIHLSSession. Until Phase 2 wires that through, the canonical
// last_request_at is left nil here. Callers that have a muxer reference
// can populate it separately.
func StreamFromHLSSession(s *APIHLSSession, cameraID, tenantID string) Stream {
	if s == nil {
		return Stream{}
	}
	out := Stream{
		ID:               s.ID.String(),
		TenantID:         tenantID,
		CameraID:         cameraID,
		Protocol:         StreamProtocolHLS,
		Direction:        StreamDirectionRead,
		State:            StreamStateActive,
		RemoteAddr:       s.RemoteAddr,
		BytesInbound:     0,
		BytesOutbound:    int64(s.OutboundBytes),
		ProtocolSpecific: &ProtocolSpecificHLS{OutboundBytes: int64(s.OutboundBytes)},
		StartedAt:        s.Created,
	}
	if s.User != "" {
		u := s.User
		out.UserID = &u
	}
	if s.Query != "" {
		q := s.Query
		out.QueryString = &q
	}
	return out
}

// FormatRTPJitter is a small helper for callers that need to display
// jitter as a string (the canonical type uses int64 microseconds while
// the per-protocol API shape uses float64). Unused at this layer; kept
// for handler-side ergonomics.
func FormatRTPJitter(jitter float64) string {
	return strconv.FormatFloat(jitter, 'f', -1, 64)
}
