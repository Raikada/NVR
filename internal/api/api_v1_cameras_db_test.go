// Package api: regression tests for the camera CRUD ↔ store split
// found by the 2026-07-02 real-camera smoke test. API-created cameras
// must land in the cameras table (else the credential vault FK-fails),
// and API-deleted cameras must cascade out of it.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// POST /v1/cameras must insert the canonical store row so the
// foundation surfaces (credentials vault, health, PathBridge) can see
// the camera. Smoke finding F1.
func TestCamerasPostInsertsStoreRow(t *testing.T) {
	a, hc, st := startCameraAPI(t)
	_ = a

	body := `{"name":"api_cam","source_type":"rtsp","source_url":"rtsp://192.0.2.9:554/stream","enabled":true}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/cameras", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.NotEmpty(t, created.ID)

	row, err := st.Cameras.GetByID(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "api_cam", row.Name)
	require.Equal(t, "rtsp://192.0.2.9:554/stream", row.SourceURL)
	require.True(t, row.Enabled)

	// The regression that surfaced F1: credentials PUT must work on an
	// API-created camera without hand-editing SQLite.
	credBody := `{"rtsp_username":"alice","rtsp_password":"hunter2"}`
	credReq, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/cameras/"+created.ID+"/credentials",
		strings.NewReader(credBody))
	credReq.Header.Set("Content-Type", "application/json")
	credRes, err := hc.Do(credReq)
	require.NoError(t, err)
	defer credRes.Body.Close()
	require.Equal(t, http.StatusOK, credRes.StatusCode)

	creds, err := st.CameraCredentials.Get(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "alice", creds.Username)
}

// DELETE /v1/cameras/:id must remove the store row (cascading
// credentials, health, events per schema). Smoke finding F3.
func TestCamerasDeleteRemovesStoreRow(t *testing.T) {
	a, hc, st := startCameraAPI(t)
	_ = a

	body := `{"name":"doomed_cam","source_type":"rtsp","source_url":"rtsp://192.0.2.9:554/stream"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/cameras", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))

	credBody := `{"rtsp_username":"alice","rtsp_password":"hunter2"}`
	credReq, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/cameras/"+created.ID+"/credentials",
		strings.NewReader(credBody))
	credReq.Header.Set("Content-Type", "application/json")
	credRes, err := hc.Do(credReq)
	require.NoError(t, err)
	credRes.Body.Close()
	require.Equal(t, http.StatusOK, credRes.StatusCode)

	delReq, _ := http.NewRequest(http.MethodDelete,
		"http://localhost:9997/v1/cameras/"+created.ID, nil)
	delRes, err := hc.Do(delReq)
	require.NoError(t, err)
	delRes.Body.Close()
	require.Equal(t, http.StatusOK, delRes.StatusCode)

	_, err = st.Cameras.GetByID(context.Background(), created.ID)
	require.Error(t, err, "camera row must be gone after DELETE")
	_, err = st.CameraCredentials.Get(context.Background(), created.ID)
	require.Error(t, err, "credentials row must cascade away after DELETE")
}

// PATCH /v1/cameras/:id must keep the store row's source URL in step,
// else the next credential rotation re-materializes a stale URL.
func TestCamerasPatchUpdatesStoreRow(t *testing.T) {
	a, hc, st := startCameraAPI(t)
	_ = a

	body := `{"name":"patch_cam","source_type":"rtsp","source_url":"rtsp://192.0.2.9:554/old"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/cameras", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))

	patchBody := `{"source_url":"rtsp://192.0.2.9:554/new"}`
	patchReq, _ := http.NewRequest(http.MethodPatch,
		"http://localhost:9997/v1/cameras/"+created.ID, strings.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRes, err := hc.Do(patchReq)
	require.NoError(t, err)
	patchRes.Body.Close()
	require.Equal(t, http.StatusOK, patchRes.StatusCode)

	row, err := st.Cameras.GetByID(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "rtsp://192.0.2.9:554/new", row.SourceURL)
}
