package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func mustInsertTarget(t *testing.T, s *Store, kind, name string, enabled bool) *NotificationTarget {
	t.Helper()
	tgt := &NotificationTarget{
		ID: uuid.NewString(), Kind: kind, Name: name, Enabled: enabled,
	}
	if kind == "webhook" {
		tgt.WebhookURL = "https://x.invalid"
	} else {
		tgt.EmailAddress = "x@y.invalid"
	}
	if err := s.NotificationTargets.Insert(context.Background(), tgt); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	return tgt
}

func TestNotificationSubscriptions_InsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := mustInsertTarget(t, s, "webhook", "Slack", true)

	sub := &NotificationSubscription{
		ID:                    uuid.NewString(),
		TargetID:              tgt.ID,
		EventTypeID:           "motion",
		MinSeverity:           "warning",
		QuietHoursStartMinute: -1,
		QuietHoursEndMinute:   -1,
	}
	if err := s.NotificationSubscriptions.Insert(ctx, sub); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.NotificationSubscriptions.GetByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TargetID != tgt.ID || got.EventTypeID != "motion" || got.MinSeverity != "warning" {
		t.Errorf("got %+v", got)
	}
	if got.QuietHoursStartMinute != -1 || got.QuietHoursEndMinute != -1 {
		t.Errorf("quiet hours: %d/%d", got.QuietHoursStartMinute, got.QuietHoursEndMinute)
	}
}

func TestNotificationSubscriptions_QuietHours(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := mustInsertTarget(t, s, "webhook", "QH", true)

	sub := &NotificationSubscription{
		ID:                    uuid.NewString(),
		TargetID:              tgt.ID,
		QuietHoursStartMinute: 1320,
		QuietHoursEndMinute:   480,
	}
	if err := s.NotificationSubscriptions.Insert(ctx, sub); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, _ := s.NotificationSubscriptions.GetByID(ctx, sub.ID)
	if got.QuietHoursStartMinute != 1320 || got.QuietHoursEndMinute != 480 {
		t.Errorf("got %d/%d", got.QuietHoursStartMinute, got.QuietHoursEndMinute)
	}
}

func TestNotificationSubscriptions_ListAllJoinedSkipsDisabled(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	enabled := mustInsertTarget(t, s, "webhook", "ena", true)
	disabled := mustInsertTarget(t, s, "webhook", "dis", false)

	for _, tgt := range []*NotificationTarget{enabled, disabled} {
		_ = s.NotificationSubscriptions.Insert(ctx, &NotificationSubscription{
			ID: uuid.NewString(), TargetID: tgt.ID, QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
		})
	}

	got, err := s.NotificationSubscriptions.ListAllJoined(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("count: %d", len(got))
	}
	if got[0].TargetID != enabled.ID {
		t.Errorf("wrong target survived: %+v", got[0])
	}
	if got[0].TargetName != "ena" || got[0].TargetKind != "webhook" {
		t.Errorf("denormalized fields: %+v", got[0])
	}
}

func TestNotificationSubscriptions_Delete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := mustInsertTarget(t, s, "webhook", "del", true)
	sub := &NotificationSubscription{
		ID: uuid.NewString(), TargetID: tgt.ID,
		QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
	}
	_ = s.NotificationSubscriptions.Insert(ctx, sub)
	if err := s.NotificationSubscriptions.Delete(ctx, sub.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.NotificationSubscriptions.Delete(ctx, sub.ID); !errors.Is(err, ErrNotificationSubscriptionNotFound) {
		t.Errorf("expected ErrNotificationSubscriptionNotFound; got %v", err)
	}
}

func TestNotificationSubscriptions_CascadeOnTargetDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := mustInsertTarget(t, s, "webhook", "casc", true)
	sub := &NotificationSubscription{
		ID: uuid.NewString(), TargetID: tgt.ID,
		QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
	}
	_ = s.NotificationSubscriptions.Insert(ctx, sub)
	if err := s.NotificationTargets.Delete(ctx, tgt.ID); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	got, _ := s.NotificationSubscriptions.ListAllJoined(ctx)
	if len(got) != 0 {
		t.Errorf("expected cascade; got %d", len(got))
	}
}
