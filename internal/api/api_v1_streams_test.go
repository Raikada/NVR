package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/bluenviron/mediamtx/internal/servers/rtmp"
	"github.com/bluenviron/mediamtx/internal/servers/rtsp"
	"github.com/bluenviron/mediamtx/internal/servers/srt"
	"github.com/bluenviron/mediamtx/internal/servers/webrtc"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// streamsTestRTSPServer mirrors the existing testRTSPServer plumbing but is
// declared locally so the v1 streams tests don't tangle with the protocol
// tests we delete in this slice.
type streamsTestRTSPServer struct {
	conns    map[uuid.UUID]*defs.APIRTSPConn
	sessions map[uuid.UUID]*defs.APIRTSPSession
}

func (s *streamsTestRTSPServer) APIConnsList() (*defs.APIRTSPConnsList, error) {
	items := make([]defs.APIRTSPConn, 0, len(s.conns))
	for _, c := range s.conns {
		items = append(items, *c)
	}
	return &defs.APIRTSPConnsList{Items: items}, nil
}

func (s *streamsTestRTSPServer) APIConnsGet(id uuid.UUID) (*defs.APIRTSPConn, error) {
	c, ok := s.conns[id]
	if !ok {
		return nil, rtsp.ErrConnNotFound
	}
	return c, nil
}

func (s *streamsTestRTSPServer) APISessionsList() (*defs.APIRTSPSessionList, error) {
	items := make([]defs.APIRTSPSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		items = append(items, *sess)
	}
	return &defs.APIRTSPSessionList{Items: items}, nil
}

func (s *streamsTestRTSPServer) APISessionsGet(id uuid.UUID) (*defs.APIRTSPSession, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, rtsp.ErrSessionNotFound
	}
	return sess, nil
}

func (s *streamsTestRTSPServer) APISessionsKick(id uuid.UUID) error {
	if _, ok := s.sessions[id]; !ok {
		return rtsp.ErrSessionNotFound
	}
	delete(s.sessions, id)
	return nil
}

type streamsTestRTMPServer struct {
	conns map[uuid.UUID]*defs.APIRTMPConn
}

func (s *streamsTestRTMPServer) APIConnsList() (*defs.APIRTMPConnList, error) {
	items := make([]defs.APIRTMPConn, 0, len(s.conns))
	for _, c := range s.conns {
		items = append(items, *c)
	}
	return &defs.APIRTMPConnList{Items: items}, nil
}

func (s *streamsTestRTMPServer) APIConnsGet(id uuid.UUID) (*defs.APIRTMPConn, error) {
	c, ok := s.conns[id]
	if !ok {
		return nil, rtmp.ErrConnNotFound
	}
	return c, nil
}

func (s *streamsTestRTMPServer) APIConnsKick(id uuid.UUID) error {
	if _, ok := s.conns[id]; !ok {
		return rtmp.ErrConnNotFound
	}
	delete(s.conns, id)
	return nil
}

type streamsTestSRTServer struct {
	conns map[uuid.UUID]*defs.APISRTConn
}

func (s *streamsTestSRTServer) APIConnsList() (*defs.APISRTConnList, error) {
	items := make([]defs.APISRTConn, 0, len(s.conns))
	for _, c := range s.conns {
		items = append(items, *c)
	}
	return &defs.APISRTConnList{Items: items}, nil
}

func (s *streamsTestSRTServer) APIConnsGet(id uuid.UUID) (*defs.APISRTConn, error) {
	c, ok := s.conns[id]
	if !ok {
		return nil, srt.ErrConnNotFound
	}
	return c, nil
}

func (s *streamsTestSRTServer) APIConnsKick(id uuid.UUID) error {
	if _, ok := s.conns[id]; !ok {
		return srt.ErrConnNotFound
	}
	delete(s.conns, id)
	return nil
}

type streamsTestWebRTCServer struct {
	sessions map[uuid.UUID]*defs.APIWebRTCSession
}

func (s *streamsTestWebRTCServer) APISessionsList() (*defs.APIWebRTCSessionList, error) {
	items := make([]defs.APIWebRTCSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		items = append(items, *sess)
	}
	return &defs.APIWebRTCSessionList{Items: items}, nil
}

func (s *streamsTestWebRTCServer) APISessionsGet(id uuid.UUID) (*defs.APIWebRTCSession, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, webrtc.ErrSessionNotFound
	}
	return sess, nil
}

