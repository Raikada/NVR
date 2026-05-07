package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestEventRetention_DefaultSeeded(t *testing.T) {
	s := mustOpenStore(t)
	got, err := s.EventRetention.Get(context.Background(), DefaultEventRetentionTypeID)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if got.KeepDurationSeconds != 604800 {
		t.Errorf("default keep_duration: %d", got.KeepDurationSeconds)
	}
}

func TestEventRetention_GetEffectiveTypeSpecific(t *testing.T) {
	s := mustOpenStore(t)
	d, err := s.EventRetention.GetEffective(context.Background(), "motion")
	if err != nil {
		t.Fatalf("effective motion: %v", err)
	}
	if d != 7776000*time.Second {
		t.Errorf("motion duration: %v", d)
	}
}

func TestEventRetention_GetEffectiveFallback(t *testing.T) {
	s := mustOpenStore(t)
	d, err := s.EventRetention.GetEffective(context.Background(), "no_such_type")
	if err != nil {
		t.Fatalf("effective fallback: %v", err)
	}
	if d != 604800*time.Second {
		t.Errorf("fallback duration: %v", d)
	}
}

func TestEventRetention_Upsert(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	e := &EventRetention{TypeID: "custom_type", KeepDurationSeconds: 3600}
	if err := s.EventRetention.Upsert(ctx, e); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.EventRetention.Get(ctx, "custom_type")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.KeepDurationSeconds != 3600 {
		t.Errorf("got %d", got.KeepDurationSeconds)
	}

	e.KeepDurationSeconds = 7200
	_ = s.EventRetention.Upsert(ctx, e)
	got, _ = s.EventRetention.Get(ctx, "custom_type")
	if got.KeepDurationSeconds != 7200 {
		t.Errorf("not replaced: %d", got.KeepDurationSeconds)
	}
}

func TestEventRetention_List(t *testing.T) {
	s := mustOpenStore(t)
	got, err := s.EventRetention.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// 7 seeded rows.
	if len(got) != 7 {
		t.Errorf("list count: %d", len(got))
	}
}

func TestEventRetention_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.EventRetention.Get(context.Background(), "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows; got %v", err)
	}
}
