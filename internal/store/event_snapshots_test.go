package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEventSnapshots_InsertAndList(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("snap-cam")
	mustInsertCamera(t, s, cam)
	e := newEvent(cam.ID, "motion", time.Now().UTC())
	mustInsertEvent(t, s, e)

	full := &EventSnapshot{
		EventID: e.ID, Kind: "full", Width: 1920, Height: 1080,
		Path: "snaps/full.jpg", SizeBytes: 12345,
	}
	thumb := &EventSnapshot{
		EventID: e.ID, Kind: "thumb", Width: 320, Height: 180,
		Path: "snaps/thumb.jpg", SizeBytes: 4321,
	}
	if err := s.EventSnapshots.Insert(ctx, full); err != nil {
		t.Fatalf("insert full: %v", err)
	}
	if err := s.EventSnapshots.Insert(ctx, thumb); err != nil {
		t.Fatalf("insert thumb: %v", err)
	}

	got, err := s.EventSnapshots.Get(ctx, e.ID, "full")
	if err != nil {
		t.Fatalf("get full: %v", err)
	}
	if got.Width != 1920 || got.SizeBytes != 12345 {
		t.Errorf("got %+v", got)
	}

	all, err := s.EventSnapshots.ListByEvent(ctx, e.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("count: %d", len(all))
	}
}

func TestEventSnapshots_DuplicateKindRejected(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("snap-dup")
	mustInsertCamera(t, s, cam)
	e := newEvent(cam.ID, "motion", time.Now().UTC())
	mustInsertEvent(t, s, e)

	first := &EventSnapshot{EventID: e.ID, Kind: "full", Path: "a.jpg", SizeBytes: 1}
	_ = s.EventSnapshots.Insert(ctx, first)
	err := s.EventSnapshots.Insert(ctx, &EventSnapshot{EventID: e.ID, Kind: "full", Path: "b.jpg", SizeBytes: 2})
	if !errors.Is(err, ErrEventSnapshotExists) {
		t.Fatalf("expected ErrEventSnapshotExists; got %v", err)
	}
}

func TestEventSnapshots_DeleteIsolated(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("snap-del")
	mustInsertCamera(t, s, cam)
	e := newEvent(cam.ID, "motion", time.Now().UTC())
	mustInsertEvent(t, s, e)

	_ = s.EventSnapshots.Insert(ctx, &EventSnapshot{EventID: e.ID, Kind: "full", Path: "f.jpg", SizeBytes: 1})
	_ = s.EventSnapshots.Insert(ctx, &EventSnapshot{EventID: e.ID, Kind: "thumb", Path: "t.jpg", SizeBytes: 1})

	if err := s.EventSnapshots.Delete(ctx, e.ID, "full"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.EventSnapshots.Get(ctx, e.ID, "full"); !errors.Is(err, ErrEventSnapshotNotFound) {
		t.Errorf("expected ErrEventSnapshotNotFound; got %v", err)
	}
	if _, err := s.EventSnapshots.Get(ctx, e.ID, "thumb"); err != nil {
		t.Errorf("thumb removed unexpectedly: %v", err)
	}

	if err := s.EventSnapshots.Delete(ctx, e.ID, "full"); !errors.Is(err, ErrEventSnapshotNotFound) {
		t.Errorf("idempotent delete: got %v", err)
	}
}

func TestEventSnapshots_CascadeOnEventDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("snap-casc")
	mustInsertCamera(t, s, cam)
	e := newEvent(cam.ID, "motion", time.Now().UTC())
	mustInsertEvent(t, s, e)
	_ = s.EventSnapshots.Insert(ctx, &EventSnapshot{EventID: e.ID, Kind: "full", Path: "x.jpg", SizeBytes: 1})

	// Cascade through camera delete.
	if err := s.Cameras.Delete(ctx, cam.ID); err != nil {
		t.Fatalf("delete cam: %v", err)
	}
	got, _ := s.EventSnapshots.ListByEvent(ctx, e.ID)
	if len(got) != 0 {
		t.Errorf("expected cascade; got %d rows", len(got))
	}
}