func (s *streamsTestWebRTCServer) APISessionsKick(id uuid.UUID) error {
	if _, ok := s.sessions[id]; !ok {
		return webrtc.ErrSessionNotFound
	}
	delete(s.sessions, id)
	return nil
}

type streamsTestHLSServer struct {
	sessions map[uuid.UUID]*defs.APIHLSSession
	muxers   map[string]*defs.APIHLSMuxer
}

func (s *streamsTestHLSServer) APISessionsList() (*defs.APIHLSSessionList, error) {
	items := make([]defs.APIHLSSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		items = append(items, *sess)
	}
	return &defs.APIHLSSessionList{Items: items}, nil
}

func (s *streamsTestHLSServer) APISessionsGet(id uuid.UUID) (*defs.APIHLSSession, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, hls.ErrSessionNotFound
	}
	return sess, nil
}

func (s *streamsTestHLSServer) APISessionsKick(id uuid.UUID) error {
	if _, ok := s.sessions[id]; !ok {
		return hls.ErrSessionNotFound
	}
	delete(s.sessions, id)
	return nil
}

func (s *streamsTestHLSServer) APIMuxersList() (*defs.APIHLSMuxerList, error) {
	items := make([]defs.APIHLSMuxer, 0, len(s.muxers))
	for _, m := range s.muxers {
		items = append(items, *m)
	}
	return &defs.APIHLSMuxerList{Items: items}, nil
}

func (s *streamsTestHLSServer) APIMuxersGet(name string) (*defs.APIHLSMuxer, error) {
	m, ok := s.muxers[name]
	if !ok {
		return nil, hls.ErrMuxerNotFound
	}
	return m, nil
}

func (s *streamsTestHLSServer) APIMuxerSnapshot(_ string) ([]byte, string, error) {
	return nil, "", hls.ErrMuxerNotFound
}

