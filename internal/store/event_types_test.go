package store

import (
	"context"
	"errors"
	"testing"
)

func TestEventTypes_SeededRowsExist(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	for _, id := range []string{"motion", "doorbell", "line_cross", "tamper", "camera_offline", "camera_online"} {
		got, err := s.EventTypes.GetByID(ctx, id)
		if err != nil {
			t.Errorf("get %s: %v", id, err)
			continue
		}
		if got.DisplayName == "" {
			t.Errorf("display_name empty for %s", id)
		}
	}
	all, err := s.EventTypes.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 12 {
		t.Errorf("expected 12 seeded types; got %d", len(all))
	}
}

func TestEventTypes_InsertCustom(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	e := &EventType{ID: "fancy_event", DisplayName: "Fancy", Vendor: "custom", Description: "test"}
	if err := s.EventTypes.Insert(ctx, e); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, _ := s.EventTypes.GetByID(ctx, "fancy_event")
	if got.DisplayName != "Fancy" || got.Vendor != "custom" || got.Description != "test" {
		t.Errorf("got %+v", got)
	}
}

func TestEventTypes_InsertDuplicate(t *testing.T) {
	s := mustOpenStore(t)
	err := s.EventTypes.Insert(context.Background(), &EventType{ID: "motion", DisplayName: "Motion2", Vendor: "custom"})
	if !errors.Is(err, ErrEventTypeExists) {
		t.Fatalf("expected ErrEventTypeExists; got %v", err)
	}
}

func TestEventTypes_UpdateOnCustom(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	_ = s.EventTypes.Insert(ctx, &EventType{ID: "ut", DisplayName: "Old", Vendor: "custom"})
	if err := s.EventTypes.Update(ctx, &EventType{ID: "ut", DisplayName: "New", Description: "desc"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.EventTypes.GetByID(ctx, "ut")
	if got.DisplayName != "New" || got.Description != "desc" {
		t.Errorf("update didn't take: %+v", got)
	}
	if got.Vendor != "custom" {
		t.Errorf("vendor changed: %q", got.Vendor)
	}
}

func TestEventTypes_DeleteCustom(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	_ = s.EventTypes.Insert(ctx, &EventType{ID: "del", DisplayName: "X", Vendor: "custom"})
	if err := s.EventTypes.Delete(ctx, "del"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.EventTypes.Delete(ctx, "del"); !errors.Is(err, ErrEventTypeNotFound) {
		t.Errorf("expected ErrEventTypeNotFound; got %v", err)
	}
}

func TestEventTypes_DeleteSeededRejected(t *testing.T) {
	s := mustOpenStore(t)
	err := s.EventTypes.Delete(context.Background(), "motion")
	if !errors.Is(err, ErrEventTypeProtected) {
		t.Errorf("expected ErrEventTypeProtected; got %v", err)
	}
}
