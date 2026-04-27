package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// invokeCameraHandler runs a v1/cameras handler with a synthetic
// *gin.Context built from the supplied method, body, query, and id
// param. We don't go through api.Initialize()'s router because the v1
// routes are registered by the orchestrator (per Phase 2A contract).
//
//nolint:unparam
func invokeCameraHandler(
	api *API,
	handler func(*gin.Context),
	method string,
	rawQuery string,
	idParam string,
	body []byte,
) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()

	target := "/v1/cameras"
	if idParam != "" {
		target = "/v1/cameras/" + idParam
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	var bodyR io.Reader
	if body != nil {
		bodyR = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, bodyR)

	c, _ := gin.CreateTestContext(w)
	c.Request = req
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}

	handler(c)
	return w.Code, w.Body.Bytes()
}

func TestV1CamerasListEmptyConfig(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")

	api := &API{
		Conf: cnf,
	}

	code, body := invokeCameraHandler(api, api.onV1CamerasList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int           `json:"item_count"`
		PageCount int           `json:"page_count"`
		Items     []defs.Camera `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 0, resp.ItemCount)
	require.Equal(t, 0, resp.PageCount)
	require.Len(t, resp.Items, 0)
}

func TestV1CamerasListPopulatedAndPagination(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  cam_a:\n"+
		"    source: rtsp://user:pass@192.0.2.1:554/stream1\n"+
		"  cam_b:\n"+
		"    source: rtsp://192.0.2.2:554/stream2\n"+
		"  cam_c:\n"+
		"    source: publisher\n")

	api := &API{Conf: cnf}

	// Default page size returns all three.
	code, body := invokeCameraHandler(api, api.onV1CamerasList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int           `json:"item_count"`
		PageCount int           `json:"page_count"`
		Items     []defs.Camera `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 3, resp.ItemCount)
	require.Equal(t, 1, resp.PageCount)
	require.Len(t, resp.Items, 3)

	// Tenant id stamped on every item.
	for _, c := range resp.Items {
		require.Equal(t, "00000000-0000-0000-0000-000000000000", c.TenantID)
	}

	// Source URL with userinfo is redacted; credentials_ref marks presence.
	var camA defs.Camera
	for _, c := range resp.Items {
		if c.Name == "cam_a" {
			camA = c
		}
	}
	require.Equal(t, "cam_a", camA.Name)
	require.NotContains(t, camA.SourceURL, "user:pass@")
	require.NotNil(t, camA.CredentialsRef)
	require.Equal(t, "inline", *camA.CredentialsRef)

	// cam_b has no userinfo so credentials_ref stays nil.
	var camB defs.Camera
	for _, c := range resp.Items {
		if c.Name == "cam_b" {
			camB = c
		}
	}
	require.Equal(t, "cam_b", camB.Name)
	require.Nil(t, camB.CredentialsRef)

	// Pagination: items_per_page=2 → two pages.
	code, body = invokeCameraHandler(api, api.onV1CamerasList, http.MethodGet, "items_per_page=2&page=0", "", nil)
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 3, resp.ItemCount)
	require.Equal(t, 2, resp.PageCount)
	require.Len(t, resp.Items, 2)

	code, body = invokeCameraHandler(api, api.onV1CamerasList, http.MethodGet, "items_per_page=2&page=1", "", nil)
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Items, 1)
}

func TestV1CamerasGetByDeterministicUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  mycam:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n")

	api := &API{Conf: cnf}

	id := cameraIDFromPathName("mycam")
	require.NotEmpty(t, id)

	code, body := invokeCameraHandler(api, api.onV1CamerasGet, http.MethodGet, "", id, nil)
	require.Equal(t, http.StatusOK, code)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(body, &cam))
	require.Equal(t, id, cam.ID)
	require.Equal(t, "mycam", cam.Name)
	require.Equal(t, defs.CameraSourceTypeRTSP, cam.SourceType)
}

func TestV1CamerasGetInvalidUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	code, _ := invokeCameraHandler(api, api.onV1CamerasGet, http.MethodGet, "", "not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestV1CamerasGetMissingCamera(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	missing := cameraIDFromPathName("nonexistent")
	code, _ := invokeCameraHandler(api, api.onV1CamerasGet, http.MethodGet, "", missing, nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestV1CamerasPostCreatesAndIssuesUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"name":        "fresh_cam",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	require.NoError(t, err)

	code, respBody := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(respBody, &cam))
	require.NotEmpty(t, cam.ID)
	require.Equal(t, cameraIDFromPathName("fresh_cam"), cam.ID)
	require.Equal(t, "fresh_cam", cam.Name)
	require.Equal(t, "00000000-0000-0000-0000-000000000000", cam.TenantID)

	// Confirm the path was added to the live conf.
	_, ok := api.Conf.OptionalPaths["fresh_cam"]
	require.True(t, ok)
}

