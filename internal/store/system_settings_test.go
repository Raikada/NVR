package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestSystemSettings_SeededRows(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	got, err := s.SystemSettings.Get(ctx, "lockout_threshold")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Value != "5" {
		t.Errorf("value: %q", got.Value)
	}
}

func TestSystemSettings_UpsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	user := &LocalUser{ID: "user-1", Username: "u-ss", PasswordHash: "h", IsActive: true}
	_ = s.LocalUsers.Insert(ctx, user)
	if err := s.SystemSettings.Upsert(ctx, "site_name", "MyHome", "user-1"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ := s.SystemSettings.Get(ctx, "site_name")
	if got.Value != "MyHome" || got.UpdatedBy != "user-1" {
		t.Errorf("got %+v", got)
	}
}

func TestSystemSettings_GetAll(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	got, err := s.SystemSettings.GetAll(ctx)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if got["site_name"] == "" || got["lockout_threshold"] == "" {
		t.Errorf("seeded rows missing: %v", got)
	}
}

func TestSystemSettings_UpsertMany(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	if err := s.SystemSettings.UpsertMany(ctx, map[string]string{
		"site_name": "X",
		"language":  "fr",
	}, ""); err != nil {
		t.Fatalf("upsert many: %v", err)
	}
	got, _ := s.SystemSettings.GetAll(ctx)
	if got["site_name"] != "X" || got["language"] != "fr" {
		t.Errorf("got %v", got)
	}
}

func TestSystemSettings_GetInt(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	n, err := s.SystemSettings.GetInt(ctx, "lockout_threshold", 99)
	if err != nil {
		t.Fatalf("get int: %v", err)
	}
	if n != 5 {
		t.Errorf("value: %d", n)
	}

	n, err = s.SystemSettings.GetInt(ctx, "no_such_key", 42)
	if err != nil || n != 42 {
		t.Errorf("missing: %d %v", n, err)
	}

	_ = s.SystemSettings.Upsert(ctx, "broken", "not-a-number", "")
	n, err = s.SystemSettings.GetInt(ctx, "broken", 7)
	if err != nil || n != 7 {
		t.Errorf("malformed: %d %v", n, err)
	}
}

func TestSystemSettings_GetDuration(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	_ = s.SystemSettings.Upsert(ctx, "probe_interval", "5m", "")
	d, err := s.SystemSettings.GetDuration(ctx, "probe_interval", time.Hour)
	if err != nil || d != 5*time.Minute {
		t.Errorf("got %v %v", d, err)
	}
	d, _ = s.SystemSettings.GetDuration(ctx, "no_such", 7*time.Minute)
	if d != 7*time.Minute {
		t.Errorf("missing: %v", d)
	}
	_ = s.SystemSettings.Upsert(ctx, "broken_dur", "not-a-dur", "")
	d, _ = s.SystemSettings.GetDuration(ctx, "broken_dur", 9*time.Minute)
	if d != 9*time.Minute {
		t.Errorf("malformed: %v", d)
	}
}

func TestSystemSettings_GetBool(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	_ = s.SystemSettings.Upsert(ctx, "enabled", "true", "")
	b, err := s.SystemSettings.GetBool(ctx, "enabled", false)
	if err != nil || !b {
		t.Errorf("got %v %v", b, err)
	}
	b, _ = s.SystemSettings.GetBool(ctx, "no_such", true)
	if !b {
		t.Errorf("default not returned")
	}
	_ = s.SystemSettings.Upsert(ctx, "broken_b", "yes-please", "")
	b, _ = s.SystemSettings.GetBool(ctx, "broken_b", true)
	if !b {
		t.Errorf("malformed default not used")
	}
}

func TestSystemSettings_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.SystemSettings.Get(context.Background(), "no_such_key")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows; got %v", err)
	}
}
