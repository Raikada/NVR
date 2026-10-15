package events

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
	s, err := store.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Seed a camera so events.camera_id FK is satisfiable.
	if err := s.Cameras.Insert(context.Background(), &store.Camera{
		ID: "cam-1", Name: "cam-1", SourceType: "rtsp",
		SourceURL: "rtsp://x/y", Enabled: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	return s
}

func TestService_InsertMaterializesExpiresAt(t *testing.T) {
	s := openStore(t)
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)

	now := time.Now().UTC()
	ev := &Event{
		CameraID: "cam-1", TypeID: "motion", Source: "onvif_pullpoint",
		OccurredAt: now, ReceivedAt: now,
	}
	if err := svc.Insert(context.Background(), ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ev.ExpiresAt.IsZero() {
		t.Fatal("expected ExpiresAt to be materialized")
	}
	// motion has 90d retention per the seeded event_retention.
	want := now.Add(90 * 24 * time.Hour)
	if got, max := ev.ExpiresAt, want.Add(time.Second); got.Before(want.Add(-time.Second)) || got.After(max) {
		t.Errorf("ExpiresAt %v not within 1s of expected %v", got, want)
	}
}

func TestService_InsertFallsBackToDefaultRetention(t *testing.T) {
	s := openStore(t)
	// seed a custom event type without retention -> falls back to __default__ (7d)
	if err := s.EventTypes.Insert(context.Background(), &store.EventType{
		ID: "custom_type", DisplayName: "Custom", Vendor: "custom",
	}); err != nil {
		t.Fatalf("seed event type: %v", err)
	}
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)

	now := time.Now().UTC()
	ev := &Event{
		CameraID: "cam-1", TypeID: "custom_type", Source: "internal",
		OccurredAt: now, ReceivedAt: now,
	}
	if err := svc.Insert(context.Background(), ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	want := now.Add(7 * 24 * time.Hour)
	if got, max := ev.ExpiresAt, want.Add(time.Second); got.Before(want.Add(-time.Second)) || got.After(max) {
		t.Errorf("ExpiresAt %v not within 1s of default 7d %v", got, want)
	}
}

func TestService_BusReceivesPublished(t *testing.T) {
	s := openStore(t)
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)

	ch, unsub := svc.Subscribe()
	defer unsub()

	go func() {
		_ = svc.Insert(context.Background(), &Event{
			CameraID: "cam-1", TypeID: "motion", Source: "onvif_pullpoint",
		})
	}()
	select {
	case ev := <-ch:
		if ev.CameraID != "cam-1" {
			t.Errorf("got camera %q", ev.CameraID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for bus publish")
	}
}

func TestService_GetAndList(t *testing.T) {
	s := openStore(t)
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)

	ev := &Event{
		ID: "ev-test-1", CameraID: "cam-1", TypeID: "motion",
		Source: "onvif_pullpoint",
	}
	if err := svc.Insert(context.Background(), ev); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := svc.Get(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != ev.ID || got.CameraID != "cam-1" {
		t.Errorf("get returned %+v", got)
	}

	page, _, err := svc.List(context.Background(), ListFilter{CameraIDs: []string{"cam-1"}, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page) == 0 {
		t.Fatal("expected at least one event from list")
	}
}

func TestService_AcknowledgeRequiresIDAndUser(t *testing.T) {
	s := openStore(t)
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)
	if err := svc.Acknowledge(context.Background(), "", "u1"); err == nil {
		t.Error("expected error on empty id")
	}
	if err := svc.Acknowledge(context.Background(), "x", ""); err == nil {
		t.Error("expected error on empty userID")
	}
}
