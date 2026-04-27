// Package api: per-emitter AuditLogEntry chain machinery for the
// recorder per ADR 0006 D3 (hash-chain mechanics) and D4 (per-tenant
// chain boundary).
//
// The recorder is one emitter (emitter_kind=recording_server,
// emitter_id=<recorder UUID>). Per ADR 0005 a recorder is bound to
// exactly one tenant per appliance; consequently the recorder
// maintains a single chain in-process — there is no per-tenant chain
// split inside the recorder. Tenant id is stamped on every entry from
// the recorder's bound conf.Conf.TenantID; the chain itself is one
// linear sequence.
//
// AuditChain.Append is the single entry point producers (config-write
// handlers, auth middleware, future pairing/break-glass call sites)
// use to add an entry to the chain. Append:
//   - stamps EmitterKind, EmitterID, ID, OccurredAt (if zero), RecordedAt
//   - reads PrevHash from the chain head (or ZeroPrevHash on first entry)
//   - computes EntryHash over the canonical-JSON serialization
//   - hands the chained entry to the buffer
//   - updates the chain head
//
// Mutex-guarded; safe for concurrent producers.
package api //nolint:revive

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// AuditChain holds the prev-hash head plus the buffer entries chain
// into. The recorder constructs one AuditChain at startup and shares
// it through the package-level audit target (see audit_emit.go).
type AuditChain struct {
	mu sync.Mutex

	emitterKind defs.AuditEmitterKind
	emitterID   string

	prevHash string

	buffer auditSink
}

// NewAuditChain builds an AuditChain for an emitter. emitterID is the
// recorder's UUID; for the recorder default this is whatever the
// orchestrator surfaces as the recording-server identifier (TBD per
// ADR 0009 — see api_v1_health.go::recordingServerID note). buffer is
// the bounded ring buffer the chain appends into.
func NewAuditChain(emitterKind defs.AuditEmitterKind, emitterID string, buffer *AuditBuffer) *AuditChain {
	return &AuditChain{
		emitterKind: emitterKind,
		emitterID:   emitterID,
		prevHash:    defs.ZeroPrevHash,
		buffer:      buffer,
	}
}

// replaceSink swaps the chain's underlying sink and restores its
// prevHash from the new sink's recovered head per ADR 0006 D3. Used
// by installAuditDiskSink to anchor a recorder restart's chain head
// to whatever the disk-backed buffer recovered. The new prevHash is
// supplied by the caller (typically AuditDiskBuffer.HeadHashOnDisk);
// passing defs.ZeroPrevHash resets the chain to a virgin origin and
// is appropriate only on uninstall (tests).
func (c *AuditChain) replaceSink(buffer auditSink, prevHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buffer = buffer
	c.prevHash = prevHash
}

// Append takes an input, stamps emitter/timestamps/id, computes the
// chain link, appends the chained entry into the buffer, and returns
// the chained entry. Returns an error only if canonical-JSON
// serialization fails — a hard internal bug, not a runtime condition
// callers can recover from.
//
// tenantID is supplied by the caller (the API has the recorder's
// configured tenant id readily available); the chain itself is
// tenant-agnostic per ADR 0005 (single-tenant recorder), but every
// entry still carries tenant_id per ADR 0001 R3 (tenancy is
// denormalized).
func (c *AuditChain) Append(in defs.AuditLogEntryInput, tenantID, siteID string) (defs.AuditLogEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UTC()
	occurred := in.OccurredAt
	if occurred.IsZero() {
		occurred = now
	}

	entry := defs.AuditLogEntry{
		ID:                uuid.New().String(),
		TenantID:          tenantID,
		SiteID:            siteID,
		OccurredAt:        occurred,
		RecordedAt:        now,
		EmitterKind:       c.emitterKind,
		EmitterID:         c.emitterID,
		ActorKind:         in.ActorKind,
		ActorID:           in.ActorID,
		Action:            in.Action,
		Outcome:           in.Outcome,
		ResourceKind:      in.ResourceKind,
		ResourceID:        in.ResourceID,
		SessionID:         in.SessionID,
		CorrelationID:     in.CorrelationID,
		SourceIP:          in.SourceIP,
		ClientFingerprint: in.ClientFingerprint,
		Attributes:        in.Attributes,
		PrevHash:          c.prevHash,
	}

	hash, err := defs.ComputeAuditEntryHash(entry)
	if err != nil {
		return defs.AuditLogEntry{}, err
	}
	entry.EntryHash = hash

	if c.buffer != nil {
		c.buffer.Append(entry)
	}
	c.prevHash = hash

	return entry, nil
}

// HeadHash returns the current chain head's EntryHash, or the zero
// sentinel if no entries have been appended yet. Useful for tests and
// for verification at chain boundaries.
func (c *AuditChain) HeadHash() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prevHash
}

// EmitterID returns the chain's emitter id.
func (c *AuditChain) EmitterID() string {
	return c.emitterID
}

// EmitterKind returns the chain's emitter kind.
func (c *AuditChain) EmitterKind() defs.AuditEmitterKind {
	return c.emitterKind
}

// resetForTests clears the chain head back to the zero sentinel.
// Tests use this in concert with AuditBuffer.Clear to start each
// test from a clean chain state.
func (c *AuditChain) resetForTests() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prevHash = defs.ZeroPrevHash
}