func TestV1CamerasPostRejectsTenantMismatch(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"name":        "blocked",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
		"tenant_id":   "11111111-1111-1111-1111-111111111111",
	})
	require.NoError(t, err)

	code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusForbidden, code)
}

func TestV1CamerasPostRequiresName(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	require.NoError(t, err)

	code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestV1CamerasPostDuplicateConflict(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  taken:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, _ := json.Marshal(map[string]any{
		"name":        "taken",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusConflict, code)
}

func TestV1CamerasPatchUpdates(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  mycam:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	id := cameraIDFromPathName("mycam")
	body, _ := json.Marshal(map[string]any{
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.99:554/newpath",
		"max_readers": 5,
	})

	code, respBody := invokeCameraHandler(api, api.onV1CamerasPatch, http.MethodPatch, "", id, body)
	require.Equal(t, http.StatusOK, code)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(respBody, &cam))
	require.Equal(t, "rtsp://192.0.2.99:554/newpath", cam.SourceURL)
	require.NotNil(t, cam.MaxReaders)
	require.Equal(t, 5, *cam.MaxReaders)
}

func TestV1CamerasPatchRuntimeIgnored(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  mycam:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	id := cameraIDFromPathName("mycam")
	body, _ := json.Marshal(map[string]any{
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.99:554/stream",
		"runtime": map[string]any{
			"online":    true,
			"available": true,
		},
	})

	code, respBody := invokeCameraHandler(api, api.onV1CamerasPatch, http.MethodPatch, "", id, body)
	require.Equal(t, http.StatusOK, code)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(respBody, &cam))
	// Runtime block on response comes from PathManager (nil here since we
	// have no PathManager); it should NOT be the value we wrote.
	require.Nil(t, cam.Runtime)
}

func TestV1CamerasPutFullReplace(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  mycam:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n"+
		"    maxReaders: 10\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	id := cameraIDFromPathName("mycam")
	body, _ := json.Marshal(map[string]any{
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.99:554/replaced",
		// max_readers omitted on purpose — PUT means "the absent fields are
		// not on the new shape."
	})

	code, respBody := invokeCameraHandler(api, api.onV1CamerasPut, http.MethodPut, "", id, body)
	require.Equal(t, http.StatusOK, code)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(respBody, &cam))
	require.Equal(t, "rtsp://192.0.2.99:554/replaced", cam.SourceURL)
}

func TestV1CamerasDeleteRemoves(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  doomed:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	id := cameraIDFromPathName("doomed")
	code, _ := invokeCameraHandler(api, api.onV1CamerasDelete, http.MethodDelete, "", id, nil)
	require.Equal(t, http.StatusOK, code)

	// Subsequent get returns 404.
	code, _ = invokeCameraHandler(api, api.onV1CamerasGet, http.MethodGet, "", id, nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestV1CamerasDeleteNotFound(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	missing := cameraIDFromPathName("never_existed")
	code, _ := invokeCameraHandler(api, api.onV1CamerasDelete, http.MethodDelete, "", missing, nil)
	require.Equal(t, http.StatusNotFound, code)
}

// TestV1CamerasGetSourceURLRedaction asserts that even when the
// underlying conf.Path source carries userinfo, the canonical
// Camera.source_url has it stripped and credentials_ref is set.
func TestV1CamerasGetSourceURLRedaction(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  withcreds:\n"+
		"    source: rtsp://admin:pa%24%24w0rd@192.0.2.1:554/stream\n")

	api := &API{Conf: cnf}

	id := cameraIDFromPathName("withcreds")
	code, body := invokeCameraHandler(api, api.onV1CamerasGet, http.MethodGet, "", id, nil)
	require.Equal(t, http.StatusOK, code)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(body, &cam))
	require.NotContains(t, cam.SourceURL, "admin")
	require.NotContains(t, cam.SourceURL, "pa$$w0rd")
	require.NotNil(t, cam.CredentialsRef)
	require.Equal(t, "inline", *cam.CredentialsRef)
}
