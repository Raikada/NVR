package snapshots

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

// testJPEG renders a solid JPEG at the given size.
func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

type harness struct {
	svc   *Service
	st    *store.Store
	root  string
	now   time.Time
	grabs atomic.Int64
}

func newHarness(t *testing.T, resolve Resolver) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: "cam-1", Name: "front", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", Enabled: true,
	}))

	h := &harness{st: st, root: t.TempDir(), now: time.Date(2026, 7, 2, 18, 0, 0, 0, time.UTC)}
	grab := func(_ context.Context, _ string) ([]byte, error) {
		h.grabs.Add(1)
		return testJPEG(t, 64, 48), nil
	}
	h.svc = New(st, nil, resolve, grab, func() string { return h.root }, nil)
	h.svc.clock = func() time.Time { return h.now }
	return h
}

// ev inserts a real event row (event_snapshots FK-references events)
// and returns its bus shape.
func (h *harness) ev(t *testing.T, id, typeID string) events.Event {
	t.Helper()
	occurred := time.Date(2026, 7, 2, 18, 0, 0, 0, time.UTC)
	require.NoError(t, h.st.Events.Insert(context.Background(), &store.Event{
		ID: id, CameraID: "cam-1", TypeID: typeID, Source: "test",
		OccurredAt: occurred, ReceivedAt: occurred, Severity: "info",
		ExpiresAt: occurred.Add(24 * time.Hour),
	}))
	return events.Event{ID: id, CameraID: "cam-1", TypeID: typeID, OccurredAt: occurred}
}

func plainResolver(info CameraInfo) Resolver {
	return func(_ context.Context, _ string) (CameraInfo, error) { return info, nil }
}

func TestHandleWritesFilesAndRows(t *testing.T) {
	h := newHarness(t, plainResolver(CameraInfo{ID: "cam-1", Name: "front"}))

	h.svc.handle(context.Background(), h.ev(t, "ev-1", "motion"))

	full := filepath.Join(h.root, "front", "2026-07-02", "ev-1-full.jpg")
	thumb := filepath.Join(h.root, "front", "2026-07-02", "ev-1-thumb.jpg")
	require.FileExists(t, full)
	require.FileExists(t, thumb)

	rowFull, err := h.st.EventSnapshots.Get(context.Background(), "ev-1", "full")
	require.NoError(t, err)
	require.Equal(t, full, rowFull.Path)
	require.Equal(t, 64, rowFull.Width)

	rowThumb, err := h.st.EventSnapshots.Get(context.Background(), "ev-1", "thumb")
	require.NoError(t, err)
	require.LessOrEqual(t, rowThumb.Width, 320)

	data, err := os.ReadFile(thumb)
	require.NoError(t, err)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, cfg.Width, rowThumb.Width)
}

func TestSkipsNonCaptureTypes(t *testing.T) {
	h := newHarness(t, plainResolver(CameraInfo{ID: "cam-1", Name: "front"}))

	h.svc.handle(context.Background(), h.ev(t, "ev-off", "camera_offline"))

	require.EqualValues(t, 0, h.grabs.Load())
	_, err := h.st.EventSnapshots.Get(context.Background(), "ev-off", "full")
	require.Error(t, err)
}

func TestFetchLadderPrefersAmcrestCGI(t *testing.T) {
	jpg := testJPEG(t, 32, 24)
	var cgiHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/cgi-bin/snapshot.cgi", r.URL.Path)
		cgiHits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpg)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)

	h := newHarness(t, plainResolver(CameraInfo{
		ID: "cam-1", Name: "front", Host: u.Host, Channel: "amcrest",
		Credentials: func(context.Context) (string, string, error) { return "a", "b", nil },
	}))
	h.svc.handle(context.Background(), h.ev(t, "ev-cgi", "motion"))

	require.EqualValues(t, 1, cgiHits.Load())
	require.EqualValues(t, 0, h.grabs.Load(), "ladder must stop at the CGI rung")
}

func TestFetchLadderOnvifURIThenFrameGrab(t *testing.T) {
	jpg := testJPEG(t, 32, 24)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/onvifsnapshot", r.URL.Path)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpg)
	}))
	t.Cleanup(srv.Close)

	// ONVIF URI works → grab untouched.
	h := newHarness(t, plainResolver(CameraInfo{
		ID: "cam-1", Name: "front", SnapshotURI: srv.URL + "/onvifsnapshot",
		Credentials: func(context.Context) (string, string, error) { return "a", "b", nil },
	}))
	h.svc.handle(context.Background(), h.ev(t, "ev-uri", "motion"))
	require.EqualValues(t, 0, h.grabs.Load())

	// Nothing camera-side → frame grab.
	h2 := newHarness(t, plainResolver(CameraInfo{ID: "cam-1", Name: "front"}))
	h2.svc.handle(context.Background(), h2.ev(t, "ev-grab", "motion"))
	require.EqualValues(t, 1, h2.grabs.Load())
}

func TestBurstReusesRecentFetch(t *testing.T) {
	h := newHarness(t, plainResolver(CameraInfo{ID: "cam-1", Name: "front"}))

	h.svc.handle(context.Background(), h.ev(t, "ev-b1", "motion"))
	h.now = h.now.Add(500 * time.Millisecond)
	h.svc.handle(context.Background(), h.ev(t, "ev-b2", "motion"))

	require.EqualValues(t, 1, h.grabs.Load(), "second event within burst window reuses the JPEG")
	_, err := h.st.EventSnapshots.Get(context.Background(), "ev-b2", "full")
	require.NoError(t, err, "reused JPEG still lands as ev-b2's snapshot")

	// Past the burst window → fresh fetch.
	h.now = h.now.Add(5 * time.Second)
	h.svc.handle(context.Background(), h.ev(t, "ev-b3", "motion"))
	require.EqualValues(t, 2, h.grabs.Load())
}

func TestFetchFailureIsNonFatal(t *testing.T) {
	h := newHarness(t, plainResolver(CameraInfo{ID: "cam-1", Name: "front"}))
	h.svc.grab = func(context.Context, string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	h.svc.handle(context.Background(), h.ev(t, "ev-fail", "motion")) // must not panic
	_, err := h.st.EventSnapshots.Get(context.Background(), "ev-fail", "full")
	require.Error(t, err)
}
