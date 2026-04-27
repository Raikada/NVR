// Process-level network bandwidth sampler for /v1/health.bandwidth.
//
// Mirrors cpu_sampler.go: read OS counters at each /v1/health call,
// divide deltas to produce a per-second rate. Bandwidth covers
// every NIC the recorder process can see (lo excluded — loopback
// traffic isn't useful as a "how much is this recorder pushing"
// signal). Linux pulls from /proc/net/dev; non-Linux platforms
// (dev / CI) return zero.
//
// Mirrors mem_sampler split-by-build-tag: implementation lives in
// bandwidth_sampler_linux.go (real reader) and
// bandwidth_sampler_other.go (zero stub).
package api

import (
	"sync"
	"time"
)

// bandwidthSample holds one snapshot of the OS counters; deltas
// against the previous snapshot give us bytes-per-second rates.
type bandwidthSample struct {
	rxBytes uint64
	txBytes uint64
}

// bandwidthSampler computes process-relevant bandwidth between
// successive Sample() calls. Goroutine-safe.
type bandwidthSampler struct {
	mu       sync.Mutex
	prev     bandwidthSample
	prevWall time.Time
	primed   bool
	// Last computed rates, retained between samples so two callers
	// in the same wall-clock interval see the same numbers.
	lastRxBps float64
	lastTxBps float64
}

// Sample returns (rx_bps, tx_bps) — bytes per second since the
// previous Sample call. The first call (no previous snapshot)
// returns (0, 0) and primes the sampler. Subsequent calls return
// real rates. Counters that wrap or go backwards (interface bounce)
// reset cleanly to (0, 0) for that interval.
func (s *bandwidthSampler) Sample() (rxBps, txBps float64) {
	now := time.Now()
	cur, ok := readNetCounters()
	if !ok {
		return 0, 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.primed {
		s.prev = cur
		s.prevWall = now
		s.primed = true
		return 0, 0
	}

	wall := now.Sub(s.prevWall)
	if wall <= 0 {
		return s.lastRxBps, s.lastTxBps
	}

	dRx := int64(cur.rxBytes) - int64(s.prev.rxBytes)
	dTx := int64(cur.txBytes) - int64(s.prev.txBytes)

	s.prev = cur
	s.prevWall = now

	if dRx < 0 || dTx < 0 {
		// Counter bounced (interface flap, NIC reset, kernel
		// reload). Reset rates rather than emit nonsense.
		s.lastRxBps, s.lastTxBps = 0, 0
		return 0, 0
	}

	s.lastRxBps = float64(dRx) / wall.Seconds()
	s.lastTxBps = float64(dTx) / wall.Seconds()
	return s.lastRxBps, s.lastTxBps
}

var bandwidthSamplerSingleton = &bandwidthSampler{}
