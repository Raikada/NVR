// Package api: package-level audit emission entry points per ADR
// 0006 D2 (per-tier emission) — the recorder is one emitter and the
// helpers here are the single seam between API/middleware/handler
// code and the chain machinery.
//
// Mirrors event_publish.go's shape (publishEvent / publishEventLocked
// + package-level pipeline helpers) so the muscle memory at the call
// site is identical.
//
// Helpers provided:
//   - emitAuditLocked / emitAudit: API-side appends from handlers and
//     middleware. *Locked variant is safe to call under a held a.mutex
//     (config-write handlers do this); the unlocked variant takes
//     a.mutex to read tenant id.
//   - emitConfigApplied: convenience for config-write handlers.
//   - emitAuthDecision: convenience for the auth middleware.
//   - emitPairingEvent: stub for when MS-pairing-client lands.
//   - emitBreakGlassActivated: stub for when break-glass paths land.
//
// The default chain target is process-wide (defaultAuditChain()) so
// tests can write into and read from the chain without explicit
// wiring; production callers that need per-API targeting can switch
// to an injected AuditChain.
package api //nolint:revive

import (
	"sync"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// auditChainSingleton is the package-wide AuditChain, lazily
// constructed. Mirrors the EventStore singleton model.
var (
	auditChainSingletonOnce sync.Once
	auditChainSingleton     *AuditChain
)

// defaultAuditChain returns the process-wide AuditChain, lazily
// constructed on first use. EmitterID starts empty; SetAuditEmitterID
// can replace it once the orchestrator surfaces the recorder's UUID
// (see api_v1_health.go::recordingServerID note for the same TBD).
func defaultAuditChain() *AuditChain {
	auditChainSingletonOnce.Do(func() {
		auditChainSingleton = NewAuditChain(
			defs.AuditEmitterKindRecordingServer,
			"",
			defaultAuditBuffer(),
		)
	})
	return auditChainSingleton
}

// emitAudit is the unlocked-caller variant: callers that do NOT hold
// a.mutex use this, e.g. the auth middleware. Acquires a.mutex
// through a.tenantID().
func (a *API) emitAudit(in defs.AuditLogEntryInput) {
	tenantID := ""
	if a != nil {
		tenantID = a.tenantID()
	}
	_, _ = defaultAuditChain().Append(in, tenantID, "")
}

// emitAuditLocked is for callers that already hold a.mutex (write or
// read). Reads a.Conf.TenantID directly without re-acquiring the
// mutex, matching publishEventLocked's pattern.
func (a *API) emitAuditLocked(in defs.AuditLogEntryInput) {
	tenantID := ""
	if a != nil && a.Conf != nil {
		tenantID = a.Conf.TenantID
	}
	_, _ = defaultAuditChain().Append(in, tenantID, "")
}

// emitConfigAppliedLocked is the convenience wrapper config-write
// handlers use alongside their existing publishEventLocked call. The
// caller holds a.mutex.
//
// resourceKind is one of the canonical entity kinds (camera,
// recording_policy, server) and resourceID is the canonical UUID
// (empty for resource-kind=server). The verb is folded into
// attributes alongside any handler-specific context.
func (a *API) emitConfigAppliedLocked(resourceKind, resourceID, verb string, extra map[string]string) {
	attrs := map[string]string{"verb": verb}
	for k, v := range extra {
		attrs[k] = v
	}
	a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindSystem,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: resourceKind,
		ResourceID:   resourceID,
		Attributes:   attrs,
	})
	// Note: ActorKind=system here because the recorder's auth surface
	// today resolves to a single privileged principal (no per-user
	// session model — ADR 0002 OQ10). When the session model lands,
	// the actor kind / id come from the resolved principal.
}

// emitAuthDecision is the convenience wrapper for the auth
// middleware. Outcome is success or failure; actor fields are best-
// effort (the recorder's auth manager today returns only a path/
// permission decision, not a structured principal).
func (a *API) emitAuthDecision(
	outcome defs.AuditOutcome,
	actorKind defs.AuditActorKind,
	actorID, sourceIP, clientFingerprint string,
	extra map[string]string,
) {
	if actorKind == "" {
		actorKind = defs.AuditActorKindUnauthenticated
	}
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:         actorKind,
		ActorID:           actorID,
		Action:            authActionForOutcome(outcome),
		Outcome:           outcome,
		ResourceKind:      "session",
		SourceIP:          sourceIP,
		ClientFingerprint: clientFingerprint,
		Attributes:        extra,
	})
}

func authActionForOutcome(o defs.AuditOutcome) string {
	if o == defs.AuditOutcomeSuccess {
		return "auth.login"
	}
	// failure and denied both surface as failed_login on the recorder
	// today; the distinct kinds materialize once the auth manager
	// distinguishes "wrong credentials" from "valid credentials,
	// denied permission" (ADR 0002 OQ10).
	return "auth.failed_login"
}

// emitPairingEvent is the entry point for pairing-related audit
// entries. The recorder doesn't implement pairing yet — the MS-
// pairing client surfaces in a later ADR — so this is a stub that
// real call sites will wire up when pairing lands.
//
// TODO when pairing lands: wire this into the MS-pairing-client
// state machine (token issued / consumed / rotated). The shape here
// is the contract the eventual call site will use.
func (a *API) emitPairingEvent(action string, outcome defs.AuditOutcome, attrs map[string]string) {
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindSystem,
		Action:       action, // pairing.token_issued, pairing.consumed, etc.
		Outcome:      outcome,
		ResourceKind: "pairing",
		Attributes:   attrs,
	})
}

// emitBreakGlassActivated is the entry point for break-glass audit
// entries. The recorder doesn't implement break-glass paths yet — the
// trust model documents them but the implementation surface is
// future work — so this is a stub.
//
// TODO when break-glass lands: wire this into the break-glass
// activation handler. The shape here is the contract the eventual
// call site will use; break-glass entries are particularly important
// to chain-integrity-protect because the action represents an
// authority escalation.
func (a *API) emitBreakGlassActivated(actorID string, attrs map[string]string) {
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindLocalUser,
		ActorID:      actorID,
		Action:       "breakglass.activated",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "server",
		Attributes:   attrs,
	})
}

// auditAdminBufferDegradedMessage is the response body string the
// admin-gating helper writes when the buffer is at the high-water
// threshold per ADR 0006 D6.
const auditAdminBufferDegradedMessage = "audit log near capacity; administrative actions paused per ADR 0006 D6"
