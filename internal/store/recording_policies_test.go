package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRecordingPolicies_DefaultSeeded(t *testing.T) {
	s := mustOpenStore(t)
	got, err := s.RecordingPolicies.GetByID(context.Background(), "policy_default")
	if err != nil {
		t.Fatalf("get policy_default: %v", err)
	}
	if got.Name != "Default Policy" {
		t.Errorf("name: %q", got.Name)
	}
	if got.Mode != "continuous" {
		t.Errorf("mode: %q", got.Mode)
	}
	if got.PreEventSeconds != 5 || got.PostEventSeconds != 5 {
		t.Errorf("pre/post: %d/%d", got.PreEventSeconds, got.PostEventSeconds)
	}
}

func TestRecordingPolicies_InsertCustomAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	p := &RecordingPolicy{
		ID:                        uuid.NewString(),
		Name:                      "garage-low",
		Mode:                      "motion",
		RetentionDurationSeconds:  86400,
		Container:                 "fmp4",
		MinSegmentDurationSeconds: 30,
		MaxSegmentDurationSeconds: 120,
		PartDurationMS:            500,
		MaxPartSizeBytes:          524288,
		PreEventSeconds:           10,
		PostEventSeconds:          15,
		Enabled:                   true,
	}
	if err := s.RecordingPolicies.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.RecordingPolicies.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mode != "motion" || got.RetentionDurationSeconds != 86400 || got.PreEventSeconds != 10 {
		t.Errorf("got %+v", got)
	}

	got2, err := s.RecordingPolicies.GetByName(ctx, "garage-low")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got2.ID != p.ID {
		t.Errorf("by name id mismatch")
	}
}

func TestRecordingPolicies_DuplicateNameRejected(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	p1 := &RecordingPolicy{
		ID: uuid.NewString(), Name: "dup", Mode: "off", Container: "fmp4",
		RetentionDurationSeconds: 100, MinSegmentDurationSeconds: 30,
		MaxSegmentDurationSeconds: 60, PartDurationMS: 500, MaxPartSizeBytes: 1024, Enabled: true,
	}
	_ = s.RecordingPolicies.Insert(ctx, p1)
	p2 := *p1
	p2.ID = uuid.NewString()
	err := s.RecordingPolicies.Insert(ctx, &p2)
	if !errors.Is(err, ErrRecordingPolicyExists) {
		t.Fatalf("expected ErrRecordingPolicyExists; got %v", err)
	}
}

func TestRecordingPolicies_Update(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	p := &RecordingPolicy{
		ID: uuid.NewString(), Name: "u1", Mode: "continuous", Container: "fmp4",
		RetentionDurationSeconds: 100, MinSegmentDurationSeconds: 30,
		MaxSegmentDurationSeconds: 60, PartDurationMS: 500, MaxPartSizeBytes: 1024, Enabled: true,
	}
	_ = s.RecordingPolicies.Insert(ctx, p)
	p.RetentionDurationSeconds = 999
	p.Enabled = false
	if err := s.RecordingPolicies.Update(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.RecordingPolicies.GetByID(ctx, p.ID)
	if got.RetentionDurationSeconds != 999 || got.Enabled {
		t.Errorf("update didn't take: %+v", got)
	}

	bogus := *p
	bogus.ID = "missing"
	if err := s.RecordingPolicies.Update(ctx, &bogus); !errors.Is(err, ErrRecordingPolicyNotFound) {
		t.Errorf("expected ErrRecordingPolicyNotFound; got %v", err)
	}
}

func TestRecordingPolicies_DeleteCustomOK(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	p := &RecordingPolicy{
		ID: uuid.NewString(), Name: "del", Mode: "off", Container: "fmp4",
		RetentionDurationSeconds: 100, MinSegmentDurationSeconds: 30,
		MaxSegmentDurationSeconds: 60, PartDurationMS: 500, MaxPartSizeBytes: 1024, Enabled: true,
	}
	_ = s.RecordingPolicies.Insert(ctx, p)
	if err := s.RecordingPolicies.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.RecordingPolicies.Delete(ctx, p.ID); !errors.Is(err, ErrRecordingPolicyNotFound) {
		t.Errorf("expected ErrRecordingPolicyNotFound; got %v", err)
	}
}

func TestRecordingPolicies_DeleteDefaultRejected(t *testing.T) {
	s := mustOpenStore(t)
	err := s.RecordingPolicies.Delete(context.Background(), "policy_default")
	if !errors.Is(err, ErrPolicyIsDefault) {
		t.Fatalf("expected ErrPolicyIsDefault; got %v", err)
	}
}

func TestRecordingPolicies_List(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = s.RecordingPolicies.Insert(ctx, &RecordingPolicy{
			ID: uuid.NewString(), Name: "p" + string(rune('0'+i)),
			Mode: "off", Container: "fmp4",
			RetentionDurationSeconds: 100, MinSegmentDurationSeconds: 30,
			MaxSegmentDurationSeconds: 60, PartDurationMS: 500, MaxPartSizeBytes: 1024, Enabled: true,
		})
	}
	all, err := s.RecordingPolicies.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// 3 inserted + 1 seeded.
	if len(all) != 4 {
		t.Errorf("list count: %d", len(all))
	}
}
