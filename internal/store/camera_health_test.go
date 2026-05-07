package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCameraHealth_UpsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("health-cam")
	mustInsertCamera(t, s, cam)

	h := &CameraHealth{
		CameraID:            cam.ID,
		RTSPState:           "connected",
		LastKeyframeAt:      time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LastEventAt:         time.Date(2026, 5, 1, 0, 1, 0, 0, time.UTC),
		LastSeenAt:          time.Date(2026, 5, 1, 0, 2, 0, 0, time.UTC),
		ConsecutiveFailures: 0,
	}
	if err := s.CameraHealth.Upsert(ctx, h); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.CameraHealth.Get(ctx, cam.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RTSPState != "connected" {
		t.Errorf("state: %q", got.RTSPState)
	}
	if !got.LastKeyframeAt.Equal(h.LastKeyframeAt) || !got.LastEventAt.Equal(h.LastEventAt) {
		t.Errorf("times mismatch: %+v", got)
	}
}

func TestCameraHealth_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.CameraHealth.Get(context.Background(), "ghost")
	if !errors.Is(err, ErrCameraHealthNotFound) {
		t.Errorf("expected ErrCameraHealthNotFound; got %v", err)
	}
}

func TestCameraHealth_IncrementFailure(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("inc-cam")
	mustInsertCamera(t, s, cam)

	for i := 0; i < 3; i++ {
		if err := s.CameraHealth.IncrementFailure(ctx, cam.ID, "boom"); err != nil {
			t.Fatalf("inc %d: %v", i, err)
		}
	}
	got, _ := s.CameraHealth.Get(ctx, cam.ID)
	if got.ConsecutiveFailures != 3 {
		t.Errorf("failures: %d", got.ConsecutiveFailures)
	}
	if got.LastError != "boom" {
		t.Errorf("last_error: %q", got.LastError)
	}
}

func TestCameraHealth_ResetFailures(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("reset-cam")
	mustInsertCamera(t, s, cam)

	_ = s.CameraHealth.IncrementFailure(ctx, cam.ID, "bad")
	_ = s.CameraHealth.IncrementFailure(ctx, cam.ID, "worse")

	if err := s.CameraHealth.ResetFailures(ctx, cam.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, _ := s.CameraHealth.Get(ctx, cam.ID)
	if got.ConsecutiveFailures != 0 || got.LastError != "" {
		t.Errorf("not reset: %+v", got)
	}

	// Reset on missing returns ErrCameraHealthNotFound.
	if err := s.CameraHealth.ResetFailures(ctx, "ghost"); !errors.Is(err, ErrCameraHealthNotFound) {
		t.Errorf("expected ErrCameraHealthNotFound; got %v", err)
	}
}

func TestCameraHealth_TouchKeyframe(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("kf-cam")
	mustInsertCamera(t, s, cam)

	when := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	if err := s.CameraHealth.TouchKeyframe(ctx, cam.ID, when); err != nil {
		t.Fatalf("touch keyframe: %v", err)
	}
	got, _ := s.CameraHealth.Get(ctx, cam.ID)
	if !got.LastKeyframeAt.Equal(when) {
		t.Errorf("keyframe: %v", got.LastKeyframeAt)
	}
	if !got.LastSeenAt.Equal(when) {
		t.Errorf("last_seen: %v", got.LastSeenAt)
	}
}

func TestCameraHealth_TouchEvent(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("evt-cam")
	mustInsertCamera(t, s, cam)

	when := time.Date(2026, 5, 7, 13, 0, 0, 0, time.UTC)
	if err := s.CameraHealth.TouchEvent(ctx, cam.ID, when); err != nil {
		t.Fatalf("touch event: %v", err)
	}
	got, _ := s.CameraHealth.Get(ctx, cam.ID)
	if !got.LastEventAt.Equal(when) {
		t.Errorf("event: %v", got.LastEventAt)
	}
}

func TestCameraHealth_DeleteCascade(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("hcasc")
	mustInsertCamera(t, s, cam)
	_ = s.CameraHealth.Upsert(ctx, &CameraHealth{CameraID: cam.ID, RTSPState: "idle"})

	if err := s.Cameras.Delete(ctx, cam.ID); err != nil {
		t.Fatalf("delete cam: %v", err)
	}
	_, err := s.CameraHealth.Get(ctx, cam.ID)
	if !errors.Is(err, ErrCameraHealthNotFound) {
		t.Errorf("expected cascade; got %v", err)
	}
}
