package cloudbridge

import (
	"context"
	"time"
)

// NopProcessor is the foundation implementation: it sleeps until the
// context is cancelled. The horizon sweeper running alongside it trims
// any cloud_outbox rows that accumulate because nothing is draining
// them.
type NopProcessor struct{}

// NewNopProcessor returns a NopProcessor.
func NewNopProcessor() *NopProcessor { return &NopProcessor{} }

// Run blocks until ctx is cancelled.
func (n *NopProcessor) Run(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			// no-op
		}
	}
}
