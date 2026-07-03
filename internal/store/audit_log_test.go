package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditLog_InsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	e := &AuditEntry{
		Action:        "user.login",
		ActorUsername: "admin",
		ActorIP:       "10.0.0.5",
		TargetKind:    "user",
		TargetID:      "u-1",
		Details:       "ok",
	}
	if err := s.AuditLog.Insert(ctx, e); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if e.ID == "" {
		t.Errorf("ID not assigned")
	}
	got, err := s.AuditLog.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Action != "user.login" || got.ActorUsername != "admin" {
		t.Errorf("got %+v", got)
	}
	if got.OccurredAt.IsZero() {
		t.Errorf("occurred_at zero")
	}
}

func TestAuditLog_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.AuditLog.GetByID(context.Background(), "missing")
	if !errors.Is(err, ErrAuditEntryNotFound) {
		t.Errorf("expected ErrAuditEntryNotFound; got %v", err)
	}
}

func TestAuditLog_ListFilters(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = s.AuditLog.Insert(ctx, &AuditEntry{
			ID:         uuid.NewString(),
			Action:     "user.login",
			TargetKind: "user",
			OccurredAt: time.Now().Add(time.Duration(-i) * time.Minute),
		})
	}
	for i := 0; i < 3; i++ {
		_ = s.AuditLog.Insert(ctx, &AuditEntry{
			ID:         uuid.NewString(),
			Action:     "camera.create",
			TargetKind: "camera",
			OccurredAt: time.Now().Add(time.Duration(-i) * time.Minute),
		})
	}

	got, _, err := s.AuditLog.List(ctx, ListAuditFilter{Action: "user.login"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("by action: %d", len(got))
	}
	got, _, _ = s.AuditLog.List(ctx, ListAuditFilter{TargetKind: "camera"})
	if len(got) != 3 {
		t.Errorf("by target_kind: %d", len(got))
	}
}

func TestAuditLog_Pagination(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = s.AuditLog.Insert(ctx, &AuditEntry{
			ID:     uuid.NewString(),
			Action: "x",
		})
	}
	page1, c1, err := s.AuditLog.List(ctx, ListAuditFilter{Limit: 4})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 4 || c1 == "" {
		t.Fatalf("page1 len=%d cursor=%q", len(page1), c1)
	}
	page2, c2, _ := s.AuditLog.List(ctx, ListAuditFilter{Limit: 4, Cursor: c1})
	if len(page2) != 4 || c2 == "" {
		t.Fatalf("page2 len=%d cursor=%q", len(page2), c2)
	}
	page3, c3, _ := s.AuditLog.List(ctx, ListAuditFilter{Limit: 4, Cursor: c2})
	if len(page3) != 2 || c3 != "" {
		t.Errorf("page3 len=%d cursor=%q", len(page3), c3)
	}
}

func TestAuditLog_StreamForExport(t *testing.T) {
	s := mustOpenStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		_ = s.AuditLog.Insert(ctx, &AuditEntry{
			ID:         uuid.NewString(),
			OccurredAt: base.Add(time.Duration(i) * time.Hour),
			Action:     "x",
		})
	}

	ch, err := s.AuditLog.StreamForExport(ctx, base.Add(-time.Hour), base.Add(2*time.Hour+time.Minute))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got int
	for range ch {
		got++
	}
	// Three rows in [base-1h, base+2h+1m].
	if got != 3 {
		t.Errorf("count: %d", got)
	}
}

func TestAuditLog_PurgeOlderThan(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		_ = s.AuditLog.Insert(ctx, &AuditEntry{
			ID:         uuid.NewString(),
			OccurredAt: cutoff.Add(-time.Duration(i+1) * time.Minute),
			Action:     "old",
		})
	}
	for i := 0; i < 2; i++ {
		_ = s.AuditLog.Insert(ctx, &AuditEntry{
			ID:         uuid.NewString(),
			OccurredAt: time.Now(),
			Action:     "new",
		})
	}
	n, err := s.AuditLog.PurgeOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted: %d", n)
	}
	all, _, _ := s.AuditLog.List(ctx, ListAuditFilter{Limit: 100})
	if len(all) != 2 {
		t.Errorf("survivors: %d", len(all))
	}
}
