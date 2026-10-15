package store

import (
	"context"
	"testing"
)

func TestRoles_GetByID(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	got, err := s.Roles.GetByID(ctx, "role_admin")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if got.Name != "admin" {
		t.Errorf("expected name=admin, got %q", got.Name)
	}

	got, err = s.Roles.GetByID(ctx, "role_viewer")
	if err != nil {
		t.Fatalf("get viewer: %v", err)
	}
	if got.Name != "viewer" {
		t.Errorf("expected name=viewer, got %q", got.Name)
	}
}

func TestRoles_GetByID_NotFound(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.Roles.GetByID(context.Background(), "role_nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRoles_GetByName(t *testing.T) {
	s := mustOpenStore(t)
	got, err := s.Roles.GetByName(context.Background(), "admin")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got.ID != "role_admin" {
		t.Errorf("expected id=role_admin, got %q", got.ID)
	}
}

func TestRoles_List(t *testing.T) {
	s := mustOpenStore(t)
	got, err := s.Roles.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Phase A B2 grew the seed set to five tiers.
	if len(got) != 5 {
		t.Errorf("expected 5 roles, got %d", len(got))
	}
	names := map[string]bool{}
	for _, r := range got {
		names[r.Name] = true
	}
	for _, want := range []string{"admin", "viewer", "owner", "operator", "audit"} {
		if !names[want] {
			t.Errorf("missing seeded role %q", want)
		}
	}
}
