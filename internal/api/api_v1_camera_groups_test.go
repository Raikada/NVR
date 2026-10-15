// Package api: Phase 5 Task 5.3 tests for /v1/camera-groups CRUD +
// the 409-on-cameras-still-attached delete invariant.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/store"
)

func TestCameraGroups_RoundTrip(t *testing.T) {
	a, hc, _ := startCameraAPI(t)
	_ = a

	body := `{"name":"lobby","display_order":1}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/camera-groups", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created cameraGroupResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, "lobby", created.Name)

	// List
	req, _ = http.NewRequest(http.MethodGet, "http://localhost:9997/v1/camera-groups", nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var listed struct {
		Items []cameraGroupResponse `json:"items"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&listed))
	require.Len(t, listed.Items, 1)

	// Update
	patchBody := `{"name":"lobby-renamed"}`
	req, _ = http.NewRequest(http.MethodPatch,
		"http://localhost:9997/v1/camera-groups/"+created.ID, strings.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Delete
	req, _ = http.NewRequest(http.MethodDelete,
		"http://localhost:9997/v1/camera-groups/"+created.ID, nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestCameraGroups_DeleteWithCamerasReturns409(t *testing.T) {
	_, hc, st := startCameraAPI(t)

	groupID := uuid.NewString()
	require.NoError(t, st.CameraGroups.Insert(context.Background(), &store.CameraGroup{
		ID: groupID, Name: "guarded",
	}))
	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: uuid.NewString(), Name: "child",
		GroupID: groupID, SourceType: "rtsp", SourceURL: "rtsp://x/y",
	}))

	req, _ := http.NewRequest(http.MethodDelete,
		"http://localhost:9997/v1/camera-groups/"+groupID, nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode)
}
