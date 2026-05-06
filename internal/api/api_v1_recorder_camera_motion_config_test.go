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

	"github.com/bluenviron/mediamtx/internal/motion"
)

func invokeMotionConfigHandler(api *API, method, idParam, suffix string, body []byte) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	target := "/v1/recorder/cameras/" + idParam + "/motion-config" + suffix
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	switch {
	case method == http.MethodGet && suffix == "":
		api.onV1RecorderCameraMotionConfigGet(c)
	case method == http.MethodPatch && suffix == "":
		api.onV1RecorderCameraMotionConfigPatch(c)
	case method == http.MethodPost && suffix == "/test":
		api.onV1RecorderCameraMotionConfigTest(c)
	}
	return w.Code, w.Body.Bytes()
}

func TestV1MotionConfigGetReturnsDefaultWhenAbsent(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	code, body := invokeMotionConfigHandler(api, http.MethodGet, cameraID, "", nil)
	require.Equal(t, http.StatusOK, code)

	var out motion.MotionConfig
	require.NoError(t, json.Unmarshal(body, &out))
	// default is "disabled, source onvif, sensitivity 50, cooldown 5000ms".
	require.False(t, out.Enabled)
	require.Equal(t, motion.MotionConfigSourceONVIF, out.Source)
	require.Equal(t, 50, out.Sensitivity)
	require.Equal(t, 5000, out.CooldownMS)
}

func TestV1MotionConfigGetCameraNotFound(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	code, _ := invokeMotionConfigHandler(api, http.MethodGet, uuid.New().String(), "", nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestV1MotionConfigPatchPersistsAndRoundtrips(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	patch := []byte(`{"enabled": true, "sensitivity": 75, "cooldown_ms": 2000}`)
	code, _ := invokeMotionConfigHandler(api, http.MethodPatch, cameraID, "", patch)
	require.Equal(t, http.StatusOK, code)

	code, body := invokeMotionConfigHandler(api, http.MethodGet, cameraID, "", nil)
	require.Equal(t, http.StatusOK, code)

	var out motion.MotionConfig
	require.NoError(t, json.Unmarshal(body, &out))
	require.True(t, out.Enabled)
	require.Equal(t, 75, out.Sensitivity)
	require.Equal(t, 2000, out.CooldownMS)
	require.Equal(t, motion.MotionConfigSourceONVIF, out.Source)
}

func TestV1MotionConfigPatchRejectsInvalid(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	cases := []struct {
		name  string
		patch string
	}{
		{"bad-source", `{"source": "neural-magic"}`},
		{"bad-sensitivity", `{"sensitivity": 150}`},
		{"bad-cooldown", `{"cooldown_ms": -1}`},
		{
			"bad-roi",
			`{"roi": {"x": 0.5, "y": 0.5, "w": 0.8, "h": 0.8}}`,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			code, _ := invokeMotionConfigHandler(api, http.MethodPatch, cameraID, "", []byte(c.patch))
			require.Equal(t, http.StatusBadRequest, code)
		})
	}
}

func TestV1MotionConfigPatchUnsetROIClears(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	// First, set an ROI.
	patch1 := []byte(`{"enabled": true, "roi": {"x": 0.1, "y": 0.1, "w": 0.5, "h": 0.5}}`)
	code, _ := invokeMotionConfigHandler(api, http.MethodPatch, cameraID, "", patch1)
	require.Equal(t, http.StatusOK, code)

	// Verify ROI present.
	code, body := invokeMotionConfigHandler(api, http.MethodGet, cameraID, "", nil)
	require.Equal(t, http.StatusOK, code)
	var out motion.MotionConfig
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotNil(t, out.ROI)

	// Now unset it.
	patch2 := []byte(`{"unset_roi": true}`)
	code, _ = invokeMotionConfigHandler(api, http.MethodPatch, cameraID, "", patch2)
	require.Equal(t, http.StatusOK, code)

	code, body = invokeMotionConfigHandler(api, http.MethodGet, cameraID, "", nil)
	require.Equal(t, http.StatusOK, code)
	var out2 motion.MotionConfig
	require.NoError(t, json.Unmarshal(body, &out2))
	require.Nil(t, out2.ROI)
}

func TestV1MotionConfigTestEmitsSyntheticEvent(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")

	store := DefaultEventStore()
	store.Clear()

	code, _ := invokeMotionConfigHandler(api, http.MethodPost, cameraID, "/test", nil)
	require.Equal(t, http.StatusOK, code)

	events := store.Snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "camera.motion_detected", events[0].Kind)
	require.Equal(t, cameraID, events[0].SubjectID)
	require.Equal(t, "true", events[0].Attributes["synthetic"])
	require.Equal(t, "tns1:Test/Synthetic", events[0].Attributes["onvif_topic"])
}

func TestV1MotionConfigTestCustomTopic(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	store := DefaultEventStore()
	store.Clear()

	body := []byte(`{"topic": "tns1:RuleEngine/CellMotionDetector/Motion"}`)
	code, _ := invokeMotionConfigHandler(api, http.MethodPost, cameraID, "/test", body)
	require.Equal(t, http.StatusOK, code)

	events := store.Snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "tns1:RuleEngine/CellMotionDetector/Motion",
		events[0].Attributes["onvif_topic"])
}
