package camerahealth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

type fakePaths struct {
	snaps []PathSnapshot
	err   error
}

func (f *fakePaths) ListPaths(_ context.Context) ([]PathSnapshot, error) {
	return f.snaps, f.err
}

type fakeSink struct{ inserted []*events.Event }

func (f *fakeSink) Insert(_ context.Context, ev *events.Event) error {
	f.inserted = append(f.inserted, ev)
	return nil
}

// harness: real store (schema + repos), one enabled camera, fake paths
// + sink + clock.
type harness struct {
	c     *Collector
	st    *store.Store
	paths *fakePaths
	sink  *fakeSink
	now   time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: "cam-1", Name: "front", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", Enabled: true,
	}))

	h := &harness{
		st:    st,
		paths: &fakePaths{},
		sink:  &fakeSink{},
		now:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
	}
	h.c = New(st, h.paths, h.sink, nil)
	h.c.clock = func() time.Time { return h.now }
	return h
}

func (h *harness) online() {
	t := h.now
	h.paths.snaps = []PathSnapshot{{Name: "front", Online: true, LastFrameAt: t}}
}

func (h *harness) offline() {
	h.paths.snaps = []PathSnapshot{{Name: "front", Online: false}}
}

func (h *harness) poll(t *testing.T, times int, step time.Duration) {
	t.Helper()
	for i := 0; i < times; i++ {
		h.c.PollOnce(context.Background())
		h.now = h.now.Add(step)
	}
}

func (h *harness) state(t *testing.T) string {
	t.Helper()
	row, err := h.st.CameraHealth.Get(context.Background(), "cam-1")
	require.NoError(t, err)
	return row.RTSPState
}

func TestConnectedStateAndKeyframe(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.poll(t, 1, 5*time.Second)

	row, err := h.st.CameraHealth.Get(context.Background(), "cam-1")
	require.NoError(t, err)
	require.Equal(t, "connected", row.RTSPState)
	require.False(t, row.LastKeyframeAt.IsZero())
	require.Empty(t, h.sink.inserted, "no event on plain connect")
}

func TestOfflineEventAfterThresholdAndDebounce(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.poll(t, 1, 5*time.Second)

	h.offline()
	// 4 failed polls (20s): below threshold — reconnecting, no event.
	h.poll(t, 4, 5*time.Second)
	require.Equal(t, "reconnecting", h.state(t))
	require.Empty(t, h.sink.inserted)

	// 5th+ failed polls, and 30s debounce elapsed → failed + one event.
	h.poll(t, 4, 5*time.Second)
	require.Equal(t, "failed", h.state(t))
	require.Len(t, h.sink.inserted, 1)
	ev := h.sink.inserted[0]
	require.Equal(t, "camera_offline", ev.TypeID)
	require.Equal(t, "cam-1", ev.CameraID)
	require.Equal(t, "warning", ev.Severity)
	require.Equal(t, "health", ev.Source)

	// Staying failed emits no duplicate events.
	h.poll(t, 3, 5*time.Second)
	require.Len(t, h.sink.inserted, 1)
}

func TestFlapWithinDebounceEmitsNothing(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.poll(t, 1, 5*time.Second)

	// Down for 15s (3 polls), back up — a credential-rotation restart.
	h.offline()
	h.poll(t, 3, 5*time.Second)
	h.online()
	h.poll(t, 1, 5*time.Second)

	require.Equal(t, "connected", h.state(t))
	require.Empty(t, h.sink.inserted)
}

func TestRecoveryEmitsOnlineExactlyOnce(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.poll(t, 1, 5*time.Second)
	h.offline()
	h.poll(t, 8, 5*time.Second) // failed + offline event
	require.Len(t, h.sink.inserted, 1)

	h.online()
	h.poll(t, 3, 5*time.Second)
	require.Equal(t, "connected", h.state(t))
	require.Len(t, h.sink.inserted, 2)
	require.Equal(t, "camera_online", h.sink.inserted[1].TypeID)
	require.Equal(t, "info", h.sink.inserted[1].Severity)
}

func TestDisabledCameraIsIdle(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, st_setEnabled(h.st, "cam-1", false))
	h.poll(t, 1, 5*time.Second)
	require.Equal(t, "idle", h.state(t))
}

func TestPathListErrorIsNeutral(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.poll(t, 1, 5*time.Second)
	require.Equal(t, "connected", h.state(t))

	h.paths.err = context.DeadlineExceeded
	h.poll(t, 10, 5*time.Second)
	// A broken poll source is not camera failure: state holds, no events.
	require.Equal(t, "connected", h.state(t))
	require.Empty(t, h.sink.inserted)
}

func TestTouchStampsLastEventAt(t *testing.T) {
	h := newHarness(t)
	h.online()
	h.poll(t, 1, 5*time.Second)

	stamp := h.now.Add(-time.Second)
	h.c.Touch("cam-1", stamp)
	h.poll(t, 1, 5*time.Second)

	row, err := h.st.CameraHealth.Get(context.Background(), "cam-1")
	require.NoError(t, err)
	require.WithinDuration(t, stamp, row.LastEventAt, time.Second)
}

// st_setEnabled flips a camera's enabled flag through the repo.
func st_setEnabled(st *store.Store, id string, enabled bool) error {
	cam, err := st.Cameras.GetByID(context.Background(), id)
	if err != nil {
		return err
	}
	cam.Enabled = enabled
	return st.Cameras.Update(context.Background(), cam)
}
