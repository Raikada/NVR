package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func invokeSourceConfigHandler(api *API, method, idParam string, body []byte) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	target := "/v1/recorder/cameras/" + idParam + "/source-config"
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
		api.onV1RecorderCameraSourceConfigGet(c)
	case http.MethodPatch:
		api.onV1RecorderCameraSourceConfigPatch(c)
	}
	return w.Code, w.Body.Bytes()
}

func TestV1RecorderSourceConfigGet(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: rpiCamera\n    rpiCameraCamID: 0\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	code, body := invokeSourceConfigHandler(api, http.MethodGet, cameraID, nil)
	require.Equal(t, http.StatusOK, code)

	var out RPiCameraSourceConfig
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, "00000000-0000-0000-0000-000000000000", out.TenantID)
	// rpiCameraWidth has a non-zero default per setDefaults.
	require.Equal(t, uint(1920), out.RPICameraWidth)
}

func TestV1RecorderSourceConfigGetUnknown(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	code, body := invokeSourceConfigHandler(api, http.MethodGet, uuid.New().String(), nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, string(body), "camera not found")
}

func TestV1RecorderSourceConfigPatch(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: rpiCamera\n    rpiCameraCamID: 0\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	patch := []byte(`{"rpiCameraWidth": 1280, "rpiCameraHeight": 720}`)
	code, _ := invokeSourceConfigHandler(api, http.MethodPatch, cameraID, patch)
	require.Equal(t, http.StatusOK, code)

	code, body := invokeSourceConfigHandler(api, http.MethodGet, cameraID, nil)
	require.Equal(t, http.StatusOK, code)
	var out RPiCameraSourceConfig
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, uint(1280), out.RPICameraWidth)
	require.Equal(t, uint(720), out.RPICameraHeight)
}

func TestV1RecorderSourceConfigPatchInvalidUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	code, body := invokeSourceConfigHandler(api, http.MethodPatch, "not-a-uuid", []byte(`{}`))
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, string(body), "invalid camera id")
}

func TestV1RecorderCameraSourceConfigPatchTenantMismatch(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: rpiCamera\n    rpiCameraCamID: 0\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	patch := []byte(`{"tenant_id": "11111111-1111-1111-1111-111111111111"}`)
	code, body := invokeSourceConfigHandler(api, http.MethodPatch, cameraID, patch)
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, string(body), "tenant_id mismatch")
}
