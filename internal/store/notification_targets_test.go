package store

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNotificationTargets_InsertWebhook(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	tgt := &NotificationTarget{
		ID:                      uuid.NewString(),
		Kind:                    "webhook",
		Name:                    "Slack",
		WebhookURL:              "https://hooks.example.invalid/x",
		WebhookSecretCiphertext: []byte{1, 2, 3},
		WebhookSecretNonce:      []byte{4, 5, 6},
		Enabled:                 true,
	}
	if err := s.NotificationTargets.Insert(ctx, tgt); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.NotificationTargets.GetByID(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != "webhook" || got.Name != "Slack" || got.WebhookURL != tgt.WebhookURL {
		t.Errorf("got %+v", got)
	}
	if !bytes.Equal(got.WebhookSecretCiphertext, tgt.WebhookSecretCiphertext) {
		t.Errorf("ct mismatch")
	}
}

func TestNotificationTargets_InsertEmail(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := &NotificationTarget{
		ID:           uuid.NewString(),
		Kind:         "email",
		Name:         "Owner",
		EmailAddress: "owner@example.invalid",
		Enabled:      true,
	}
	if err := s.NotificationTargets.Insert(ctx, tgt); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, _ := s.NotificationTargets.GetByID(ctx, tgt.ID)
	if got.EmailAddress != "owner@example.invalid" || got.WebhookURL != "" {
		t.Errorf("got %+v", got)
	}
}

func TestNotificationTargets_List(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = s.NotificationTargets.Insert(ctx, &NotificationTarget{
			ID: uuid.NewString(), Kind: "webhook", Name: "n",
			WebhookURL: "https://x.invalid", Enabled: true,
		})
	}
	got, err := s.NotificationTargets.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("count: %d", len(got))
	}
}

func TestNotificationTargets_Update(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := &NotificationTarget{
		ID: uuid.NewString(), Kind: "webhook", Name: "old",
		WebhookURL: "https://old.invalid", Enabled: true,
	}
	_ = s.NotificationTargets.Insert(ctx, tgt)

	tgt.Name = "new"
	tgt.WebhookURL = "https://new.invalid"
	tgt.Enabled = false
	if err := s.NotificationTargets.Update(ctx, tgt); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.NotificationTargets.GetByID(ctx, tgt.ID)
	if got.Name != "new" || got.WebhookURL != "https://new.invalid" || got.Enabled {
		t.Errorf("got %+v", got)
	}

	if err := s.NotificationTargets.SetEnabled(ctx, tgt.ID, true); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	got, _ = s.NotificationTargets.GetByID(ctx, tgt.ID)
	if !got.Enabled {
		t.Errorf("not enabled")
	}
}

func TestNotificationTargets_RotateSecret(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := &NotificationTarget{
		ID: uuid.NewString(), Kind: "webhook", Name: "rotate",
		WebhookURL:              "https://r.invalid",
		WebhookSecretCiphertext: []byte{1},
		WebhookSecretNonce:      []byte{2},
		Enabled:                 true,
	}
	_ = s.NotificationTargets.Insert(ctx, tgt)
	newCT := []byte{99, 100}
	newN := []byte{101, 102}
	if err := s.NotificationTargets.RotateWebhookSecret(ctx, tgt.ID, newCT, newN); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	got, _ := s.NotificationTargets.GetByID(ctx, tgt.ID)
	if !bytes.Equal(got.WebhookSecretCiphertext, newCT) || !bytes.Equal(got.WebhookSecretNonce, newN) {
		t.Errorf("not rotated")
	}
}

func TestNotificationTargets_Delete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	tgt := &NotificationTarget{ID: uuid.NewString(), Kind: "email", Name: "del", EmailAddress: "x@y.invalid"}
	_ = s.NotificationTargets.Insert(ctx, tgt)
	if err := s.NotificationTargets.Delete(ctx, tgt.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.NotificationTargets.Delete(ctx, tgt.ID); !errors.Is(err, ErrNotificationTargetNotFound) {
		t.Errorf("expected ErrNotificationTargetNotFound; got %v", err)
	}
}
