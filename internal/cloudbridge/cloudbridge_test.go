package cloudbridge

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
	s, err := store.Open(filepath.Join(dir, "cb.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHorizonSweeper_DeletesOnlyOldRows(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := &store.CloudOutboxRow{
		ID: "old", Kind: "event", PayloadJSON: "{}",
		CreatedAt: now.Add(-200 * time.Hour), // older than default 168h
	}
	fresh := &store.CloudOutboxRow{
		ID: "fresh", Kind: "event", PayloadJSON: "{}",
		CreatedAt: now.Add(-10 * time.Hour),
	}
	if err := s.CloudOutbox.Insert(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if err := s.CloudOutbox.Insert(ctx, fresh); err != nil {
		t.Fatalf("insert fresh: %v", err)
	}

	sw := NewHorizonSweeper(s.CloudOutbox, func(_ context.Context) int { return 168 }, time.Minute, nil)
	if err := sw.Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// One row should remain.
	rows, err := s.CloudOutbox.ClaimBatch(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "fresh" {
		t.Errorf("expected only 'fresh' to remain, got %v", rows)
	}
}

func TestNopProcessor_StopsOnCancel(t *testing.T) {
	p := NewNopProcessor()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("NopProcessor did not exit on cancel")
	}
}

func TestService_RunReturnsOnCancel(t *testing.T) {
	s := openStore(t)
	sw := NewHorizonSweeper(s.CloudOutbox, func(_ context.Context) int { return 168 }, time.Hour, nil)
	svc := NewService(NewNopProcessor(), sw, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Service.Run did not exit on cancel")
	}
}

func TestHorizonSweeper_HoursOverride(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	// Place a row at -10h. With horizon=24h it should remain. With horizon=1h it should go.
	row := &store.CloudOutboxRow{
		ID: "candidate", Kind: "event", PayloadJSON: "{}",
		CreatedAt: now.Add(-10 * time.Hour),
	}
	if err := s.CloudOutbox.Insert(ctx, row); err != nil {
		t.Fatalf("insert: %v", err)
	}

	keep := NewHorizonSweeper(s.CloudOutbox, func(_ context.Context) int { return 24 }, time.Hour, nil)
	if err := keep.Sweep(ctx); err != nil {
		t.Fatalf("sweep keep: %v", err)
	}
	if rows, _ := s.CloudOutbox.ClaimBatch(ctx, 10); len(rows) != 1 {
		t.Fatalf("expected row to remain at 24h horizon, got %d", len(rows))
	}
}
