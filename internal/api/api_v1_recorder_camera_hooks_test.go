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
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func invokeCameraHooksHandler(api *API, method, idParam string, body []byte) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	target := "/v1/recorder/cameras/" + idParam + "/hooks"
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	switch method {
	case http.MethodGet:
		api.onV1RecorderCameraHooksGet(c)
	case http.MethodPatch:
		api.onV1RecorderCameraHooksPatch(c)
	}
	return w.Code, w.Body.Bytes()
}

func TestV1RecorderCameraHooksGet(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n    runOnInit: /bin/echo init\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	code, body := invokeCameraHooksHandler(api, http.MethodGet, cameraID, nil)
	require.Equal(t, http.StatusOK, code)

	var out CameraHooks
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, "/bin/echo init", out.RunOnInit)
	require.Equal(t, "00000000-0000-0000-0000-000000000000", out.TenantID)
}

func TestV1RecorderCameraHooksGetUnknown(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	code, body := invokeCameraHooksHandler(api, http.MethodGet, uuid.New().String(), nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, string(body), "camera not found")
}

func TestV1RecorderCameraHooksPatch(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	patch := []byte(`{"runOnReady": "/bin/echo ready"}`)
	code, _ := invokeCameraHooksHandler(api, http.MethodPatch, cameraID, patch)
	require.Equal(t, http.StatusOK, code)

	code, body := invokeCameraHooksHandler(api, http.MethodGet, cameraID, nil)
	require.Equal(t, http.StatusOK, code)
	var out CameraHooks
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, "/bin/echo ready", out.RunOnReady)
}

func TestV1RecorderCameraHooksPatchCredentialWarning(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
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

	cameraID := cameraIDFromPathName("cam_a")
	patch := []byte(`{"runOnRecordSegmentCreate": "/bin/post --secret=abc"}`)
	code, _ := invokeCameraHooksHandler(api, http.MethodPatch, cameraID, patch)
	require.Equal(t, http.StatusOK, code)
	require.True(t, warned, "expected credential-marker warning on hook patch with embedded secret")
}

func TestV1RecorderCameraHooksPatchInvalidUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	code, body := invokeCameraHooksHandler(api, http.MethodPatch, "not-a-uuid", []byte(`{}`))
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, string(body), "invalid camera id")
}

func TestV1RecorderCameraHooksPatchTenantMismatch(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	patch := []byte(`{"tenant_id": "11111111-1111-1111-1111-111111111111"}`)
	code, body := invokeCameraHooksHandler(api, http.MethodPatch, cameraID, patch)
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, string(body), "tenant_id mismatch")
}
