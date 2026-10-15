// Package api: Phase A B3 — acknowledge-all + bookmarks.
package api //nolint:revive

import (
	ctxpkg "context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

func newB3Harness(t *testing.T) (*httptestServerShim, *store.Store, *events.Service) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	evs := events.NewService(st.Events, st.EventRetention, nil, events.NewBus())

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address:       "localhost:9997",
		ReadTimeout:   conf.Duration(10 * time.Second),
		WriteTimeout:  conf.Duration(10 * time.Second),
		Conf:          cnf,
		AuthManager:   test.NilAuthManager,
		Store:         st,
		EventsService: evs,
		Parent:        &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return &httptestServerShim{hc: &http.Client{Transport: tr}}, st, evs
}

// httptestServerShim keeps call sites terse.
type httptestServerShim struct{ hc *http.Client }

func (s *httptestServerShim) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://localhost:9997"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := s.hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var buf [8192]byte
	n, _ := res.Body.Read(buf[:])
	return res, buf[:n]
}

func seedCameraAndEvents(t *testing.T, st *store.Store, evs *events.Service, camID string, n int) {
	t.Helper()
	require.NoError(t, st.Cameras.Insert(ctxpkg.Background(), &store.Camera{
		ID: camID, Name: "cam_" + strings.ReplaceAll(camID, "-", "_"), SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", Enabled: true,
	}))
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < n; i++ {
		require.NoError(t, evs.Insert(ctxpkg.Background(), &events.Event{
			CameraID: camID, TypeID: "motion", Source: "test",
			OccurredAt: base.Add(time.Duration(i) * time.Minute), Severity: "info",
		}))
	}
}

func TestAcknowledgeAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, st, evs := newB3Harness(t)
	seedCameraAndEvents(t, st, evs, "cam-a1", 3)
	seedCameraAndEvents(t, st, evs, "cam-b1", 2)

	// Scoped to one camera.
	res, body := srv.do(t, http.MethodPost, "/v1/events/acknowledge-all", `{"camera_id":"cam-a1"}`)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	var out struct {
		Acknowledged int `json:"acknowledged"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, 3, out.Acknowledged)

	// Unscoped mops up the rest; idempotent second run acks zero.
	res2, body2 := srv.do(t, http.MethodPost, "/v1/events/acknowledge-all", `{}`)
	require.Equal(t, http.StatusOK, res2.StatusCode)
	require.NoError(t, json.Unmarshal(body2, &out))
	require.Equal(t, 2, out.Acknowledged)

	res3, body3 := srv.do(t, http.MethodPost, "/v1/events/acknowledge-all", `{}`)
	require.Equal(t, http.StatusOK, res3.StatusCode)
	require.NoError(t, json.Unmarshal(body3, &out))
	require.Equal(t, 0, out.Acknowledged)
}

func TestBookmarkCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, st, evs := newB3Harness(t)
	seedCameraAndEvents(t, st, evs, "cam-bm1", 0)

	at := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	res, body := srv.do(t, http.MethodPost, "/v1/cameras/cam-bm1/bookmarks",
		`{"time":"`+at+`","note":"delivery arrived"}`)
	require.Equal(t, http.StatusCreated, res.StatusCode, "%s", body)
	var out struct {
		ID         string    `json:"id"`
		TypeID     string    `json:"type_id"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, "bookmark", out.TypeID)
	require.NotEmpty(t, out.ID)

	// It rides the normal events pipeline: listable, note in payload.
	row, err := st.Events.GetByID(ctxpkg.Background(), out.ID)
	require.NoError(t, err)
	require.Equal(t, "bookmark", row.TypeID)
	require.Contains(t, row.PayloadJSON, "delivery arrived")

	// Unknown camera → 404.
	res2, _ := srv.do(t, http.MethodPost, "/v1/cameras/ghost/bookmarks", `{"note":"x"}`)
	require.Equal(t, http.StatusNotFound, res2.StatusCode)
}
