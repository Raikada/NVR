package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCameraCapabilities_UpsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("cap-cam")
	mustInsertCamera(t, s, cam)

	c := &CameraCapabilities{
		CameraID:               cam.ID,
		ProfilesJSON:           `[{"token":"p1"}]`,
		SelectedProfileToken:   "p1",
		HasAudio:               true,
		HasPTZ:                 true,
		HasMotion:              false,
		HasIO:                  false,
		HasImaging:             true,
		VendorCapabilitiesJSON: `{"vendor":"acme"}`,
		ProbedAt:               time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.CameraCapabilities.Upsert(ctx, c); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.CameraCapabilities.Get(ctx, cam.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProfilesJSON != c.ProfilesJSON ||
		got.SelectedProfileToken != "p1" ||
		!got.HasAudio || !got.HasPTZ || got.HasMotion || got.HasIO || !got.HasImaging ||
		got.VendorCapabilitiesJSON != c.VendorCapabilitiesJSON {
		t.Errorf("got %+v", got)
	}
	if !got.ProbedAt.Equal(c.ProbedAt) {
		t.Errorf("probed_at: %v", got.ProbedAt)
	}
}

func TestCameraCapabilities_UpsertReplaces(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("cap-replace")
	mustInsertCamera(t, s, cam)

	first := &CameraCapabilities{CameraID: cam.ID, ProfilesJSON: `[]`, HasAudio: true}
	_ = s.CameraCapabilities.Upsert(ctx, first)
	second := &CameraCapabilities{CameraID: cam.ID, ProfilesJSON: `[{"new":1}]`, HasAudio: false, HasPTZ: true}
	if err := s.CameraCapabilities.Upsert(ctx, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, _ := s.CameraCapabilities.Get(ctx, cam.ID)
	if got.ProfilesJSON != `[{"new":1}]` || got.HasAudio || !got.HasPTZ {
		t.Errorf("not replaced: %+v", got)
	}
}

func TestCameraCapabilities_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.CameraCapabilities.Get(context.Background(), "no-cam")
	if !errors.Is(err, ErrCameraCapabilitiesNotFound) {
		t.Errorf("expected ErrCameraCapabilitiesNotFound; got %v", err)
	}
}

func TestCameraCapabilities_DeleteCascade(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("cap-cascade")
	mustInsertCamera(t, s, cam)
	_ = s.CameraCapabilities.Upsert(ctx, &CameraCapabilities{CameraID: cam.ID, ProfilesJSON: `[]`})

	if err := s.Cameras.Delete(ctx, cam.ID); err != nil {
		t.Fatalf("delete cam: %v", err)
	}
	_, err := s.CameraCapabilities.Get(ctx, cam.ID)
	if !errors.Is(err, ErrCameraCapabilitiesNotFound) {
		t.Errorf("cascade should have removed row; got %v", err)
	}
}
