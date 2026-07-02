// Package api: SP4 tests — DB-backed events list, signed media
// endpoints, event-to-clip.
package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/mediasign"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
	ctxpkg "context"
)

type sp4Harness struct {
	a   *API
	srv *httptest.Server
	st  *store.Store
	evs *events.Service
	dir string
}

func newSP4Harness(t *testing.T) *sp4Harness {
	t.Helper()
	withFreshClipStore(t)
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	evs := events.NewService(st.Events, st.EventRetention, nil, events.NewBus())

	signer, err := mediasign.Open(dir)
	require.NoError(t, err)

	cnf := tempConf(t, "pathDefaults:\n"+
		"  recordPath: "+filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")+"\n"+
		"paths:\n"+
		"  all_others:\n")

	a := &API{
		Conf:          cnf,
		AuthManager:   test.NilAuthManager,
		Parent:        &testParent{},
		Store:         st,
		EventsService: evs,
		Signer:        signer,
	}
	t.Cleanup(func() {
		recordingRegistriesMu.Lock()
		delete(recordingRegistries, a)
		recordingRegistriesMu.Unlock()
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/events", a.onV1EventsList)
	r.GET("/v1/events/:id", a.onV1EventsGet)
	r.POST("/v1/events/:id/clip", a.onV1EventClipPost)
	r.GET("/v1/media/snapshots/:event_id/:kind", a.onV1MediaSnapshot)
	r.GET("/v1/media/clips/:clip_id", a.onV1MediaClip)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return &sp4Harness{a: a, srv: srv, st: st, evs: evs, dir: dir}
}

func (h *sp4Harness) insertCamera(t *testing.T, id, name string) {
	t.Helper()
	require.NoError(t, h.st.Cameras.Insert(ctxpkg.Background(), &store.Camera{
		ID: id, Name: name, SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", Enabled: true,
	}))
}

func (h *sp4Harness) insertEvent(t *testing.T, id, cameraID, typeID string, at time.Time) {
	t.Helper()
	require.NoError(t, h.evs.Insert(ctxpkg.Background(), &events.Event{
		ID: id, CameraID: cameraID, TypeID: typeID,
		Source: "test", OccurredAt: at, Severity: "info",
	}))
}

func TestEventsListServedFromService(t *testing.T) {
	h := newSP4Harness(t)
	h.insertCamera(t, "cam-a", "cama")
	h.insertCamera(t, "cam-b", "camb")
	base := time.Now().UTC().Add(-time.Hour)
	h.insertEvent(t, "ev-1", "cam-a", "motion", base)
	h.insertEvent(t, "ev-2", "cam-b", "doorbell", base.Add(time.Minute))

	res, err := http.Get(h.srv.URL + "/v1/events")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out struct {
		Items []struct {
			ID       string `json:"id"`
			CameraID string `json:"camera_id"`
			TypeID   string `json:"type_id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Len(t, out.Items, 2)
	require.Equal(t, "ev-2", out.Items[0].ID, "newest first")

	// camera filter
	res2, err := http.Get(h.srv.URL + "/v1/events?camera_id=cam-a")
	require.NoError(t, err)
	defer res2.Body.Close()
	require.NoError(t, json.NewDecoder(res2.Body).Decode(&out))
	require.Len(t, out.Items, 1)
	require.Equal(t, "ev-1", out.Items[0].ID)
}

func TestEventsListCarriesSignedSnapshotURLs(t *testing.T) {
	h := newSP4Harness(t)
	h.insertCamera(t, "cam-a", "cama")
	h.insertEvent(t, "ev-s", "cam-a", "motion", time.Now().UTC())

	jpg := testJPEGBytes(t, 64, 48)
	snapPath := filepath.Join(h.dir, "ev-s-full.jpg")
	require.NoError(t, os.WriteFile(snapPath, jpg, 0o644))
	require.NoError(t, h.st.EventSnapshots.Insert(ctxpkg.Background(), &store.EventSnapshot{
		EventID: "ev-s", Kind: "full", Width: 64, Height: 48,
		Path: snapPath, SizeBytes: int64(len(jpg)), FetchedAt: time.Now().UTC(),
	}))
	require.NoError(t, h.st.EventSnapshots.Insert(ctxpkg.Background(), &store.EventSnapshot{
		EventID: "ev-s", Kind: "thumb", Width: 32, Height: 24,
		Path: snapPath, SizeBytes: int64(len(jpg)), FetchedAt: time.Now().UTC(),
	}))

	res, err := http.Get(h.srv.URL + "/v1/events/ev-s")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var got struct {
		SnapshotURL  string `json:"snapshot_url"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.Contains(t, got.SnapshotURL, "/v1/media/snapshots/ev-s/full?")
	require.Contains(t, got.SnapshotURL, "sig=")
	require.Contains(t, got.ThumbnailURL, "/v1/media/snapshots/ev-s/thumb?")

	// The signed URL actually serves the JPEG.
	res2, err := http.Get(h.srv.URL + got.SnapshotURL)
	require.NoError(t, err)
	defer res2.Body.Close()
	require.Equal(t, http.StatusOK, res2.StatusCode)
	body, _ := os.ReadFile(snapPath)
	got2 := make([]byte, len(body))
	n, _ := res2.Body.Read(got2)
	require.Equal(t, body[:n], got2[:n])
}

func TestMediaSnapshotRejectsBadSignature(t *testing.T) {
	h := newSP4Harness(t)
	h.insertCamera(t, "cam-a", "cama")
	h.insertEvent(t, "ev-x", "cam-a", "motion", time.Now().UTC())
	jpg := testJPEGBytes(t, 8, 8)
	p := filepath.Join(h.dir, "x.jpg")
	require.NoError(t, os.WriteFile(p, jpg, 0o644))
	require.NoError(t, h.st.EventSnapshots.Insert(ctxpkg.Background(), &store.EventSnapshot{
		EventID: "ev-x", Kind: "full", Path: p, FetchedAt: time.Now().UTC(),
	}))

	res, err := http.Get(h.srv.URL + fmt.Sprintf("/v1/media/snapshots/ev-x/full?exp=%d&sig=deadbeef", time.Now().Add(time.Hour).Unix()))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestEventClipCreate(t *testing.T) {
	h := newSP4Harness(t)
	camID := cameraIDFromPathName("cam1")
	h.insertCamera(t, camID, "cam1")

	// Segments around the event time (fixture harness from clip tests).
	writeClipSegmentFile(t, h.dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))
	writeClipSegmentFile(t, h.dir, "cam1", "2008-11-07_11-22-30-000000", fixtureMP4Bytes(t))
	// Segment filenames decode as LOCAL time in recordstore, so the
	// event instant is built in time.Local to match the fixture names.
	// The window must also start before the first (~1s real fmp4)
	// segment, as any continuous recording would cover it:
	// occurred+pre_roll(5s) puts the start at 11:21:59 local.
	occurred := time.Date(2008, 11, 7, 11, 22, 4, 0, time.Local)
	h.insertEvent(t, "ev-clip", camID, "motion", occurred)

	res, err := http.Post(h.srv.URL+"/v1/events/ev-clip/clip", "application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var got struct {
		Clip struct {
			ID       string `json:"id"`
			CameraID string `json:"camera_id"`
		} `json:"clip"`
		DownloadURL string `json:"download_url"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.NotEmpty(t, got.Clip.ID)
	require.Equal(t, camID, got.Clip.CameraID)
	require.Contains(t, got.DownloadURL, "/v1/media/clips/"+got.Clip.ID+"?")

	// Idempotent: second POST returns the same clip.
	res2, err := http.Post(h.srv.URL+"/v1/events/ev-clip/clip", "application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer res2.Body.Close()
	require.Equal(t, http.StatusOK, res2.StatusCode)
	var got2 struct {
		Clip struct {
			ID string `json:"id"`
		} `json:"clip"`
	}
	require.NoError(t, json.NewDecoder(res2.Body).Decode(&got2))
	require.Equal(t, got.Clip.ID, got2.Clip.ID)

	// The event wire object carries the clip id.
	preparerWG.Wait()
	res3, err := http.Get(h.srv.URL + "/v1/events/ev-clip")
	require.NoError(t, err)
	defer res3.Body.Close()
	var evGot struct {
		ClipID string `json:"clip_id"`
	}
	require.NoError(t, json.NewDecoder(res3.Body).Decode(&evGot))
	require.Equal(t, got.Clip.ID, evGot.ClipID)

	// The signed clip URL serves the MP4 once ready.
	res4, err := http.Get(h.srv.URL + got.DownloadURL)
	require.NoError(t, err)
	defer res4.Body.Close()
	require.Equal(t, http.StatusOK, res4.StatusCode)
	head := make([]byte, 12)
	_, _ = res4.Body.Read(head)
	require.True(t, isMP4(head), "download must be an mp4")
}

func TestEventClipCreateNoSegments(t *testing.T) {
	h := newSP4Harness(t)
	camID := cameraIDFromPathName("cam1")
	h.insertCamera(t, camID, "cam1")
	h.insertEvent(t, "ev-empty", camID, "motion", time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))

	res, err := http.Post(h.srv.URL+"/v1/events/ev-empty/clip", "application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode)
}

// testJPEGBytes mirrors the snapshots package helper (kept local to
// avoid an internal test-only dependency).
func testJPEGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	return buildTestJPEG(t, w, h)
}

func buildTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}
