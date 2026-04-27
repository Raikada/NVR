// Per-volume write-rate observer for /v1/storage-volumes.
//
// Tracks the most recent used_bytes reading per mount path, with a
// timestamp. On each /v1/storage-volumes call the handler asks the
// observer for "bytes/sec since last sample"; the observer computes
// the delta against the prior reading and returns it. Recorder
// retention-pruning can shrink used_bytes between samples, so
// negative deltas clamp to zero (we surface only positive write
// activity, which is what an operator wants from a "write rate"
// signal).
//
// The first sample for any volume returns nil (no prior reading to
// diff against). The observer is process-wide, mirrors the
// EventStore / AuditChain singleton pattern.

package api

import (
	"sync"
	"time"
)

type volumeRateState struct {
	usedBytes int64
	at        time.Time
}

type volumeRateObserver struct {
	mu sync.Mutex
	// Keyed by absolute mount path so two callers observing the
	// same volume see consistent state.
	prev map[string]volumeRateState
}

// Sample records the current used_bytes for a mount path and
// returns the bytes-per-second since the last sample. nil on first
// sighting; nil on backwards-walking deltas (retention pruning),
// which the caller treats as "no positive write activity."
func (o *volumeRateObserver) Sample(mountPath string, usedBytes int64, now time.Time) *int64 {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.prev == nil {
		o.prev = make(map[string]volumeRateState)
	}

	prior, ok := o.prev[mountPath]
	o.prev[mountPath] = volumeRateState{usedBytes: usedBytes, at: now}
	if !ok {
		return nil
	}

	wall := now.Sub(prior.at).Seconds()
	if wall <= 0 {
		return nil
	}
	delta := usedBytes - prior.usedBytes
	if delta <= 0 {
		// Retention pruned faster than recorder wrote, or used_bytes
		// got reset. Surface no rate this interval.
		zero := int64(0)
		return &zero
	}
	rate := int64(float64(delta) / wall)
	return &rate
}

var volumeRateObserverSingleton = &volumeRateObserver{}
