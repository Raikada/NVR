package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func invokeCameraDefaultsHandler(api *API, method string, body []byte) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, "/v1/recorder/camera-defaults", nil)
	} else {
		req = httptest.NewRequest(method, "/v1/recorder/camera-defaults", bytes.NewReader(body))
	}
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	switch method {
	case http.MethodGet:
		api.onV1RecorderCameraDefaultsGet(c)
	case http.MethodPatch:
		api.onV1RecorderCameraDefaultsPatch(c)
	}
	return w.Code, w.Body.Bytes()
}

func TestV1RecorderCameraDefaultsGetStripsPolicyFields(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	code, body := invokeCameraDefaultsHandler(api, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))

	// Source-level non-policy defaults are still present.
	require.Equal(t, "publisher", out["source"])

	// Recording-policy fields must be absent.
	for _, k := range []string{
		"record",
		"recordPath",
		"recordFormat",
		"recordPartDuration",
		"recordMaxPartSize",
		"recordSegmentDuration",
		"recordDeleteAfter",
	} {
		_, present := out[k]
		require.Falsef(t, present, "policy field %q must be stripped from camera-defaults", k)
	}
}

func TestV1RecorderCameraDefaultsPatchSucceedsForNonPolicyFields(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	code, _ := invokeCameraDefaultsHandler(api, http.MethodPatch,
		[]byte(`{"sourceOnDemand": true, "sourceOnDemandStartTimeout": "20s"}`))
	require.Equal(t, http.StatusOK, code)

	code, body := invokeCameraDefaultsHandler(api, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, true, out["sourceOnDemand"])
	require.Equal(t, "20s", out["sourceOnDemandStartTimeout"])
}

func TestV1RecorderCameraDefaultsPatchRejectsPolicyFields(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Conf:   cnf,
		Parent: &testParent{},
	}

	code, body := invokeCameraDefaultsHandler(api, http.MethodPatch,
		[]byte(`{"recordFormat": "mpegts", "sourceOnDemand": true}`))
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, string(body), "/v1/recording-policies")
	require.Contains(t, string(body), "recordFormat")

	// Verify the patch did not partially apply (sourceOnDemand still default false).
	code, body = invokeCameraDefaultsHandler(api, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, false, out["sourceOnDemand"])
}
