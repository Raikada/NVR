package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCameraGroups_InsertGetList(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	g := &CameraGroup{
		ID:           uuid.NewString(),
		Name:         "front",
		DisplayOrder: 1,
	}
	if err := s.CameraGroups.Insert(ctx, g); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.CameraGroups.GetByID(ctx, g.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Name != "front" || got.DisplayOrder != 1 {
		t.Errorf("got %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps zero: %+v", got)
	}

	g2, err := s.CameraGroups.GetByName(ctx, "front")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if g2.ID != g.ID {
		t.Errorf("get by name returned wrong id")
	}

	g3 := &CameraGroup{ID: uuid.NewString(), Name: "back", DisplayOrder: 2}
	_ = s.CameraGroups.Insert(ctx, g3)

	all, err := s.CameraGroups.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list count: %d", len(all))
	}
	// Front (1) before back (2) by display_order ASC.
	if all[0].Name != "front" {
		t.Errorf("ordering: %+v", all)
	}
}

func TestCameraGroups_DuplicateNameRejected(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	_ = s.CameraGroups.Insert(ctx, &CameraGroup{ID: uuid.NewString(), Name: "x"})
	err := s.CameraGroups.Insert(ctx, &CameraGroup{ID: uuid.NewString(), Name: "x"})
	if !errors.Is(err, ErrCameraGroupExists) {
		t.Fatalf("expected ErrCameraGroupExists; got %v", err)
	}
}

func TestCameraGroups_RenameAndDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	g := &CameraGroup{ID: uuid.NewString(), Name: "x", DisplayOrder: 5}
	_ = s.CameraGroups.Insert(ctx, g)
	g.Name = "y"
	g.DisplayOrder = 9
	if err := s.CameraGroups.Update(ctx, g); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.CameraGroups.GetByID(ctx, g.ID)
	if got.Name != "y" || got.DisplayOrder != 9 {
		t.Errorf("update didn't take: %+v", got)
	}

	if err := s.CameraGroups.Delete(ctx, g.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := s.CameraGroups.GetByID(ctx, g.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after delete; got %v", err)
	}

	if err := s.CameraGroups.Delete(ctx, "missing"); !errors.Is(err, ErrCameraGroupNotFound) {
		t.Errorf("expected ErrCameraGroupNotFound; got %v", err)
	}
	if err := s.CameraGroups.Update(ctx, &CameraGroup{ID: "missing", Name: "z"}); !errors.Is(err, ErrCameraGroupNotFound) {
		t.Errorf("expected ErrCameraGroupNotFound on update; got %v", err)
	}
}
