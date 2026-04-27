// Package api: in-memory ring buffer of chained AuditLogEntry per
// ADR 0006 D6 (offline buffering at the recorder).
//
// ADR 0006 D6 commits to a "30 days of typical audit volume" durable
// on-disk buffer at the recorder. The recorder doesn't yet ship the
// MS-pairing client and has no MS to forward to; in-memory buffering
// is the v1 surface the eventual MS-aggregation work will consume
// from. Disk persistence is follow-up work; the in-memory shape is
// the contract the rest of the recorder is wired against today.
//
// Capacity is 4096 entries by default — larger than EventStore's 1024
// because audit must survive longer offline windows even in the
// in-memory shape (a recorder LAN-isolated for an afternoon should
// still buffer admin actions without dropping any). The eventual
// disk-backed buffer will cap at 30-days-of-volume and tier into the
// in-memory ring for hot reads.
//
// Degraded-mode threshold per ADR 0006 D6: when len >= 80% of
// capacity, IsDegraded() returns true. Admin-action handlers (camera
// CRUD, recording-policy CRUD, recorder-config PATCH, etc.) consult
// this and reject new operations with 503; recording, playback,
// snapshot, and other media-flow operations are NEVER gated.
//
// Eviction posture: oldest-first eviction at full capacity. The
// in-memory buffer is the soft ceiling; the eventual disk-backed
// store handles the hard-stop semantics ADR 0006 D6 commits to.
// Eviction in the in-memory buffer does NOT break the chain — the
// chain head (prev_hash sequence) is held in AuditChain and is
// independent of buffer membership. Evicted entries are simply
// no-longer-available-for-read; they remain part of the cryptographic
// chain via their hash relationships.
package api //nolint:revive

import (
	"sync"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// defaultAuditBufferCapacity is the in-memory ring buffer's fixed
// capacity. 4096 is deliberately larger than EventStore (1024)
// because audit's at-least-once + chain-integrity contract makes drop
// rate more consequential.
const defaultAuditBufferCapacity = 4096

// auditDegradedThresholdNumerator / Denominator express the 80%
// high-water mark per ADR 0006 D6 as integer arithmetic
// (numerator/denominator = 4/5 = 80%).
const (
	auditDegradedThresholdNumerator   = 4
	auditDegradedThresholdDenominator = 5
)

// auditSink is the abstraction the AuditChain producer writes
// through and the read-side handlers (audit_v1_audit.go,
// audit_gate.go) consult. Both the in-memory AuditBuffer and the
// disk-backed AuditDiskBuffer satisfy it; the chain machinery is
// agnostic to which implementation is wired in. Production callers
// install the disk-backed implementation per ADR 0006 D6; tests
// continue to drive the in-memory ring.
type auditSink interface {
	Append(entry defs.AuditLogEntry)
	Snapshot() []defs.AuditLogEntry
	GetByID(id string) (defs.AuditLogEntry, bool)
	Len() int
	Capacity() int
	IsDegraded() bool
	Clear()
}

// AuditBuffer is the recorder's in-memory ring buffer of chained
// AuditLogEntry. Newest-last; oldest-first eviction at capacity.
//
// All exported methods are safe for concurrent use.
type AuditBuffer struct {
	mu       sync.RWMutex
	capacity int
	items    []defs.AuditLogEntry
}

// NewAuditBuffer constructs a buffer with the given capacity. <= 0
// falls back to the default.
func NewAuditBuffer(capacity int) *AuditBuffer {
	if capacity <= 0 {
		capacity = defaultAuditBufferCapacity
	}
	return &AuditBuffer{
		capacity: capacity,
		items:    make([]defs.AuditLogEntry, 0, capacity),
	}
}

// Append adds an already-chained entry to the buffer. Producers
// should go through AuditChain.Append rather than calling this
// directly — Append assumes EntryHash is set and PrevHash links to
// the prior entry.
func (b *AuditBuffer) Append(entry defs.AuditLogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) >= b.capacity {
		copy(b.items, b.items[1:])
		b.items = b.items[:len(b.items)-1]
	}
	b.items = append(b.items, entry)
}