// v1StreamsFixture builds a populated API with one stream per protocol
// cluster and returns the API plus the IDs of every stream created plus the
// path-name strings used (for camera_id derivation checks).
func v1StreamsFixture(t *testing.T) (
	*API,
	map[string]uuid.UUID,
	map[string]string,
) {
	t.Helper()
	cnf := tempConf(t, "api: yes\n")

	now := time.Now()
	transport := "UDP"
	profile := "AVP"

	rtspID := uuid.New()
	rtspsID := uuid.New()
	rtspConnID := uuid.New()
	rtmpID := uuid.New()
	rtmpsID := uuid.New()
	srtID := uuid.New()
	webrtcID := uuid.New()
	hlsID := uuid.New()

	rtspSrv := &streamsTestRTSPServer{
		sessions: map[uuid.UUID]*defs.APIRTSPSession{
			rtspID: {
				ID:           rtspID,
				Created:      now,
				RemoteAddr:   "192.168.1.10:5000",
				State:        defs.APIRTSPSessionStatePublish,
				Path:         "rtsp_path",
				Query:        "token=secret123&p=abc",
				Transport:    &transport,
				Profile:      &profile,
				Conns:        []uuid.UUID{rtspConnID},
				InboundBytes: 1000,
			},
		},
		conns: map[uuid.UUID]*defs.APIRTSPConn{
			rtspConnID: {
				ID:            rtspConnID,
				Created:       now,
				RemoteAddr:    "192.168.1.10:5000",
				InboundBytes:  1000,
				OutboundBytes: 0,
			},
		},
	}

	rtspsSrv := &streamsTestRTSPServer{
		sessions: map[uuid.UUID]*defs.APIRTSPSession{
			rtspsID: {
				ID:         rtspsID,
				Created:    now,
				RemoteAddr: "192.168.1.11:5001",
				State:      defs.APIRTSPSessionStateRead,
				Path:       "rtsps_path",
				Transport:  &transport,
				Profile:    &profile,
			},
		},
		conns: map[uuid.UUID]*defs.APIRTSPConn{},
	}

	rtmpSrv := &streamsTestRTMPServer{
		conns: map[uuid.UUID]*defs.APIRTMPConn{
			rtmpID: {
				ID:           rtmpID,
				Created:      now,
				RemoteAddr:   "192.168.1.20:5000",
				State:        defs.APIRTMPConnStatePublish,
				Path:         "rtmp_path",
				InboundBytes: 5000,
			},
		},
	}

	rtmpsSrv := &streamsTestRTMPServer{
		conns: map[uuid.UUID]*defs.APIRTMPConn{
			rtmpsID: {
				ID:           rtmpsID,
				Created:      now,
				RemoteAddr:   "192.168.1.21:5001",
				State:        defs.APIRTMPConnStateRead,
				Path:         "rtmps_path",
				InboundBytes: 6000,
			},
		},
	}

	srtSrv := &streamsTestSRTServer{
		conns: map[uuid.UUID]*defs.APISRTConn{
			srtID: {
				ID:            srtID,
				Created:       now,
				RemoteAddr:    "192.168.1.30:5000",
				State:         defs.APISRTConnStatePublish,
				Path:          "srt_path",
				BytesReceived: 7000,
			},
		},
	}

	webrtcSrv := &streamsTestWebRTCServer{
		sessions: map[uuid.UUID]*defs.APIWebRTCSession{
			webrtcID: {
				ID:                        webrtcID,
				Created:                   now,
				RemoteAddr:                "192.168.1.40:5000",
				PeerConnectionEstablished: true,
				LocalCandidate:            "192.168.1.100:8000",
				RemoteCandidate:           "192.168.1.40:5000",
				State:                     defs.APIWebRTCSessionStateRead,
				Path:                      "webrtc_path",
				InboundBytes:              8000,
			},
		},
	}

	hlsSrv := &streamsTestHLSServer{
		sessions: map[uuid.UUID]*defs.APIHLSSession{
			hlsID: {
				ID:            hlsID,
				Created:       now,
				RemoteAddr:    "192.168.1.50:5000",
				Path:          "hls_path",
				Query:         "key=secret&p=q",
				OutboundBytes: 9000,
			},
		},
		muxers: map[string]*defs.APIHLSMuxer{},
	}

	api := &API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		Conf:         cnf,
		AuthManager:  test.NilAuthManager,
		Parent:       &testParent{},
		RTSPServer:   rtspSrv,
		RTSPSServer:  rtspsSrv,
		RTMPServer:   rtmpSrv,
		RTMPSServer:  rtmpsSrv,
		SRTServer:    srtSrv,
		WebRTCServer: webrtcSrv,
		HLSServer:    hlsSrv,
	}

	ids := map[string]uuid.UUID{
		"rtsp":   rtspID,
		"rtsps":  rtspsID,
		"rtmp":   rtmpID,
		"rtmps":  rtmpsID,
		"srt":    srtID,
		"webrtc": webrtcID,
		"hls":    hlsID,
	}
	paths := map[string]string{
		"rtsp":   "rtsp_path",
		"rtsps":  "rtsps_path",
		"rtmp":   "rtmp_path",
		"rtmps":  "rtmps_path",
		"srt":    "srt_path",
		"webrtc": "webrtc_path",
		"hls":    "hls_path",
	}
	return api, ids, paths
}

// streamHandlerKind disambiguates which v1/streams handler a test wants to
// invoke; the helper builds a synthetic *gin.Context for it.
type streamHandlerKind int

const (
	streamHandlerList streamHandlerKind = iota
	streamHandlerGet
	streamHandlerDelete
)

// invokeStreamHandler exercises the /v1/streams handler directly. It builds a
// *gin.Context with the supplied URL query string and (for get/delete) the
// path-id parameter, runs the handler, and returns the response status and
// raw body for the caller to assert on.
//
// We don't go through api.Initialize()'s router because the v1 routes are
// registered by the orchestrator, not by this slice (per Phase 2B contract).
// Direct context wiring keeps the tests self-contained.
func invokeStreamHandler(
	api *API,
	kind streamHandlerKind,
	rawQuery string,
	idParam string,
) (int, []byte) {
	return invokeStreamHandlerWithPrincipal(api, kind, rawQuery, idParam, nil)
}

// invokeStreamHandlerWithPrincipal mirrors invokeStreamHandler but stashes the
// supplied Principal on the gin.Context before calling the handler — the
// production-path equivalent of middlewareAuth setting it. Pass nil to
// reproduce the no-middleware path (handlers fall through to the
// unauthenticated principal).
func invokeStreamHandlerWithPrincipal(
	api *API,
	kind streamHandlerKind,
	rawQuery string,
	idParam string,
	principal *Principal,
) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()

	method := http.MethodGet
	if kind == streamHandlerDelete {
		method = http.MethodDelete
	}
	target := "/v1/streams"
	if idParam != "" {
		target = "/v1/streams/" + idParam
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(method, target, nil)

	c, _ := gin.CreateTestContext(w)
	c.Request = req
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	if principal != nil {
		setPrincipalOnContext(c, principal)
	}

	switch kind {
	case streamHandlerList:
		api.onV1StreamsList(c)
	case streamHandlerGet:
		api.onV1StreamsGet(c)
	case streamHandlerDelete:
		api.onV1StreamsDelete(c)
	}

	return w.Code, w.Body.Bytes()
}

