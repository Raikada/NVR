// Package defs: canonical AuditLogEntry shape per ADR 0006.
//
// AuditLogEntry is a tamper-evident record of a security- or
// compliance-relevant action: authentication, authorization decisions,
// configuration changes, pairings, revocations, break-glass
// invocations. The recorder is one emitter; per ADR 0006 D2 the
// recorder authors entries for actions it observes locally
// (authentication at the recorder's API surface, configuration applies
// it accepts, break-glass invocations on the appliance, etc.).
//
// Entries form a per-emitter SHA-256 hash chain; see audit_translate.go
// for the canonical-JSON serializer and chain mechanics.
//
// All field JSON tags are snake_case per the canonical /v1 surface
// convention.
package defs

import (
	"time"
)

// AuditEmitterKind enumerates the emitter tiers per ADR 0006 D2.
type AuditEmitterKind string

// Audit emitter kinds.
const (
	AuditEmitterKindCloud            AuditEmitterKind = "cloud"
	AuditEmitterKindManagementServer AuditEmitterKind = "management_server"
	AuditEmitterKindRecordingServer  AuditEmitterKind = "recording_server"
)

// AuditActorKind enumerates the actor tiers per domain-model.md
// AuditLogEntry.actor_kind.
type AuditActorKind string

// Audit actor kinds.
const (
	AuditActorKindCloudUser       AuditActorKind = "cloud_user"
	AuditActorKindLocalUser       AuditActorKind = "local_user"
	AuditActorKindServiceAccount  AuditActorKind = "service_account"
	AuditActorKindSystem          AuditActorKind = "system"
	AuditActorKindUnauthenticated AuditActorKind = "unauthenticated"
)

// AuditOutcome enumerates audit outcomes per domain-model.md.
type AuditOutcome string

// Audit outcomes.
const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
	AuditOutcomeDenied  AuditOutcome = "denied"
)

// AuditLogEntry is the canonical AuditLogEntry entity per ADR 0006 and
// domain-model.md §AuditLogEntry. Field order is intentional: id and
// tenancy first, then timestamps, then emitter, then actor, then
// action/outcome/resource, then session/correlation context, then
// classification-managed network identifiers, then attributes, then
// chain-link fields. This is also the field order encoding/json uses
// for serialization, but chain canonicalization sorts keys
// lexicographically (see audit_translate.go), so struct field order
// affects only Go-side ergonomics.
type AuditLogEntry struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	SiteID   string `json:"site_id,omitempty"`

	OccurredAt time.Time `json:"occurred_at"`
	RecordedAt time.Time `json:"recorded_at"`

	EmitterKind AuditEmitterKind `json:"emitter_kind"`
	EmitterID   string           `json:"emitter_id"`

	ActorKind AuditActorKind `json:"actor_kind"`
	ActorID   string         `json:"actor_id,omitempty"`

	Action  string       `json:"action"`
	Outcome AuditOutcome `json:"outcome"`

	ResourceKind string `json:"resource_kind"`
	ResourceID   string `json:"resource_id,omitempty"`

	SessionID         string `json:"session_id,omitempty"`
	CorrelationID     string `json:"correlation_id,omitempty"`
	SourceIP          string `json:"source_ip,omitempty"`
	ClientFingerprint string `json:"client_fingerprint,omitempty"`

	Attributes map[string]string `json:"attributes,omitempty"`

	// PrevHash is the previous entry's EntryHash on this emitter's
	// chain, hex-encoded. Per ADR 0006 D3 the first entry in a chain
	// has PrevHash set to 32 zero bytes (64 hex zeros). PrevHash is
	// included in the canonical-JSON serialization that EntryHash
	// covers; see audit_translate.go.
	PrevHash string `json:"prev_hash"`

	// EntryHash is the SHA-256 of the canonical-JSON serialization of
	// this entry with EntryHash itself set to the empty string. Hex-
	// encoded.
	EntryHash string `json:"entry_hash"`
}

// AuditLogEntryInput is the recorder's "I want to write an audit
// entry" payload. The chain machinery (audit_chain.go) takes an Input,
// stamps Recorded-At, EmitterKind/EmitterID, ID, computes PrevHash and
// EntryHash, and produces the chained AuditLogEntry.
type AuditLogEntryInput struct {
	OccurredAt time.Time

	ActorKind AuditActorKind
	ActorID   string

	Action  string
	Outcome AuditOutcome

	ResourceKind string
	ResourceID   string

	SessionID         string
	CorrelationID     string
	SourceIP          string
	ClientFingerprint string

	Attributes map[string]string
}

// ZeroPrevHash is the chain-origin sentinel per ADR 0006 D3: 32 zero
// bytes hex-encoded.
const ZeroPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"
