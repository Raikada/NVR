package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// hlsMuxerOnlyServer implements the APIHLSServer surface (the muxers
// half of it; sessions are unused at this surface).
type hlsMuxerOnlyServer struct {
	muxers map[string]*defs.APIHLSMuxer
}

func (s *hlsMuxerOnlyServer) APIMuxersList() (*defs.APIHLSMuxerList, error) {
	items := make([]defs.APIHLSMuxer, 0, len(s.muxers))
	for _, m := range s.muxers {
		items = append(items, *m)
	}
	return &defs.APIHLSMuxerList{Items: items}, nil
}

func (s *hlsMuxerOnlyServer) APIMuxersGet(name string) (*defs.APIHLSMuxer, error) {
	m, ok := s.muxers[name]
	if !ok {
		return nil, hls.ErrMuxerNotFound
	}
	return m, nil
}

func (s *hlsMuxerOnlyServer) APISessionsList() (*defs.APIHLSSessionList, error) {
	return &defs.APIHLSSessionList{}, nil
}

func (s *hlsMuxerOnlyServer) APISessionsGet(_ uuid.UUID) (*defs.APIHLSSession, error) {
	return nil, hls.ErrSessionNotFound
}

func (s *hlsMuxerOnlyServer) APISessionsKick(_ uuid.UUID) error {
	return hls.ErrSessionNotFound
}

func invokeHLSMuxersHandler(api *API, kind string, idParam string) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	target := "/v1/recorder/hls-muxers"
	if idParam != "" {
		target += "/" + idParam
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	switch kind {
	case "list":
		api.onV1RecorderHLSMuxersList(c)
	case "get":
		api.onV1RecorderHLSMuxersGet(c)
	}
	return w.Code, w.Body.Bytes()
}

func TestV1RecorderHLSMuxersList(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	hlsSrv := &hlsMuxerOnlyServer{
		muxers: map[string]*defs.APIHLSMuxer{
			"cam_a": {Path: "cam_a", Created: now, OutboundBytes: 100},
			"cam_b": {Path: "cam_b", Created: now.Add(time.Minute), OutboundBytes: 200},
		},
	}
	api := &API{
		Conf:        cnf,
		HLSServer:   hlsSrv,
		Parent:      &testParent{},
		ReadTimeout: conf.Duration(10 * time.Second),
	}

	code, body := invokeHLSMuxersHandler(api, "list", "")
	require.Equal(t, http.StatusOK, code)

	var out defs.APIHLSMuxerList
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, 2, out.ItemCount)
	require.Len(t, out.Items, 2)
	for _, m := range out.Items {
		require.Equal(t, "00000000-0000-0000-0000-000000000000", m.TenantID)
	}
}

func TestV1RecorderHLSMuxersGetByCameraID(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &hlsMuxerOnlyServer{
		muxers: map[string]*defs.APIHLSMuxer{
			"cam_a": {Path: "cam_a", OutboundBytes: 9999},
		},
	}
	api := &API{
		Conf:      cnf,
		HLSServer: hlsSrv,
		Parent:    &testParent{},
	}

	cameraID := cameraIDFromPathName("cam_a")
	code, body := invokeHLSMuxersHandler(api, "get", cameraID)
	require.Equal(t, http.StatusOK, code)

	var out defs.APIHLSMuxer
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, "cam_a", out.Path)
	require.Equal(t, uint64(9999), out.OutboundBytes)
}

func TestV1RecorderHLSMuxersGetUnknownCamera(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &hlsMuxerOnlyServer{muxers: map[string]*defs.APIHLSMuxer{}}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	// Valid UUID, but not derived from any configured path.
	code, body := invokeHLSMuxersHandler(api, "get", uuid.New().String())
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, string(body), "camera not found")
}

func TestV1RecorderHLSMuxersGetInvalidUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	hlsSrv := &hlsMuxerOnlyServer{muxers: map[string]*defs.APIHLSMuxer{}}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	code, body := invokeHLSMuxersHandler(api, "get", "not-a-uuid")
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, string(body), "invalid camera id")
}
