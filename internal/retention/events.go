package retention

import (
	"context"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// EventsSweeper deletes expired events in batches.
type EventsSweeper struct {
	events    *store.EventsRepo
	batchSize int
	interval  time.Duration
	logger    logger.Writer
}

// NewEventsSweeper builds an EventsSweeper. batchSize defaults to 200,
// interval to 5m.
func NewEventsSweeper(events *store.EventsRepo, batchSize int, interval time.Duration, log logger.Writer) *EventsSweeper {
	if batchSize <= 0 {
		batchSize = 200
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &EventsSweeper{events: events, batchSize: batchSize, interval: interval, logger: log}
}

// Name implements Sweeper.
func (s *EventsSweeper) Name() string { return "events" }

// Interval implements Sweeper.
func (s *EventsSweeper) Interval() time.Duration { return s.interval }

// Sweep deletes a single batch of expired events. The Manager calls this
// every Interval; for a one-shot purge use a small loop in the API
// /retention/sweep handler.
func (s *EventsSweeper) Sweep(ctx context.Context) error {
	deleted, err := s.events.DeleteExpired(ctx, s.batchSize)
	if err != nil {
		return err
	}
	if deleted > 0 && s.logger != nil {
		s.logger.Log(logger.Debug, "[retention.events] deleted %d expired events", deleted)
	}
	return nil
}
