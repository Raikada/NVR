package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustInsertOutboxParents(t *testing.T, s *Store) (string, string) {
	t.Helper()
	cam := newCamera("ob-cam-" + uuid.NewString()[:8])
	mustInsertCamera(t, s, cam)
	e := newEvent(cam.ID, "motion", time.Now().UTC())
	mustInsertEvent(t, s, e)
	tgt := mustInsertTarget(t, s, "webhook", "ob-tgt-"+uuid.NewString()[:8], true)
	return tgt.ID, e.ID
}

func TestNotificationOutbox_InsertAndClaim(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	targetID, eventID := mustInsertOutboxParents(t, s)

	row := &NotificationOutboxRow{
		ID:          uuid.NewString(),
		TargetID:    targetID,
		EventID:     eventID,
		PayloadJSON: `{"x":1}`,
	}
	if err := s.NotificationOutbox.Insert(ctx, row); err != nil {
		t.Fatalf("insert: %v", err)
	}

	claimed, err := s.NotificationOutbox.ClaimBatch(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("count: %d", len(claimed))
	}
	if claimed[0].State != "in_flight" {
		t.Errorf("state: %q", claimed[0].State)
	}
	// Re-claim must yield 0 rows.
	again, _ := s.NotificationOutbox.ClaimBatch(ctx, 10)
	if len(again) != 0 {
		t.Errorf("re-claim returned %d rows", len(again))
	}
}

func TestNotificationOutbox_ClaimRespectsNextAttemptAt(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	targetID, eventID := mustInsertOutboxParents(t, s)

	future := &NotificationOutboxRow{
		ID:            uuid.NewString(),
		TargetID:      targetID,
		EventID:       eventID,
		PayloadJSON:   `{}`,
		NextAttemptAt: time.Now().Add(time.Hour),
	}
	_ = s.NotificationOutbox.Insert(ctx, future)
	now := &NotificationOutboxRow{
		ID:            uuid.NewString(),
		TargetID:      targetID,
		EventID:       eventID,
		PayloadJSON:   `{}`,
		NextAttemptAt: time.Now().UTC().Add(-time.Minute),
	}
	_ = s.NotificationOutbox.Insert(ctx, now)

	claimed, _ := s.NotificationOutbox.ClaimBatch(ctx, 10)
	if len(claimed) != 1 {
		t.Fatalf("count: %d", len(claimed))
	}
	if claimed[0].ID != now.ID {
		t.Errorf("claimed wrong row: %+v", claimed[0])
	}
}

func TestNotificationOutbox_MarkDeliveredFailedDead(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	targetID, eventID := mustInsertOutboxParents(t, s)

	row := &NotificationOutboxRow{
		ID: uuid.NewString(), TargetID: targetID, EventID: eventID, PayloadJSON: `{}`,
	}
	_ = s.NotificationOutbox.Insert(ctx, row)
	_, _ = s.NotificationOutbox.ClaimBatch(ctx, 10)

	if err := s.NotificationOutbox.MarkFailed(ctx, row.ID, "boom", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, _ := s.NotificationOutbox.ListRecent(ctx, 10)
	if got[0].State != "pending" || got[0].Attempts != 1 || got[0].LastError != "boom" {
		t.Errorf("after fail: %+v", got[0])
	}

	if err := s.NotificationOutbox.MarkDead(ctx, row.ID, "fatal"); err != nil {
		t.Fatalf("mark dead: %v", err)
	}
	got, _ = s.NotificationOutbox.ListRecent(ctx, 10)
	if got[0].State != "dead" {
		t.Errorf("after dead: %+v", got[0])
	}

	row2 := &NotificationOutboxRow{
		ID: uuid.NewString(), TargetID: targetID, EventID: eventID, PayloadJSON: `{}`,
	}
	_ = s.NotificationOutbox.Insert(ctx, row2)
	_, _ = s.NotificationOutbox.ClaimBatch(ctx, 10)
	if err := s.NotificationOutbox.MarkDelivered(ctx, row2.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	got, _ = s.NotificationOutbox.ListRecent(ctx, 10)
	for _, r := range got {
		if r.ID == row2.ID {
			if r.State != "delivered" || r.DeliveredAt.IsZero() {
				t.Errorf("delivered not set: %+v", r)
			}
		}
	}
}

func TestNotificationOutbox_Reset(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	targetID, eventID := mustInsertOutboxParents(t, s)

	row := &NotificationOutboxRow{
		ID: uuid.NewString(), TargetID: targetID, EventID: eventID, PayloadJSON: `{}`,
	}
	_ = s.NotificationOutbox.Insert(ctx, row)
	_, _ = s.NotificationOutbox.ClaimBatch(ctx, 10)
	_ = s.NotificationOutbox.MarkDead(ctx, row.ID, "dead")

	if err := s.NotificationOutbox.Reset(ctx, row.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	claimed, _ := s.NotificationOutbox.ClaimBatch(ctx, 10)
	if len(claimed) != 1 || claimed[0].ID != row.ID {
		t.Errorf("reset row not claimable: %+v", claimed)
	}
}

func TestNotificationOutbox_NotFound(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	if err := s.NotificationOutbox.MarkDelivered(ctx, "missing"); !errors.Is(err, ErrNotificationOutboxNotFound) {
		t.Errorf("delivered: %v", err)
	}
	if err := s.NotificationOutbox.MarkFailed(ctx, "missing", "x", time.Now()); !errors.Is(err, ErrNotificationOutboxNotFound) {
		t.Errorf("failed: %v", err)
	}
	if err := s.NotificationOutbox.MarkDead(ctx, "missing", "x"); !errors.Is(err, ErrNotificationOutboxNotFound) {
		t.Errorf("dead: %v", err)
	}
	if err := s.NotificationOutbox.Reset(ctx, "missing"); !errors.Is(err, ErrNotificationOutboxNotFound) {
		t.Errorf("reset: %v", err)
	}
}
