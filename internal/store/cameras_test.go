package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustInsertCamera(t *testing.T, s *Store, c *Camera) {
	t.Helper()
	if err := s.Cameras.Insert(context.Background(), c); err != nil {
		t.Fatalf("insert camera: %v", err)
	}
}

func newCamera(name string) *Camera {
	return &Camera{
		ID:         uuid.NewString(),
		Name:       name,
		SourceType: "rtsp",
		SourceURL:  "rtsp://example.invalid/stream",
		Enabled:    true,
	}
}

func TestCameras_InsertGetByID(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	c := &Camera{
		ID:                uuid.NewString(),
		Name:              "front-door",
		DisplayName:       "Front Door",
		Manufacturer:      "Acme",
		Model:             "X1",
		SerialNumber:      "SN-1",
		FirmwareVersion:   "1.0.0",
		MACAddress:        "aa:bb:cc:dd:ee:ff",
		IPAddress:         "10.0.0.1",
		Hostname:          "front.local",
		SourceType:        "rtsp",
		SourceURL:         "rtsp://example.invalid/stream",
		OnvifXAddr:        "http://10.0.0.1/onvif",
		RecordingPolicyID: "policy_default",
		Enabled:           true,
		PairedAt:          time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	mustInsertCamera(t, s, c)

	got, err := s.Cameras.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "front-door" || got.DisplayName != "Front Door" {
		t.Errorf("got %+v", got)
	}
	if got.Manufacturer != "Acme" || got.Model != "X1" || got.SerialNumber != "SN-1" {
		t.Errorf("vendor fields: %+v", got)
	}
	if got.RecordingPolicyID != "policy_default" {
		t.Errorf("policy: %q", got.RecordingPolicyID)
	}
	if got.PairedAt.IsZero() {
		t.Errorf("paired_at lost")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps zero")
	}
}

func TestCameras_InsertMinimalFields(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	c := newCamera("min")
	mustInsertCamera(t, s, c)
	got, err := s.Cameras.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Manufacturer != "" || got.Model != "" || got.RecordingPolicyID != "" {
		t.Errorf("optional fields not nullable: %+v", got)
	}
	if got.GroupID != "" {
		t.Errorf("group: %q", got.GroupID)
	}
	if got.PairedAt.IsZero() && got.LastCapabilityProbeAt.IsZero() {
		// Expected: both zero.
	}
}

func TestCameras_DuplicateNameRejected(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	if err := s.Cameras.Insert(ctx, newCamera("dup")); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.Cameras.Insert(ctx, newCamera("dup"))
	if !errors.Is(err, ErrCameraExists) {
		t.Fatalf("expected ErrCameraExists; got %v", err)
	}
}

func TestCameras_GetByName(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	c := newCamera("byname")
	mustInsertCamera(t, s, c)
	got, err := s.Cameras.GetByName(ctx, "byname")
	if err != nil {
		t.Fatalf("by name: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("id mismatch")
	}
}

func TestCameras_ListFilters(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	g := &CameraGroup{ID: uuid.NewString(), Name: "g1"}
	_ = s.CameraGroups.Insert(ctx, g)

	c1 := newCamera("c1")
	c1.GroupID = g.ID
	c1.Enabled = true
	mustInsertCamera(t, s, c1)

	c2 := newCamera("c2")
	c2.GroupID = g.ID
	c2.Enabled = false
	mustInsertCamera(t, s, c2)

	c3 := newCamera("c3")
	c3.Enabled = true
	mustInsertCamera(t, s, c3)

	all, err := s.Cameras.List(ctx, ListCamerasFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all count: %d", len(all))
	}

	got, err := s.Cameras.List(ctx, ListCamerasFilter{GroupID: g.ID})
	if err != nil {
		t.Fatalf("by group: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("group count: %d", len(got))
	}

	enabledTrue := true
	got, err = s.Cameras.List(ctx, ListCamerasFilter{Enabled: &enabledTrue})
	if err != nil {
		t.Fatalf("by enabled: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("enabled count: %d", len(got))
	}
}

func TestCameras_Update(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	c := newCamera("upd")
	mustInsertCamera(t, s, c)

	c.DisplayName = "Updated"
	c.Manufacturer = "NewVendor"
	c.SourceURL = "rtsp://other.invalid/stream"
	c.Enabled = false
	if err := s.Cameras.Update(ctx, c); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := s.Cameras.GetByID(ctx, c.ID)
	if got.DisplayName != "Updated" || got.Manufacturer != "NewVendor" {
		t.Errorf("update didn't take: %+v", got)
	}
	if got.Enabled {
		t.Errorf("enabled not flipped")
	}

	// Update missing → ErrCameraNotFound.
	bogus := newCamera("ghost")
	bogus.ID = "missing"
	if err := s.Cameras.Update(ctx, bogus); !errors.Is(err, ErrCameraNotFound) {
		t.Errorf("expected ErrCameraNotFound; got %v", err)
	}
}

func TestCameras_PerFieldSetters(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	c := newCamera("setters")
	mustInsertCamera(t, s, c)

	if err := s.Cameras.SetFirmwareVersion(ctx, c.ID, "2.5"); err != nil {
		t.Fatalf("set fw: %v", err)
	}
	got, _ := s.Cameras.GetByID(ctx, c.ID)
	if got.FirmwareVersion != "2.5" {
		t.Errorf("fw: %q", got.FirmwareVersion)
	}

	when := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Cameras.SetPaired(ctx, c.ID, when); err != nil {
		t.Fatalf("set paired: %v", err)
	}
	got, _ = s.Cameras.GetByID(ctx, c.ID)
	if !got.PairedAt.Equal(when) {
		t.Errorf("paired_at: %v", got.PairedAt)
	}

	probeAt := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	if err := s.Cameras.SetLastCapabilityProbe(ctx, c.ID, probeAt); err != nil {
		t.Fatalf("set probe: %v", err)
	}
	got, _ = s.Cameras.GetByID(ctx, c.ID)
	if !got.LastCapabilityProbeAt.Equal(probeAt) {
		t.Errorf("probe: %v", got.LastCapabilityProbeAt)
	}

	if err := s.Cameras.SetEnabled(ctx, c.ID, false); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	got, _ = s.Cameras.GetByID(ctx, c.ID)
	if got.Enabled {
		t.Errorf("enabled not toggled")
	}

	// Missing id returns ErrCameraNotFound.
	if err := s.Cameras.SetEnabled(ctx, "missing", true); !errors.Is(err, ErrCameraNotFound) {
		t.Errorf("expected ErrCameraNotFound; got %v", err)
	}
}

func TestCameras_DeleteIdempotent(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	c := newCamera("del")
	mustInsertCamera(t, s, c)

	if err := s.Cameras.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Cameras.GetByID(ctx, c.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after delete; got %v", err)
	}
	if err := s.Cameras.Delete(ctx, c.ID); !errors.Is(err, ErrCameraNotFound) {
		t.Errorf("expected ErrCameraNotFound on second delete; got %v", err)
	}
}

func TestCameras_EventChannelRoundTrip(t *testing.T) {
	s := openTestStore(t)

	c := newCamera("evch")
	c.EventChannel = "amcrest"
	mustInsertCamera(t, s, c)

	got, err := s.Cameras.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EventChannel != "amcrest" {
		t.Fatalf("event_channel after insert = %q, want amcrest", got.EventChannel)
	}

	got.EventChannel = "onvif"
	if err := s.Cameras.Update(context.Background(), got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, err := s.Cameras.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if got2.EventChannel != "onvif" {
		t.Fatalf("event_channel after update = %q, want onvif", got2.EventChannel)
	}

	// Unset stays empty (NULL → "").
	c2 := newCamera("evch2")
	mustInsertCamera(t, s, c2)
	got3, err := s.Cameras.GetByID(context.Background(), c2.ID)
	if err != nil {
		t.Fatalf("get 3: %v", err)
	}
	if got3.EventChannel != "" {
		t.Fatalf("default event_channel = %q, want empty", got3.EventChannel)
	}
}
