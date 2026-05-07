package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustInsertEvent(t *testing.T, s *Store, e *Event) {
	t.Helper()
	if err := s.Events.Insert(context.Background(), e); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func newEvent(camID, typeID string, occurredAt time.Time) *Event {
	return &Event{
		ID:         uuid.NewString(),
		CameraID:   camID,
		TypeID:     typeID,
		Source:     "internal",
		OccurredAt: occurredAt,
		ReceivedAt: occurredAt,
		ExpiresAt:  occurredAt.Add(24 * time.Hour),
	}
}

func TestEvents_InsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ev-cam")
	mustInsertCamera(t, s, cam)

	when := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	e := newEvent(cam.ID, "motion", when)
	e.Severity = "warning"
	e.PayloadJSON = `{"x":1}`
	e.RegionJSON = `[[0,0],[1,1]]`
	mustInsertEvent(t, s, e)

	got, err := s.Events.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CameraID != cam.ID || got.TypeID != "motion" || got.Severity != "warning" {
		t.Errorf("got %+v", got)
	}
	if got.PayloadJSON != `{"x":1}` || got.RegionJSON != `[[0,0],[1,1]]` {
		t.Errorf("payload/region: %+v", got)
	}
	if !got.OccurredAt.Equal(when) {
		t.Errorf("occurred_at: %v", got.OccurredAt)
	}
}

func TestEvents_InsertWithoutSeverity(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ev-no-sev")
	mustInsertCamera(t, s, cam)

	when := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	e := newEvent(cam.ID, "motion", when)
	mustInsertEvent(t, s, e)

	got, err := s.Events.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Severity != "" {
		t.Errorf("severity: %q", got.Severity)
	}
}

func TestEvents_GetByID_NotFound(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.Events.GetByID(context.Background(), "missing")
	if !errors.Is(err, ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound; got %v", err)
	}
}

func TestEvents_ListFilters(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam1 := newCamera("ev-list-1")
	cam2 := newCamera("ev-list-2")
	mustInsertCamera(t, s, cam1)
	mustInsertCamera(t, s, cam2)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		e := newEvent(cam1.ID, "motion", base.Add(time.Duration(i)*time.Hour))
		e.Severity = "info"
		mustInsertEvent(t, s, e)
	}
	for i := 0; i < 3; i++ {
		e := newEvent(cam2.ID, "doorbell", base.Add(time.Duration(i)*time.Hour))
		e.Severity = "critical"
		mustInsertEvent(t, s, e)
	}

	got, _, err := s.Events.List(ctx, ListEventsFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(got) != 8 {
		t.Errorf("all: %d", len(got))
	}

	got, _, _ = s.Events.List(ctx, ListEventsFilter{CameraIDs: []string{cam1.ID}})
	if len(got) != 5 {
		t.Errorf("by camera: %d", len(got))
	}

	got, _, _ = s.Events.List(ctx, ListEventsFilter{TypeIDs: []string{"doorbell"}})
	if len(got) != 3 {
		t.Errorf("by type: %d", len(got))
	}

	got, _, _ = s.Events.List(ctx, ListEventsFilter{
		From: base.Add(2 * time.Hour),
		To:   base.Add(3 * time.Hour),
	})
	if len(got) != 3 { // motion @ +2,+3 + doorbell @ +2.
		t.Errorf("time range: %d", len(got))
	}

	got, _, _ = s.Events.List(ctx, ListEventsFilter{MinSev: "critical"})
	if len(got) != 3 {
		t.Errorf("min sev: %d", len(got))
	}
}