func TestV1StreamsListReturnsAllProtocols(t *testing.T) {
	api, ids, paths := v1StreamsFixture(t)

	code, body := invokeStreamHandler(api, streamHandlerList, "", "")
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                      `json:"item_count"`
		PageCount int                      `json:"page_count"`
		Items     []map[string]interface{} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 7, resp.ItemCount)
	require.Len(t, resp.Items, 7)

	byProtocol := map[string]map[string]interface{}{}
	for _, s := range resp.Items {
		p, _ := s["protocol"].(string)
		byProtocol[p] = s
	}
	for proto, id := range ids {
		s, ok := byProtocol[proto]
		require.Truef(t, ok, "missing %s stream in list output", proto)
		require.Equal(t, id.String(), s["id"])
		require.Equal(t, cameraIDFromPathName(paths[proto]), s["camera_id"])
		require.Equal(t, "00000000-0000-0000-0000-000000000000", s["tenant_id"])
	}
}

func TestV1StreamsListFilterByProtocol(t *testing.T) {
	api, ids, _ := v1StreamsFixture(t)

	code, body := invokeStreamHandler(api, streamHandlerList, "protocol=rtmp", "")
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                      `json:"item_count"`
		Items     []map[string]interface{} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 1, resp.ItemCount)
	require.Equal(t, ids["rtmp"].String(), resp.Items[0]["id"])
	require.Equal(t, "rtmp", resp.Items[0]["protocol"])
}

func TestV1StreamsListFilterByCameraID(t *testing.T) {
	api, ids, paths := v1StreamsFixture(t)

	cameraID := cameraIDFromPathName(paths["webrtc"])
	q := url.Values{"camera_id": {cameraID}}.Encode()

	code, body := invokeStreamHandler(api, streamHandlerList, q, "")
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                      `json:"item_count"`
		Items     []map[string]interface{} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 1, resp.ItemCount)
	require.Equal(t, ids["webrtc"].String(), resp.Items[0]["id"])
}

func TestV1StreamsListFilterByDirection(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	code, body := invokeStreamHandler(api, streamHandlerList, "direction=publish", "")
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                      `json:"item_count"`
		Items     []map[string]interface{} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	for _, s := range resp.Items {
		require.Equal(t, "publish", s["direction"])
	}
	// publish: rtsp, rtmp, srt -> 3
	require.Equal(t, 3, resp.ItemCount)
}

func TestV1StreamsListFilterByState(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	// All translators stamp StreamStateActive today.
	code, body := invokeStreamHandler(api, streamHandlerList, "state=active", "")
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 7, resp.ItemCount)

	code, body = invokeStreamHandler(api, streamHandlerList, "state=ended", "")
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 0, resp.ItemCount)
}

func TestV1StreamsListInvalidFilters(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	for _, qs := range []string{
		"protocol=bogus",
		"camera_id=not-a-uuid",
		"direction=sideways",
		"state=spinning",
	} {
		t.Run(qs, func(t *testing.T) {
			code, _ := invokeStreamHandler(api, streamHandlerList, qs, "")
			require.Equal(t, http.StatusBadRequest, code)
		})
	}
}

func TestV1StreamsListPagination(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	code, body := invokeStreamHandler(api, streamHandlerList,
		"items_per_page=3&page=0", "")
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                      `json:"item_count"`
		PageCount int                      `json:"page_count"`
		Items     []map[string]interface{} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 7, resp.ItemCount, "item_count is the unpaginated count")
	require.Equal(t, 3, resp.PageCount)
	require.Len(t, resp.Items, 3)
}

