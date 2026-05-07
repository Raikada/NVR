package retention

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "ret.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestEventsSweeper_DeletesOnlyExpired(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Seed a camera so events FK passes.
	if err := s.Cameras.Insert(ctx, &store.Camera{
		ID: "cam-1", Name: "cam-1", SourceType: "rtsp", SourceURL: "rtsp://x/y",
		Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed camera: %v", err)
	}

	now := time.Now().UTC()
	expired := &store.Event{
		ID: "ev-old", CameraID: "cam-1", TypeID: "motion", Source: "test",
		OccurredAt: now.Add(-48 * time.Hour),
		ReceivedAt: now.Add(-48 * time.Hour),
		ExpiresAt:  now.Add(-time.Hour), // already expired
	}
	fresh := &store.Event{
		ID: "ev-new", CameraID: "cam-1", TypeID: "motion", Source: "test",
		OccurredAt: now,
		ReceivedAt: now,
		ExpiresAt:  now.Add(7 * 24 * time.Hour),
	}
	if err := s.Events.Insert(ctx, expired); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	if err := s.Events.Insert(ctx, fresh); err != nil {
		t.Fatalf("insert fresh: %v", err)
	}

	sw := NewEventsSweeper(s.Events, 100, time.Minute, nil)
	if err := sw.Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// fresh remains; expired is gone
	if _, err := s.Events.GetByID(ctx, "ev-new"); err != nil {
		t.Errorf("fresh event was deleted: %v", err)
	}
	if _, err := s.Events.GetByID(ctx, "ev-old"); err == nil {
		t.Errorf("expired event was NOT deleted")
	}
}

// noopSegmentLister satisfies SegmentLister; counts the call to confirm
// SegmentsSweeper visits every enabled camera.
type noopSegmentLister struct {
	calls map[string]int
}

func (n *noopSegmentLister) SweepCamera(_ context.Context, cameraID string, _ time.Time) (int, error) {
	if n.calls == nil {
		n.calls = make(map[string]int)
	}
	n.calls[cameraID]++
	return 0, nil
}

func TestSegmentsSweeper_VisitsEnabledCameras(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	enabled := &store.Camera{ID: "cam-on", Name: "on", SourceType: "rtsp",
		SourceURL: "rtsp://x/y", Enabled: true, CreatedAt: now, UpdatedAt: now}
	disabled := &store.Camera{ID: "cam-off", Name: "off", SourceType: "rtsp",
		SourceURL: "rtsp://x/z", Enabled: false, CreatedAt: now, UpdatedAt: now}
	if err := s.Cameras.Insert(ctx, enabled); err != nil {
		t.Fatalf("seed enabled: %v", err)
	}
	if err := s.Cameras.Insert(ctx, disabled); err != nil {
		t.Fatalf("seed disabled: %v", err)
	}

	lister := &noopSegmentLister{}
	sw := NewSegmentsSweeper(s.Cameras, s.RecordingPolicies, lister, time.Minute, nil)

	if err := sw.Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if lister.calls["cam-on"] != 1 {
		t.Errorf("expected 1 call for cam-on, got %d", lister.calls["cam-on"])
	}
	if lister.calls["cam-off"] != 0 {
		t.Errorf("expected 0 calls for disabled cam-off, got %d", lister.calls["cam-off"])
	}
}

func TestSegmentsSweeper_NilListerNoop(t *testing.T) {
	s := openStore(t)
	sw := NewSegmentsSweeper(s.Cameras, s.RecordingPolicies, nil, time.Minute, nil)
	if err := sw.Sweep(context.Background()); err != nil {
		t.Errorf("nil lister should noop, got %v", err)
	}
}

func TestManager_SweepAllRunsEverySweeper(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	calls := make(map[string]int)
	makeFake := func(name string) Sweeper {
		return &fakeSweeper{name: name, fn: func(_ context.Context) error {
			calls[name]++
			return nil
		}}
	}

	mgr := NewManager(nil,
		makeFake("alpha"),
		makeFake("beta"),
		NewEventsSweeper(s.Events, 100, time.Minute, nil),
	)
	if err := mgr.SweepAll(ctx); err != nil {
		t.Fatalf("sweepAll: %v", err)
	}
	if calls["alpha"] != 1 || calls["beta"] != 1 {
		t.Errorf("got calls=%v", calls)
	}
}

type fakeSweeper struct {
	name string
	fn   func(context.Context) error
}

func (f *fakeSweeper) Name() string                       { return f.name }
func (f *fakeSweeper) Interval() time.Duration            { return time.Hour }
func (f *fakeSweeper) Sweep(ctx context.Context) error    { return f.fn(ctx) }