func TestEvents_ListPagination(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ev-page")
	mustInsertCamera(t, s, cam)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		e := newEvent(cam.ID, "motion", base.Add(time.Duration(i)*time.Minute))
		mustInsertEvent(t, s, e)
		ids = append(ids, e.ID)
	}

	page1, cursor1, err := s.Events.List(ctx, ListEventsFilter{Limit: 4})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 4 || cursor1 == "" {
		t.Fatalf("page1 len=%d cursor=%q", len(page1), cursor1)
	}
	page2, cursor2, _ := s.Events.List(ctx, ListEventsFilter{Limit: 4, Cursor: cursor1})
	if len(page2) != 4 || cursor2 == "" {
		t.Fatalf("page2 len=%d cursor=%q", len(page2), cursor2)
	}
	page3, cursor3, _ := s.Events.List(ctx, ListEventsFilter{Limit: 4, Cursor: cursor2})
	if len(page3) != 2 || cursor3 != "" {
		t.Errorf("page3 len=%d cursor=%q", len(page3), cursor3)
	}
	// Pages should be disjoint.
	seen := map[string]bool{}
	for _, e := range page1 {
		seen[e.ID] = true
	}
	for _, e := range page2 {
		if seen[e.ID] {
			t.Fatalf("dup id: %s", e.ID)
		}
		seen[e.ID] = true
	}
	for _, e := range page3 {
		if seen[e.ID] {
			t.Fatalf("dup id: %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestEvents_OnlyUnack(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ev-ack")
	mustInsertCamera(t, s, cam)
	user := &LocalUser{ID: uuid.NewString(), Username: "u-ack", PasswordHash: "h", IsActive: true}
	_ = s.LocalUsers.Insert(ctx, user)

	when := time.Now().UTC()
	e1 := newEvent(cam.ID, "motion", when)
	mustInsertEvent(t, s, e1)
	e2 := newEvent(cam.ID, "motion", when.Add(time.Second))
	mustInsertEvent(t, s, e2)

	if err := s.Events.Acknowledge(ctx, e1.ID, user.ID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	got, _, _ := s.Events.List(ctx, ListEventsFilter{OnlyUnack: true})
	if len(got) != 1 || got[0].ID != e2.ID {
		t.Errorf("only unack: %+v", got)
	}
}

func TestEvents_AcknowledgeIdempotent(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ack-idem")
	mustInsertCamera(t, s, cam)
	user := &LocalUser{ID: uuid.NewString(), Username: "u-idem", PasswordHash: "h", IsActive: true}
	_ = s.LocalUsers.Insert(ctx, user)

	e := newEvent(cam.ID, "motion", time.Now().UTC())
	mustInsertEvent(t, s, e)

	if err := s.Events.Acknowledge(ctx, e.ID, user.ID); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	got, _ := s.Events.GetByID(ctx, e.ID)
	first := got.AcknowledgedAt

	time.Sleep(2 * time.Millisecond)
	if err := s.Events.Acknowledge(ctx, e.ID, user.ID); err != nil {
		t.Fatalf("second ack: %v", err)
	}
	got, _ = s.Events.GetByID(ctx, e.ID)
	if !got.AcknowledgedAt.Equal(first) {
		t.Errorf("ack timestamp got rewritten")
	}

	if err := s.Events.Acknowledge(ctx, "missing", user.ID); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound; got %v", err)
	}
}

func TestEvents_DeleteExpired(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ev-exp")
	mustInsertCamera(t, s, cam)

	now := time.Now().UTC()
	// Two expired, one in the future.
	for i := 0; i < 2; i++ {
		e := newEvent(cam.ID, "motion", now.Add(-time.Hour))
		e.ExpiresAt = now.Add(-time.Minute - time.Duration(i)*time.Second)
		mustInsertEvent(t, s, e)
	}
	live := newEvent(cam.ID, "motion", now)
	live.ExpiresAt = now.Add(time.Hour)
	mustInsertEvent(t, s, live)

	n, err := s.Events.DeleteExpired(ctx, 100)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n != 2 {
		t.Errorf("count: %d", n)
	}
	got, err := s.Events.GetByID(ctx, live.ID)
	if err != nil || got == nil {
		t.Errorf("live row removed unexpectedly: %v", err)
	}
}

func TestEvents_CountByCameraSince(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("ev-count")
	mustInsertCamera(t, s, cam)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		mustInsertEvent(t, s, newEvent(cam.ID, "motion", base.Add(time.Duration(i)*time.Hour)))
	}

	n, err := s.Events.CountByCameraSince(ctx, cam.ID, base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count: %d", n)
	}
}

// Used to keep fmt import alive even if other tests are pruned.
var _ = fmt.Sprintf
