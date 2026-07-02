package retention

import (
	"context"
	"os"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// EventsSweeper deletes expired events in batches.
type EventsSweeper struct {
	events    *store.EventsRepo
	snapshots *store.EventSnapshotsRepo
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

// AttachSnapshots enables the SP4 file pass: snapshot files belonging
// to expired events are unlinked alongside the row cascade.
func (s *EventsSweeper) AttachSnapshots(snaps *store.EventSnapshotsRepo) {
	s.snapshots = snaps
}

// Sweep deletes a single batch of expired events. The Manager calls this
// every Interval; for a one-shot purge use a small loop in the API
// /retention/sweep handler.
func (s *EventsSweeper) Sweep(ctx context.Context) error {
	// Collect snapshot file paths BEFORE the delete — the FK cascade
	// removes the rows, and files unlinked after a failed delete would
	// orphan rows instead (worse than orphan files).
	var paths []string
	if s.snapshots != nil {
		var perr error
		paths, perr = s.snapshots.ListPathsForExpiredEvents(ctx, s.batchSize*4)
		if perr != nil && s.logger != nil {
			s.logger.Log(logger.Warn, "[retention.events] snapshot path collect: %v", perr)
		}
	}

	deleted, err := s.events.DeleteExpired(ctx, s.batchSize)
	if err != nil {
		return err
	}
	if deleted > 0 && s.logger != nil {
		s.logger.Log(logger.Debug, "[retention.events] deleted %d expired events", deleted)
	}
	if deleted > 0 {
		for _, p := range paths {
			if uerr := os.Remove(p); uerr != nil && !os.IsNotExist(uerr) && s.logger != nil {
				s.logger.Log(logger.Warn, "[retention.events] unlink %s: %v", p, uerr)
			}
		}
	}
	return nil
}
