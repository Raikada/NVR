// Package api: Phase A B6 — camera stats + per-user UI prefs.
package api //nolint:revive

import (
	ctxpkg "context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

// fakePathManager serves one path's runtime counters.
type fakePathManager struct{ path *defs.APIPath }

func (f *fakePathManager) APIPathsList() (*defs.APIPathList, error) {
	return &defs.APIPathList{Items: []defs.APIPath{*f.path}}, nil
}

func (f *fakePathManager) APIPathsGet(name string) (*defs.APIPath, error) {
	if f.path != nil && f.path.Name == name {
		return f.path, nil
	}
	return nil, fmt.Errorf("path not found")
}

func TestCameraStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Cameras.Insert(ctxpkg.Background(), &store.Camera{
		ID: "cam-st", Name: "statcam", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", Enabled: true,
	}))

	onlineAt := time.Now().UTC().Add(-90 * time.Second)
	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second), Conf: cnf,
		AuthManager: test.NilAuthManager, Store: st, Parent: &testParent{},
		PathManager: &fakePathManager{path: &defs.APIPath{
			Name: "statcam", Online: true, OnlineTime: &onlineAt,
			InboundBytes: 123456, OutboundBytes: 7890,
			InboundFramesInError: 2,
			Tracks2: []defs.APIPathTrack{{Codec: defs.APIPathTrackCodec("H264")}, {Codec: defs.APIPathTrackCodec("MPEG-4 Audio")}},
		}},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	shim := &httptestServerShim{hc: &http.Client{Transport: tr}}

	res, body := shim.do(t, http.MethodGet, "/v1/cameras/cam-st/stats", "")
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	var out struct {
		Online        bool      `json:"online"`
		OnlineSince   time.Time `json:"online_since"`
		BytesReceived uint64    `json:"bytes_received"`
		BytesSent     uint64    `json:"bytes_sent"`
		FramesInError uint64    `json:"frames_in_error"`
		Tracks        []string  `json:"tracks"`
		Timestamp     time.Time `json:"ts"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.True(t, out.Online)
	require.EqualValues(t, 123456, out.BytesReceived)
	require.Equal(t, []string{"H264", "MPEG-4 Audio"}, out.Tracks)
	require.WithinDuration(t, time.Now(), out.Timestamp, 5*time.Second)

	res, _ = shim.do(t, http.MethodGet, "/v1/cameras/ghost/stats", "")
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestUserPrefsRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	la, dir := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(ctxpkg.Background())
	require.NoError(t, err)
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second), Conf: cnf,
		AuthManager: test.NilAuthManager, LocalAuth: la, Store: st,
		Parent: &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	token := loginForToken(t, hc, pw)

	put := func(body string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodPut, "http://localhost:9997/v1/me/prefs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := hc.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var buf [4096]byte
		n, _ := res.Body.Read(buf[:])
		return res, buf[:n]
	}

	res, body := put(`{"default_grid":"2x2","tour":{"cameras":["a","b"],"dwell":8}}`)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

	// Merge semantics: a later PUT of one key keeps the others.
	res, _ = put(`{"default_grid":"3x3"}`)
	require.Equal(t, http.StatusOK, res.StatusCode)

	req, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/v1/me/prefs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	gres, err := hc.Do(req)
	require.NoError(t, err)
	defer gres.Body.Close()
	require.Equal(t, http.StatusOK, gres.StatusCode)
	var prefs map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(gres.Body).Decode(&prefs))
	require.JSONEq(t, `"3x3"`, string(prefs["default_grid"]))
	require.Contains(t, string(prefs["tour"]), "dwell")

	// No token → 401.
	nreq, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/v1/me/prefs", nil)
	nres, err := hc.Do(nreq)
	require.NoError(t, err)
	nres.Body.Close()
	require.Equal(t, http.StatusUnauthorized, nres.StatusCode)
}
