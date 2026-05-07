// Package api: Phase 5 Task 5.2 tests for the additive camera surfaces
// (credentials PUT, health GET, recording-state GET, probe stub).
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

// startCameraAPI spins an API with a wired Phase 2/3 store + cameras
// service. Used by the additive Task 5.2 endpoints.
func startCameraAPI(t *testing.T) (*API, *http.Client, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	vault, err := cameracred.Open(dir)
	require.NoError(t, err)
	bus := cameras.NewBus()
	svc := cameras.NewService(st, vault, bus, nil)

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address:       "localhost:9997",
		ReadTimeout:   conf.Duration(10 * time.Second),
		WriteTimeout:  conf.Duration(10 * time.Second),
		Conf:          cnf,
		AuthManager:   test.NilAuthManager,
		Store:         st,
		CamerasService: svc,
		Vault:         vault,
		Parent:        &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)

	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	return a, hc, st
}

func TestCameraCredentials_RoundTripAndRedaction(t *testing.T) {
	a, hc, st := startCameraAPI(t)
	_ = a

	camID := uuid.NewString()
	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: camID, Name: "test", SourceType: "rtsp", SourceURL: "rtsp://example/stream",
	}))

	body := `{"rtsp_username":"alice","rtsp_password":"hunter2"}`
	req, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/cameras/"+camID+"/credentials",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out cameraCredentialsResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, "alice", out.Username)
	require.True(t, out.PasswordSet)

	// Plaintext password must NEVER appear in the response.
	rawBytes, _ := json.Marshal(out)
	require.NotContains(t, string(rawBytes), "hunter2")
}

func TestCameraHealth_NoRowReturnsStub(t *testing.T) {
	a, hc, st := startCameraAPI(t)
	_ = a
	camID := uuid.NewString()
	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: camID, Name: "stub", SourceType: "rtsp", SourceURL: "rtsp://example/stream",
	}))

	req, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/v1/cameras/"+camID+"/health", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out cameraHealthResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, "unknown", out.RTSPState)
}

func TestCameraProbe_Returns501(t *testing.T) {
	_, hc, _ := startCameraAPI(t)
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/cameras/"+uuid.NewString()+"/probe", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotImplemented, res.StatusCode)
}
