package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRecordingSchedules_ReplaceAllAndList(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	in := []*RecordingSchedule{
		{ID: uuid.NewString(), DayOfWeek: 1, StartMinute: 540, EndMinute: 1020},
		{ID: uuid.NewString(), DayOfWeek: 0, StartMinute: 0, EndMinute: 720},
		{ID: uuid.NewString(), DayOfWeek: 1, StartMinute: 60, EndMinute: 540},
	}
	if err := s.RecordingSchedules.ReplaceAllForPolicy(ctx, "policy_default", in); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, err := s.RecordingSchedules.ListByPolicy(ctx, "policy_default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: %d", len(got))
	}
	// Day 0 first, then day 1 entries (60 before 540).
	if got[0].DayOfWeek != 0 || got[1].DayOfWeek != 1 || got[1].StartMinute != 60 || got[2].StartMinute != 540 {
		t.Errorf("ordering: %+v", got)
	}
}

func TestRecordingSchedules_ReplaceIdempotent(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	first := []*RecordingSchedule{
		{ID: uuid.NewString(), DayOfWeek: 0, StartMinute: 0, EndMinute: 100},
		{ID: uuid.NewString(), DayOfWeek: 1, StartMinute: 0, EndMinute: 100},
	}
	_ = s.RecordingSchedules.ReplaceAllForPolicy(ctx, "policy_default", first)

	second := []*RecordingSchedule{
		{ID: uuid.NewString(), DayOfWeek: 2, StartMinute: 0, EndMinute: 100},
	}
	if err := s.RecordingSchedules.ReplaceAllForPolicy(ctx, "policy_default", second); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	got, _ := s.RecordingSchedules.ListByPolicy(ctx, "policy_default")
	if len(got) != 1 || got[0].DayOfWeek != 2 {
		t.Errorf("not replaced: %+v", got)
	}
}

func TestRecordingSchedules_ReplaceEmpty(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	in := []*RecordingSchedule{
		{ID: uuid.NewString(), DayOfWeek: 0, StartMinute: 0, EndMinute: 100},
	}
	_ = s.RecordingSchedules.ReplaceAllForPolicy(ctx, "policy_default", in)

	if err := s.RecordingSchedules.ReplaceAllForPolicy(ctx, "policy_default", nil); err != nil {
		t.Fatalf("replace empty: %v", err)
	}
	got, _ := s.RecordingSchedules.ListByPolicy(ctx, "policy_default")
	if len(got) != 0 {
		t.Errorf("expected empty; got %d", len(got))
	}
}

func TestRecordingSchedules_DeleteByPolicy(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	in := []*RecordingSchedule{
		{ID: uuid.NewString(), DayOfWeek: 0, StartMinute: 0, EndMinute: 100},
	}
	_ = s.RecordingSchedules.ReplaceAllForPolicy(ctx, "policy_default", in)

	if err := s.RecordingSchedules.DeleteByPolicy(ctx, "policy_default"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.RecordingSchedules.ListByPolicy(ctx, "policy_default")
	if len(got) != 0 {
		t.Errorf("not deleted: %+v", got)
	}
}

func TestRecordingSchedules_CascadeOnPolicyDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	p := &RecordingPolicy{
		ID: uuid.NewString(), Name: "to-delete", Mode: "scheduled", Container: "fmp4",
		RetentionDurationSeconds: 100, MinSegmentDurationSeconds: 30,
		MaxSegmentDurationSeconds: 60, PartDurationMS: 500, MaxPartSizeBytes: 1024, Enabled: true,
	}
	_ = s.RecordingPolicies.Insert(ctx, p)

	in := []*RecordingSchedule{{ID: uuid.NewString(), DayOfWeek: 0, StartMinute: 0, EndMinute: 100}}
	_ = s.RecordingSchedules.ReplaceAllForPolicy(ctx, p.ID, in)

	if err := s.RecordingPolicies.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete policy: %v", err)
	}
	got, _ := s.RecordingSchedules.ListByPolicy(ctx, p.ID)
	if len(got) != 0 {
		t.Errorf("schedules not cascaded: %+v", got)
	}
}
