// Package api: Phase A B5 — PTZ + camera reboot endpoints.
package api //nolint:revive

import (
	ctxpkg "context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

// recordingPTZ captures calls instead of talking SOAP.
type recordingPTZ struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingPTZ) note(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recordingPTZ) ContinuousMove(_ ctxpkg.Context, profile string, x, y, z float64) error {
	r.note("move:" + profile)
	return nil
}
func (r *recordingPTZ) Stop(_ ctxpkg.Context, profile string) error { r.note("stop:" + profile); return nil }
func (r *recordingPTZ) GotoHome(_ ctxpkg.Context, profile string) error {
	r.note("home:" + profile)
	return nil
}
func (r *recordingPTZ) GetPresets(_ ctxpkg.Context, profile string) ([]onvif.PTZPreset, error) {
	r.note("presets:" + profile)
	return []onvif.PTZPreset{{Token: "P1", Name: "Driveway"}}, nil
}
func (r *recordingPTZ) GotoPreset(_ ctxpkg.Context, profile, preset string) error {
	r.note("goto:" + preset)
	return nil
}
func (r *recordingPTZ) SetPreset(_ ctxpkg.Context, profile, name string) (string, error) {
	r.note("set:" + name)
	return "P9", nil
}

func newPTZHarness(t *testing.T) (*httptestServerShim, *store.Store, *recordingPTZ, *recordingPTZ) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	vault, err := cameracred.Open(dir)
	require.NoError(t, err)
	svc := cameras.NewService(st, vault, cameras.NewBus(), nil)

	// A PTZ-capable camera with credentials and capabilities.
	require.NoError(t, svc.Insert(ctxpkg.Background(), &store.Camera{
		ID: "cam-ptz", Name: "ptzcam", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", OnvifXAddr: "http://192.0.2.1/onvif/device_service",
		Enabled: true,
	}, "admin", "pw"))
	require.NoError(t, st.CameraCapabilities.Upsert(ctxpkg.Background(), &store.CameraCapabilities{
		CameraID: "cam-ptz", ProfilesJSON: "[]", SelectedProfileToken: "Profile000",
		HasPTZ: true, ProbedAt: time.Now().UTC(),
	}))
	// A non-PTZ camera.
	require.NoError(t, svc.Insert(ctxpkg.Background(), &store.Camera{
		ID: "cam-fixed", Name: "fixedcam", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.2/s", Enabled: true,
	}, "admin", "pw"))
	require.NoError(t, st.CameraCapabilities.Upsert(ctxpkg.Background(), &store.CameraCapabilities{
		CameraID: "cam-fixed", ProfilesJSON: "[]", SelectedProfileToken: "Profile000",
		HasPTZ: false, ProbedAt: time.Now().UTC(),
	}))

	rec := &recordingPTZ{}
	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address:        "localhost:9997",
		ReadTimeout:    conf.Duration(10 * time.Second),
		WriteTimeout:   conf.Duration(10 * time.Second),
		Conf:           cnf,
		AuthManager:    test.NilAuthManager,
		Store:          st,
		CamerasService: svc,
		Vault:          vault,
		Parent:         &testParent{},
		NewPTZClientFn: func(xaddr, username, password string) PTZController {
			require.Equal(t, "http://192.0.2.1/onvif/device_service", xaddr)
			require.Equal(t, "admin", username)
			require.Equal(t, "pw", password)
			return rec
		},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return &httptestServerShim{hc: &http.Client{Transport: tr}}, st, rec, rec
}

func TestPTZEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _, rec, _ := newPTZHarness(t)

	res, body := srv.do(t, http.MethodPost, "/v1/cameras/cam-ptz/ptz/move", `{"pan":0.5,"tilt":-0.2,"zoom":0}`)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	res, _ = srv.do(t, http.MethodPost, "/v1/cameras/cam-ptz/ptz/stop", `{}`)
	require.Equal(t, http.StatusOK, res.StatusCode)
	res, _ = srv.do(t, http.MethodPost, "/v1/cameras/cam-ptz/ptz/home", `{}`)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, body = srv.do(t, http.MethodGet, "/v1/cameras/cam-ptz/ptz/presets", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var presets struct {
		Items []onvif.PTZPreset `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &presets))
	require.Len(t, presets.Items, 1)

	res, body = srv.do(t, http.MethodPost, "/v1/cameras/cam-ptz/ptz/presets", `{"name":"Porch"}`)
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Contains(t, string(body), "P9")

	res, _ = srv.do(t, http.MethodPost, "/v1/cameras/cam-ptz/ptz/presets/P1/goto", `{}`)
	require.Equal(t, http.StatusOK, res.StatusCode)

	rec.mu.Lock()
	joined := strings.Join(rec.calls, ",")
	rec.mu.Unlock()
	require.Contains(t, joined, "move:Profile000")
	require.Contains(t, joined, "stop:Profile000")
	require.Contains(t, joined, "home:Profile000")
	require.Contains(t, joined, "set:Porch")
	require.Contains(t, joined, "goto:P1")

	// Non-PTZ camera → 409; unknown camera → 404.
	res, _ = srv.do(t, http.MethodPost, "/v1/cameras/cam-fixed/ptz/move", `{"pan":1}`)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	res, _ = srv.do(t, http.MethodPost, "/v1/cameras/ghost/ptz/move", `{"pan":1}`)
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	// Velocity bounds enforced.
	res, _ = srv.do(t, http.MethodPost, "/v1/cameras/cam-ptz/ptz/move", `{"pan":5}`)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCameraRebootEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _, _, _ := newPTZHarness(t)

	// The harness API has no RebootCameraFn injected yet — inject via a
	// second harness would duplicate setup; instead assert the 404 and
	// no-xaddr paths here and the happy path with a fresh injection.
	res, _ := srv.do(t, http.MethodPost, "/v1/cameras/ghost/reboot", `{}`)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestCameraRebootHappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	vault, err := cameracred.Open(dir)
	require.NoError(t, err)
	svc := cameras.NewService(st, vault, cameras.NewBus(), nil)
	require.NoError(t, svc.Insert(ctxpkg.Background(), &store.Camera{
		ID: "cam-rb", Name: "rbcam", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", OnvifXAddr: "http://192.0.2.1/onvif/device_service",
		Enabled: true,
	}, "admin", "pw"))

	called := false
	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second), Conf: cnf,
		AuthManager: test.NilAuthManager, Store: st, CamerasService: svc,
		Vault: vault, Parent: &testParent{},
		RebootCameraFn: func(_ ctxpkg.Context, xaddr, u, p string) (string, error) {
			called = true
			require.Equal(t, "http://192.0.2.1/onvif/device_service", xaddr)
			require.Equal(t, "admin", u)
			require.Equal(t, "pw", p)
			return "Rebooting in 30 seconds", nil
		},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	shim := &httptestServerShim{hc: &http.Client{Transport: tr}}

	res, body := shim.do(t, http.MethodPost, "/v1/cameras/cam-rb/reboot", `{}`)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "Rebooting")
	require.True(t, called)
}
