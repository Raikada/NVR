// Package cloudbridge is the seam where the recorder pushes events,
// health, and audit rows to a future cloud product. The foundation
// ships only the no-op processor (sleeps and runs an horizon sweeper);
// when the cloud product launches, swap nopProcessor for the real
// HTTP processor without touching the rest of the recorder.
package cloudbridge

import (
	"context"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// Processor is the per-impl seam: foundation uses NopProcessor, future
// cloud product supplies a real implementation that drains cloud_outbox
// over HTTPS. Run blocks until ctx is cancelled.
type Processor interface {
	Run(ctx context.Context)
}

// Service composes a Processor (which drains pending rows) with a
// horizon sweeper (which trims forever-pending rows when no cloud is
// configured, so unconfigured installs don't accumulate cloud_outbox
// forever).
type Service struct {
	processor Processor
	sweeper   *HorizonSweeper
	logger    logger.Writer
}

// NewService wires a Service.
func NewService(p Processor, sw *HorizonSweeper, log logger.Writer) *Service {
	return &Service{processor: p, sweeper: sw, logger: log}
}

// Run blocks until ctx is cancelled. Spins up the processor and the
// horizon sweeper goroutines.
func (s *Service) Run(ctx context.Context) {
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		s.processor.Run(ctx)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		s.sweeper.Run(ctx)
	}()
	<-done
	<-done
}

// HorizonSweeper deletes cloud_outbox rows older than horizonHours. Used
// by the no-op processor so unconfigured installs don't accumulate
// outbox rows forever; the configured-cloud processor MAY also call it
// once-per-day to bound disk usage if delivery falls behind.
type HorizonSweeper struct {
	deleter  HorizonDeleter
	getHours func(ctx context.Context) int
	interval time.Duration
	logger   logger.Writer
}

// HorizonDeleter is the small seam this package uses; satisfied by
// *store.CloudOutboxRepo.DeleteOlderThan.
type HorizonDeleter interface {
	DeleteOlderThan(ctx context.Context, t time.Time) (int, error)
}

// NewHorizonSweeper builds a HorizonSweeper. interval defaults to 1h;
// getHours is invoked per-tick to read the live system_settings value.
func NewHorizonSweeper(d HorizonDeleter, getHours func(ctx context.Context) int, interval time.Duration, log logger.Writer) *HorizonSweeper {
	if interval <= 0 {
		interval = time.Hour
	}
	return &HorizonSweeper{deleter: d, getHours: getHours, interval: interval, logger: log}
}

// Run blocks until ctx is cancelled. Sweeps every Interval.
func (h *HorizonSweeper) Run(ctx context.Context) {
	tick := time.NewTicker(h.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := h.Sweep(ctx); err != nil && h.logger != nil {
				h.logger.Log(logger.Warn, "[cloudbridge.horizon] sweep: %v", err)
			}
		}
	}
}

// Sweep performs one horizon pass. Exposed for tests + the diagnostic
// API endpoint.
func (h *HorizonSweeper) Sweep(ctx context.Context) error {
	hours := 168 // 7d default
	if h.getHours != nil {
		if v := h.getHours(ctx); v > 0 {
			hours = v
		}
	}
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	deleted, err := h.deleter.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return err
	}
	if deleted > 0 && h.logger != nil {
		h.logger.Log(logger.Debug, "[cloudbridge.horizon] deleted %d cloud_outbox rows older than %v", deleted, cutoff)
	}
	return nil
}