func TestV1StreamsListRedaction(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	// RTSP: query has token=secret123 which must be redacted.
	code, body := invokeStreamHandler(api, streamHandlerList, "protocol=rtsp", "")
	require.Equal(t, http.StatusOK, code)
	bodyStr := string(body)
	require.NotContains(t, bodyStr, "secret123",
		"token value must be redacted from query_string")
	require.Contains(t, bodyStr, "redacted")
	// remote_addr must be redacted.
	require.NotContains(t, bodyStr, "192.168.1.10:5000",
		"remote_addr must be redacted")

	// transport_connections fold-in's remote_addr is also subject to
	// redaction.
	require.NotContains(t, string(body), "192.168.1.10",
		"transport_connections remote_addr must be redacted")

	// HLS: key=secret must be redacted.
	code, body = invokeStreamHandler(api, streamHandlerList, "protocol=hls", "")
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, string(body), "key=secret",
		"key= value must be redacted")

	// WebRTC: ICE candidates carry IPs (PII); must be redacted.
	code, body = invokeStreamHandler(api, streamHandlerList, "protocol=webrtc", "")
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, string(body), "192.168.1.100:8000",
		"local_candidates must be redacted")
	require.NotContains(t, string(body), "192.168.1.40:5000",
		"remote_candidates must be redacted")
}

func TestV1StreamsGetByID(t *testing.T) {
	api, ids, _ := v1StreamsFixture(t)

	for _, proto := range []string{"rtsp", "rtsps", "rtmp", "rtmps", "srt", "webrtc", "hls"} {
		t.Run(proto, func(t *testing.T) {
			id := ids[proto]
			code, body := invokeStreamHandler(api, streamHandlerGet, "", id.String())
			require.Equal(t, http.StatusOK, code)

			var raw map[string]interface{}
			require.NoError(t, json.Unmarshal(body, &raw))
			require.Equal(t, id.String(), raw["id"])
			require.Equal(t, proto, raw["protocol"])
			require.Equal(t, "redacted", raw["remote_addr"],
				"remote_addr must be redacted at the API boundary")
			// tenant_id must be stamped.
			require.Equal(t, "00000000-0000-0000-0000-000000000000", raw["tenant_id"])
		})
	}
}

func TestV1StreamsGetNotFound(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)
	missing := uuid.New().String()
	code, _ := invokeStreamHandler(api, streamHandlerGet, "", missing)
	require.Equal(t, http.StatusNotFound, code)
}

func TestV1StreamsGetInvalidID(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)
	code, _ := invokeStreamHandler(api, streamHandlerGet, "", "not-a-uuid")
	require.Equal(t, http.StatusBadRequest, code)
}

func TestV1StreamsDelete(t *testing.T) {
	for _, proto := range []string{"rtsp", "rtsps", "rtmp", "rtmps", "srt", "webrtc", "hls"} {
		t.Run(proto, func(t *testing.T) {
			api, ids, _ := v1StreamsFixture(t)
			id := ids[proto]
			code, body := invokeStreamHandler(api, streamHandlerDelete, "", id.String())
			require.Equal(t, http.StatusOK, code)
			// kick handlers return the standard {"status":"ok"} body.
			require.True(t, strings.Contains(string(body), "\"ok\""),
				"delete should return the canonical OK envelope")
		})
	}
}

func TestV1StreamsDeleteNotFound(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)
	missing := uuid.New().String()
	code, _ := invokeStreamHandler(api, streamHandlerDelete, "", missing)
	require.Equal(t, http.StatusNotFound, code)
}

// TestV1StreamsListPIIRedactedForUnauthenticated covers the
// fail-closed default per ADR 0010 D2 and recorder canonical-divergence
// D5: with no Principal on the context (no middleware ran), the
// handler must redact every PII field on the response.
func TestV1StreamsListPIIRedactedForUnauthenticated(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	code, body := invokeStreamHandlerWithPrincipal(
		api, streamHandlerList, "", "", nil,
	)
	require.Equal(t, http.StatusOK, code)
	bodyStr := string(body)

	// Stream.remote_addr (every protocol).
	require.NotContains(t, bodyStr, "192.168.1.10:5000")
	require.NotContains(t, bodyStr, "192.168.1.20:5000")
	require.NotContains(t, bodyStr, "192.168.1.40:5000")
	require.NotContains(t, bodyStr, "192.168.1.50:5000")
	// WebRTC ICE candidate IPs.
	require.NotContains(t, bodyStr, "192.168.1.100:8000")
}