// Snapshot returns a copy of the buffer's current contents in oldest-
// first order. Callers may filter, sort, and paginate the snapshot
// without holding the buffer's lock.
func (b *AuditBuffer) Snapshot() []defs.AuditLogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]defs.AuditLogEntry, len(b.items))
	copy(out, b.items)
	return out
}

// GetByID returns the entry with the given id, or false if absent.
func (b *AuditBuffer) GetByID(id string) (defs.AuditLogEntry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i := range b.items {
		if b.items[i].ID == id {
			return b.items[i], true
		}
	}
	return defs.AuditLogEntry{}, false
}

// Len returns the current number of buffered entries.
func (b *AuditBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.items)
}

// Capacity returns the buffer's fixed capacity.
func (b *AuditBuffer) Capacity() int {
	return b.capacity
}

// IsDegraded reports whether the buffer is at or above the 80%
// high-water threshold per ADR 0006 D6. Admin-action handlers
// consult this and reject new operations with 503; recording and
// other media-flow operations never consult it.
func (b *AuditBuffer) IsDegraded() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	// len >= cap * 4/5 ; integer-arithmetic form avoids float drift.
	return len(b.items)*auditDegradedThresholdDenominator >=
		b.capacity*auditDegradedThresholdNumerator
}

// Clear empties the buffer. Useful in tests.
func (b *AuditBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = b.items[:0]
}

// auditBufferSingleton is the default process-wide buffer used when
// no caller has injected one. The chain singleton (audit_emit.go)
// shares this buffer.
//
// auditSinkSingleton is the active sink the chain producer and the
// read-side handlers consult. By default it is the in-memory
// AuditBuffer; production wiring (api.API.Initialize, when configured
// with an audit-disk directory) calls installAuditDiskSink to swap it
// for the durable AuditDiskBuffer per ADR 0006 D6.
var (
	auditBufferSingletonOnce sync.Once
	auditBufferSingleton     *AuditBuffer

	auditSinkMu       sync.RWMutex
	auditSinkOverride auditSink
)

// defaultAuditBuffer returns the process-wide in-memory buffer,
// lazily constructed on first use. Tests interact directly with this
// (Clear, etc.); production read-paths should go through
// defaultAuditSink so they observe whichever sink is installed.
func defaultAuditBuffer() *AuditBuffer {
	auditBufferSingletonOnce.Do(func() {
		auditBufferSingleton = NewAuditBuffer(defaultAuditBufferCapacity)
	})
	return auditBufferSingleton
}

// defaultAuditSink returns the active audit sink. When a disk-backed
// buffer has been installed via installAuditDiskSink, that wins;
// otherwise the in-memory ring is returned. Read-side handlers
// (audit_v1_audit.go, audit_gate.go) and the chain producer go
// through this accessor so the swap is transparent.
func defaultAuditSink() auditSink {
	auditSinkMu.RLock()
	override := auditSinkOverride
	auditSinkMu.RUnlock()
	if override != nil {
		return override
	}
	return defaultAuditBuffer()
}

// installAuditDiskSink swaps the active sink to a disk-backed buffer
// and re-points the chain singleton at it, restoring the chain's
// prevHash from the disk-backed head per ADR 0006 D3 (chain
// continuity across restarts). Production callers invoke this once
// at startup after OpenAuditDiskBuffer succeeds; tests invoke it
// directly with a freshly-opened buffer.
//
// uninstallAuditDiskSink reverts to the in-memory ring; tests use
// this in t.Cleanup to avoid cross-test bleed.
func installAuditDiskSink(b *AuditDiskBuffer) {
	auditSinkMu.Lock()
	auditSinkOverride = b
	auditSinkMu.Unlock()

	// Re-anchor the chain singleton against the disk buffer's head.
	chain := defaultAuditChain()
	chain.replaceSink(b, b.HeadHashOnDisk())
}

// uninstallAuditDiskSink reverts to the in-memory ring. Used by
// tests. Production never calls this — once disk-backed is on, it
// stays on.
func uninstallAuditDiskSink() {
	auditSinkMu.Lock()
	auditSinkOverride = nil
	auditSinkMu.Unlock()

	chain := defaultAuditChain()
	chain.replaceSink(defaultAuditBuffer(), defs.ZeroPrevHash)
}
