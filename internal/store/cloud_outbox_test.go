package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCloudOutbox_InsertAndClaim(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	row := &CloudOutboxRow{
		ID:          uuid.NewString(),
		Kind:        "event",
		PayloadJSON: `{"x":1}`,
	}
	if err := s.CloudOutbox.Insert(ctx, row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	claimed, err := s.CloudOutbox.ClaimBatch(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("count: %d", len(claimed))
	}
	if claimed[0].State != "in_flight" || claimed[0].Kind != "event" {
		t.Errorf("claimed: %+v", claimed[0])
	}
}

func TestCloudOutbox_ClaimRespectsNextAttempt(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	future := &CloudOutboxRow{
		ID: uuid.NewString(), Kind: "event", PayloadJSON: `{}`,
		NextAttemptAt: time.Now().Add(time.Hour),
	}
	_ = s.CloudOutbox.Insert(ctx, future)
	now := &CloudOutboxRow{
		ID: uuid.NewString(), Kind: "event", PayloadJSON: `{}`,
		NextAttemptAt: time.Now().UTC().Add(-time.Minute),
	}
	_ = s.CloudOutbox.Insert(ctx, now)

	claimed, _ := s.CloudOutbox.ClaimBatch(ctx, 10)
	if len(claimed) != 1 {
		t.Fatalf("count: %d", len(claimed))
	}
	if claimed[0].ID != now.ID {
		t.Errorf("wrong claim: %+v", claimed[0])
	}
}

func TestCloudOutbox_StateTransitions(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	row := &CloudOutboxRow{ID: uuid.NewString(), Kind: "event", PayloadJSON: `{}`}
	_ = s.CloudOutbox.Insert(ctx, row)
	_, _ = s.CloudOutbox.ClaimBatch(ctx, 10)

	if err := s.CloudOutbox.MarkFailed(ctx, row.ID, "boom", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := s.CloudOutbox.MarkDead(ctx, row.ID, "fatal"); err != nil {
		t.Fatalf("mark dead: %v", err)
	}

	row2 := &CloudOutboxRow{ID: uuid.NewString(), Kind: "audit", PayloadJSON: `{}`}
	_ = s.CloudOutbox.Insert(ctx, row2)
	_, _ = s.CloudOutbox.ClaimBatch(ctx, 10)
	if err := s.CloudOutbox.MarkDelivered(ctx, row2.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
}

func TestCloudOutbox_DeleteOlderThan(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-time.Hour)

	for i := 0; i < 3; i++ {
		row := &CloudOutboxRow{
			ID: uuid.NewString(), Kind: "event", PayloadJSON: `{}`,
			CreatedAt:     cutoff.Add(-time.Duration(i+1) * time.Minute),
			NextAttemptAt: cutoff.Add(-time.Duration(i+1) * time.Minute),
		}
		_ = s.CloudOutbox.Insert(ctx, row)
	}
	for i := 0; i < 2; i++ {
		row := &CloudOutboxRow{ID: uuid.NewString(), Kind: "event", PayloadJSON: `{}`}
		_ = s.CloudOutbox.Insert(ctx, row)
	}

	n, err := s.CloudOutbox.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted: %d", n)
	}
}

func TestCloudOutbox_NotFound(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	if err := s.CloudOutbox.MarkDelivered(ctx, "missing"); !errors.Is(err, ErrCloudOutboxNotFound) {
		t.Errorf("delivered: %v", err)
	}
	if err := s.CloudOutbox.MarkFailed(ctx, "missing", "x", time.Now()); !errors.Is(err, ErrCloudOutboxNotFound) {
		t.Errorf("failed: %v", err)
	}
	if err := s.CloudOutbox.MarkDead(ctx, "missing", "x"); !errors.Is(err, ErrCloudOutboxNotFound) {
		t.Errorf("dead: %v", err)
	}
}
