package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// invokeRecorderConfigHandler runs onV1RecorderConfigGet or
// onV1RecorderConfigPatch directly via a gin test context. Route
// registration is the orchestrator's job per the Phase 2 contract;
// these tests exercise handler behavior in isolation.
func invokeRecorderConfigHandler(api *API, method string, body []byte) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, "/v1/recorder/config", nil)
	} else {
		req = httptest.NewRequest(method, "/v1/recorder/config", bytes.NewReader(body))
	}
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	switch method {
	case http.MethodGet:
		api.onV1RecorderConfigGet(c)
	case http.MethodPatch:
		api.onV1RecorderConfigPatch(c)
	}
	return w.Code, w.Body.Bytes()
}

func TestV1RecorderConfigGet(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")

	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	code, body := invokeRecorderConfigHandler(api, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, true, out["api"])
	// PathDefaults / Paths must NOT be projected by the global view.
	_, hasPaths := out["paths"]
	require.False(t, hasPaths)
}

func TestV1RecorderConfigPatch(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	patch := []byte(`{"rtmp": false, "readTimeout": "7s"}`)
	code, _ := invokeRecorderConfigHandler(api, http.MethodPatch, patch)
	require.Equal(t, http.StatusOK, code)

	// Patch is dispatched through goroutine in the global handler; the
	// in-place a.Conf update is synchronous though, so we can verify
	// the new state immediately.
	code, body := invokeRecorderConfigHandler(api, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, false, out["rtmp"])
	require.Equal(t, "7s", out["readTimeout"])
}

func TestV1RecorderConfigPatchUnknownField(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	code, body := invokeRecorderConfigHandler(api, http.MethodPatch, []byte(`{"unknown": 1}`))
	require.Equal(t, http.StatusBadRequest, code)
	var env map[string]string
	require.NoError(t, json.Unmarshal(body, &env))
	require.Contains(t, env["error"], `unknown field "unknown"`)
}

func TestV1RecorderConfigPatchTenantMismatch(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	patch := []byte(`{"tenantId": "11111111-1111-1111-1111-111111111111"}`)
	code, body := invokeRecorderConfigHandler(api, http.MethodPatch, patch)
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, string(body), "tenant_id mismatch")
}

// TestV1RecorderConfigRecordingVolumesRoundTrip locks in the operator
// flow for ADR 0009 §D5 priority overrides: PATCH a recordingVolumes
// map onto /v1/recorder/config, then GET it back and confirm the
// values round-trip through the persistence layer cleanly.
func TestV1RecorderConfigRecordingVolumesRoundTrip(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	patch := []byte(`{"recordingVolumes": {"/srv/disk1": {"priority": 100}, "/srv/disk2": {"priority": 50}}}`)
	code, _ := invokeRecorderConfigHandler(api, http.MethodPatch, patch)
	require.Equal(t, http.StatusOK, code)

	code, body := invokeRecorderConfigHandler(api, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))

	rv, ok := out["recordingVolumes"].(map[string]any)
	require.True(t, ok, "recordingVolumes missing from GET body: %s", string(body))
	d1, ok := rv["/srv/disk1"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(100), d1["priority"])
	d2, ok := rv["/srv/disk2"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(50), d2["priority"])
}

// TestV1RecorderConfigRecordingVolumesNegativeRejected confirms that
// the conf.Validate() walk rejects negative priorities.
func TestV1RecorderConfigRecordingVolumesNegativeRejected(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	patch := []byte(`{"recordingVolumes": {"/srv/disk1": {"priority": -3}}}`)
	code, body := invokeRecorderConfigHandler(api, http.MethodPatch, patch)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, string(body), "negative priority")
}

func TestV1RecorderConfigPatchHookCredentialWarning(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	warned := false
	api := &API{
		Conf: cnf,
		Parent: &testParent{
			log: func(_ logger.Level, format string, _ ...any) {
				if strings.Contains(format, "credential fragment") {
					warned = true
				}
			},
		},
	}

	patch := []byte(`{"runOnConnect": "/bin/notify token=abc"}`)
	code, _ := invokeRecorderConfigHandler(api, http.MethodPatch, patch)
	require.Equal(t, http.StatusOK, code)
	require.True(t, warned, "expected credential-marker warning on hook patch")
}