// TestV1StreamsListPIIRedactedForServiceAccountWithoutPermission covers
// the pre-OQ10 internal/HTTP auth path with the default
// (globalPIIReadGrant=false) operator config: the static service-account
// Principal carries an empty Scope, so PII stays redacted.
func TestV1StreamsListPIIRedactedForServiceAccountWithoutPermission(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	svcPrincipal := &Principal{
		PrincipalKind: defs.AuditActorKindServiceAccount,
		// Scope intentionally empty — the pre-OQ10 default.
	}

	code, body := invokeStreamHandlerWithPrincipal(
		api, streamHandlerList, "", "", svcPrincipal,
	)
	require.Equal(t, http.StatusOK, code)
	bodyStr := string(body)

	require.NotContains(t, bodyStr, "192.168.1.10:5000",
		"remote_addr must stay redacted for a service account without session.pii.read")
	require.NotContains(t, bodyStr, "192.168.1.100:8000",
		"WebRTC ICE candidates must stay redacted for a service account without session.pii.read")
	require.Contains(t, bodyStr, "redacted")
}

// TestV1StreamsListPIIUnmaskedForPrincipalWithPermission covers the
// happy path: a JWT-authed cloud_user Principal whose `scope` claim
// includes session.pii.read sees the raw PII fields.
func TestV1StreamsListPIIUnmaskedForPrincipalWithPermission(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	cloudPrincipal := &Principal{
		Sub:           "user-uuid",
		PrincipalKind: defs.AuditActorKindCloudUser,
		Scope:         []string{"stream.list", PermSessionPIIRead},
	}

	code, body := invokeStreamHandlerWithPrincipal(
		api, streamHandlerList, "", "", cloudPrincipal,
	)
	require.Equal(t, http.StatusOK, code)
	bodyStr := string(body)

	// Stream.remote_addr unmasked.
	require.Contains(t, bodyStr, "192.168.1.10:5000",
		"RTSP remote_addr must be unmasked when principal holds session.pii.read")
	require.Contains(t, bodyStr, "192.168.1.20:5000",
		"RTMP remote_addr must be unmasked when principal holds session.pii.read")
	// WebRTC ICE candidates unmasked.
	require.Contains(t, bodyStr, "192.168.1.100:8000",
		"WebRTC local_candidates must be unmasked when principal holds session.pii.read")
	require.Contains(t, bodyStr, "192.168.1.40:5000",
		"WebRTC remote_candidates must be unmasked when principal holds session.pii.read")

	// Sensitive query-string redaction is independent of the PII gate
	// (D12 closure): credential-pattern values still get scrubbed.
	require.NotContains(t, bodyStr, "secret123",
		"query_string credential-pattern redaction must apply even with session.pii.read")
}

// TestV1StreamsListPIIUnmaskedViaGlobalPIIReadGrant covers the operator
// escape-hatch: a pre-OQ10 deployment whose recorder config has
// globalPIIReadGrant=true sees PII unmasked even though its
// authentication path produces a static service-account Principal with
// no per-user scope claim. The grant injects session.pii.read into that
// Principal at middleware time.
func TestV1StreamsListPIIUnmaskedViaGlobalPIIReadGrant(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)

	// Simulate the static-auth principal that middlewareAuth produces
	// when globalPIIReadGrant=true on the recorder bootstrap config.
	// The unit-level guarantee that the conf flag flows into
	// principalFromAuthClaims is covered by
	// TestPrincipalFromAuthClaimsPreOQ10WithGlobalGrant; here we
	// exercise the handler-side behavior under that resolved
	// Principal.
	staticPrincipal := principalFromAuthClaims(
		auth.Claims{Method: conf.AuthMethodInternal},
		true, // globalPIIReadGrant
	)
	require.True(t, staticPrincipal.HasPermission(PermSessionPIIRead))

	code, body := invokeStreamHandlerWithPrincipal(
		api, streamHandlerList, "", "", staticPrincipal,
	)
	require.Equal(t, http.StatusOK, code)
	bodyStr := string(body)

	require.Contains(t, bodyStr, "192.168.1.10:5000",
		"RTSP remote_addr must be unmasked when globalPIIReadGrant=true")
	require.Contains(t, bodyStr, "192.168.1.100:8000",
		"WebRTC local_candidates must be unmasked when globalPIIReadGrant=true")
}

func TestV1StreamsListTenantIDStamp(t *testing.T) {
	api, _, _ := v1StreamsFixture(t)
	code, body := invokeStreamHandler(api, streamHandlerList, "", "")
	require.Equal(t, http.StatusOK, code)
	var resp struct {
		Items []map[string]interface{} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.NotEmpty(t, resp.Items)
	for _, s := range resp.Items {
		require.Equal(t, "00000000-0000-0000-0000-000000000000", s["tenant_id"])
	}
}
