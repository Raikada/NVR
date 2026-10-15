package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newClip(camID string, start time.Time) *Clip {
	return &Clip{
		ID:        uuid.NewString(),
		CameraID:  camID,
		StartTime: start,
		EndTime:   start.Add(30 * time.Second),
	}
}

func TestClips_StateMachine(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("clip-cam")
	mustInsertCamera(t, s, cam)

	c := newClip(cam.ID, time.Now().UTC())
	if err := s.Clips.Insert(ctx, c); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.Clips.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != "requested" {
		t.Errorf("initial state: %q", got.State)
	}
	if got.Format != "fmp4" {
		t.Errorf("default format: %q", got.Format)
	}

	if err := s.Clips.SetPreparing(ctx, c.ID); err != nil {
		t.Fatalf("set preparing: %v", err)
	}
	got, _ = s.Clips.GetByID(ctx, c.ID)
	if got.State != "preparing" {
		t.Errorf("state: %q", got.State)
	}

	if err := s.Clips.SetReady(ctx, c.ID, "/clips/foo.mp4", "abc123", 4096); err != nil {
		t.Fatalf("set ready: %v", err)
	}
	got, _ = s.Clips.GetByID(ctx, c.ID)
	if got.State != "ready" || got.OutputPath != "/clips/foo.mp4" || got.SizeBytes != 4096 || got.Checksum != "abc123" {
		t.Errorf("ready: %+v", got)
	}
	if got.ReadyAt.IsZero() {
		t.Errorf("ready_at not set")
	}
}

func TestClips_SetFailed(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("clip-fail")
	mustInsertCamera(t, s, cam)

	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)
	if err := s.Clips.SetFailed(ctx, c.ID, "extractor crashed"); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	got, _ := s.Clips.GetByID(ctx, c.ID)
	if got.State != "failed" {
		t.Errorf("state: %q", got.State)
	}
}

func TestClips_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.Clips.GetByID(context.Background(), "missing")
	if !errors.Is(err, ErrClipNotFound) {
		t.Errorf("expected ErrClipNotFound; got %v", err)
	}
}

func TestClips_ListFilters(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("clip-list")
	mustInsertCamera(t, s, cam)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		c := newClip(cam.ID, base.Add(time.Duration(i)*time.Hour))
		_ = s.Clips.Insert(ctx, c)
	}

	got, err := s.Clips.List(ctx, ListClipsFilter{CameraID: cam.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("count: %d", len(got))
	}

	got, _ = s.Clips.List(ctx, ListClipsFilter{State: "requested"})
	if len(got) != 4 {
		t.Errorf("by state: %d", len(got))
	}
}

func TestClips_DeleteExpiredReady(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("clip-exp")
	mustInsertCamera(t, s, cam)

	now := time.Now().UTC()

	expired := []string{"/clips/a.mp4", "/clips/b.mp4"}
	for _, p := range expired {
		c := newClip(cam.ID, now.Add(-time.Hour))
		c.ExpiresAt = now.Add(-time.Minute)
		_ = s.Clips.Insert(ctx, c)
		_ = s.Clips.SetPreparing(ctx, c.ID)
		_ = s.Clips.SetReady(ctx, c.ID, p, "", 100)
	}

	live := newClip(cam.ID, now)
	live.ExpiresAt = now.Add(time.Hour)
	_ = s.Clips.Insert(ctx, live)
	_ = s.Clips.SetReady(ctx, live.ID, "/clips/live.mp4", "", 100)

	notReady := newClip(cam.ID, now)
	notReady.ExpiresAt = now.Add(-time.Hour)
	_ = s.Clips.Insert(ctx, notReady)

	got, err := s.Clips.DeleteExpiredReady(ctx, 100)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	sort.Strings(got)
	want := []string{"/clips/a.mp4", "/clips/b.mp4"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("paths: got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("paths[%d]: got %q want %q", i, got[i], want[i])
		}
	}
	// Live and not-ready remain.
	if _, err := s.Clips.GetByID(ctx, live.ID); err != nil {
		t.Errorf("live deleted: %v", err)
	}
	if _, err := s.Clips.GetByID(ctx, notReady.ID); err != nil {
		t.Errorf("not-ready deleted: %v", err)
	}
}

func TestClips_DeleteIdempotent(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("clip-del")
	mustInsertCamera(t, s, cam)
	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)
	if err := s.Clips.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Clips.Delete(ctx, c.ID); !errors.Is(err, ErrClipNotFound) {
		t.Errorf("expected ErrClipNotFound; got %v", err)
	}
}

func TestClips_CascadeOnCameraDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("clip-casc")
	mustInsertCamera(t, s, cam)
	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)
	if err := s.Cameras.Delete(ctx, cam.ID); err != nil {
		t.Fatalf("delete cam: %v", err)
	}
	if _, err := s.Clips.GetByID(ctx, c.ID); !errors.Is(err, ErrClipNotFound) {
		t.Errorf("expected cascade; got %v", err)
	}
}
