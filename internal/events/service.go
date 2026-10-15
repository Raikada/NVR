package events

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/store"
)

// CloudOutboxEnqueuer mirrors a single method from the cloud_outbox
// repo so this package does not need to import all of internal/store
// just for one call. *store.CloudOutboxRepo satisfies it structurally.
type CloudOutboxEnqueuer interface {
	Insert(ctx context.Context, row *store.CloudOutboxRow) error
}

// Service is the canonical event entry point. Insert persists, computes
// ExpiresAt from event_retention, enqueues a cloud_outbox row, and
// publishes to the bus.
type Service struct {
	events    *store.EventsRepo
	retention *store.EventRetentionRepo
	cloud     CloudOutboxEnqueuer
	bus       *Bus
}

// NewService wires a Service. cloud may be nil; in that case Insert
// skips the cloud_outbox step.
func NewService(
	events *store.EventsRepo,
	retention *store.EventRetentionRepo,
	cloud CloudOutboxEnqueuer,
	bus *Bus,
) *Service {
	return &Service{events: events, retention: retention, cloud: cloud, bus: bus}
}

// Insert validates, materializes ExpiresAt, persists, enqueues the cloud
// outbox row, and publishes to the bus.
func (s *Service) Insert(ctx context.Context, ev *Event) error {
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	if ev.ReceivedAt.IsZero() {
		ev.ReceivedAt = time.Now().UTC()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = ev.ReceivedAt
	}
	keep, err := s.retention.GetEffective(ctx, ev.TypeID)
	if err != nil {
		return err
	}
	ev.ExpiresAt = ev.ReceivedAt.Add(keep)

	row := &store.Event{
		ID:          ev.ID,
		CameraID:    ev.CameraID,
		TypeID:      ev.TypeID,
		Source:      ev.Source,
		OccurredAt:  ev.OccurredAt,
		ReceivedAt:  ev.ReceivedAt,
		Severity:    ev.Severity,
		PayloadJSON: string(ev.PayloadJSON),
		RegionJSON:  string(ev.RegionJSON),
		ExpiresAt:   ev.ExpiresAt,
	}
	if err := s.events.Insert(ctx, row); err != nil {
		return err
	}
	if s.cloud != nil {
		payload, _ := json.Marshal(ev)
		// Cloud-outbox failure is logged but non-fatal; events table is
		// authoritative.
		_ = s.cloud.Insert(ctx, &store.CloudOutboxRow{
			ID:            uuid.New().String(),
			Kind:          "event",
			PayloadJSON:   string(payload),
			State:         "pending",
			NextAttemptAt: time.Now().UTC(),
			CreatedAt:     time.Now().UTC(),
		})
	}
	s.bus.Publish(*ev)
	return nil
}

// Get fetches an event by id and converts it to the in-memory Event.
func (s *Service) Get(ctx context.Context, id string) (*Event, error) {
	row, err := s.events.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return rowToEvent(row), nil
}

// List paginates events by id DESC. Returns the page and the next cursor.
func (s *Service) List(ctx context.Context, f ListFilter) ([]*Event, string, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	storeFilter := store.ListEventsFilter{
		CameraIDs: f.CameraIDs,
		TypeIDs:   f.TypeIDs,
		Sources:   f.Sources,
		From:      f.From,
		To:        f.To,
		MinSev:    f.MinSev,
		OnlyUnack: f.OnlyUnack,
		Cursor:    f.Cursor,
		Limit:     f.Limit,
	}
	rows, next, err := s.events.List(ctx, storeFilter)
	if err != nil {
		return nil, "", err
	}
	out := make([]*Event, len(rows))
	for i, r := range rows {
		out[i] = rowToEvent(r)
	}
	return out, next, nil
}

// Acknowledge marks an event acknowledged by userID. Idempotent.
func (s *Service) Acknowledge(ctx context.Context, id, userID string) error {
	if id == "" || userID == "" {
		return errors.New("acknowledge: id and userID required")
	}
	return s.events.Acknowledge(ctx, id, userID)
}

// Subscribe returns a bus subscription. Used by the API SSE handler and
// the notifications dispatcher.
func (s *Service) Subscribe() (<-chan Event, func()) {
	return s.bus.Subscribe()
}

func rowToEvent(r *store.Event) *Event {
	ev := &Event{
		ID:             r.ID,
		CameraID:       r.CameraID,
		TypeID:         r.TypeID,
		Source:         r.Source,
		OccurredAt:     r.OccurredAt,
		ReceivedAt:     r.ReceivedAt,
		Severity:       r.Severity,
		PayloadJSON:    json.RawMessage(r.PayloadJSON),
		RegionJSON:     json.RawMessage(r.RegionJSON),
		AcknowledgedAt: r.AcknowledgedAt,
		AcknowledgedBy: r.AcknowledgedBy,
		ExpiresAt:      r.ExpiresAt,
	}
	return ev
}
