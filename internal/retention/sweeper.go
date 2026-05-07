// Package retention runs the per-domain retention sweepers (segments,
// events, clips). Each sweeper implements Sweeper and is owned by a
// Manager that schedules them on independent tickers. Manager.SweepAll
// is the diagnostic entrypoint exposed by the API and is also called
// once at startup.
package retention

import (
	"context"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// Sweeper is the per-domain interface. Each implementation knows how to
// remove a bounded batch of expired rows / files; the Manager invokes
// Sweep on a periodic ticker. Sweep should be idempotent and bounded
// per call so a long-running pass cannot block other domains.
type Sweeper interface {
	Name() string
	Sweep(ctx context.Context) error
	Interval() time.Duration
}

// Manager owns the sweeper set and runs each on its own goroutine.
type Manager struct {
	sweepers []Sweeper
	logger   logger.Writer
}

// NewManager builds a Manager over the provided sweepers.
func NewManager(log logger.Writer, sweepers ...Sweeper) *Manager {
	return &Manager{sweepers: sweepers, logger: log}
}

// Run blocks until ctx is cancelled. Each sweeper runs on its own
// ticker; Sweep errors are logged at Warn but do not stop the ticker.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, sw := range m.sweepers {
		wg.Add(1)
		go func(sw Sweeper) {
			defer wg.Done()
			m.runOne(ctx, sw)
		}(sw)
	}
	wg.Wait()
}

func (m *Manager) runOne(ctx context.Context, sw Sweeper) {
	tick := time.NewTicker(sw.Interval())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := sw.Sweep(ctx); err != nil && m.logger != nil {
				m.logger.Log(logger.Warn, "[retention.%s] sweep: %v", sw.Name(), err)
			}
		}
	}
}

// SweepAll invokes every sweeper sequentially and aggregates errors via
// the per-sweeper log line. Returns nil unless ctx is cancelled.
func (m *Manager) SweepAll(ctx context.Context) error {
	for _, sw := range m.sweepers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := sw.Sweep(ctx); err != nil && m.logger != nil {
			m.logger.Log(logger.Warn, "[retention.%s] sweep: %v", sw.Name(), err)
		}
	}
	return nil
}
