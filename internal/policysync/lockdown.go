// Package policysync owns the recorder side of slice 4-C
// RecordingPolicy canonical-authority migration per ADR 0017. Mirrors
// the slice-4-B internal/camerasync/ shape: a small lockdown gate, a
// poll loop that fetches MS desired-state, and an apply layer that
// reconciles MS-issued versions against the recorder's local cache.
//
// The lockdown gate (this file) is deliberately small and
// side-effect-free: it reads the identity's policy-canonical-source
// flag and the request's principal kind / scope claims to decide
// whether a mutation may proceed. The caller (the policy-mutation
// handler) handles audit emission and the actual mutation.
package policysync

import (
	"github.com/bluenviron/mediamtx/internal/identity"
)

// LockdownDecision describes the outcome of the gate check.
type LockdownDecision int

// Lockdown decisions.
const (
	// DecisionAllow lets the mutation proceed without further gate
	// checks.
	DecisionAllow LockdownDecision = iota
	// DecisionRejectMSCanonical means the recorder is locked down (per
	// ADR 0017 D5) and the request is not from the MS service principal.
	// Caller returns 403 + recording_policy_canonical_source_is_ms.
	DecisionRejectMSCanonical
	// DecisionAllowBreakglass lets the mutation proceed because the
	// caller used the local-admin diagnostic break-glass path. Caller
	// emits recording_policy.local_override audit at high severity per
	// ADR 0017 D7 in addition to the regular config.applied entry.
	DecisionAllowBreakglass
)

// PrincipalLike is the smallest interface the lockdown gate needs from
// the caller's auth principal. Tests inject a fake; the api package's
// *Principal type satisfies it.
type PrincipalLike interface {
	IsServiceAccount() bool
	HasPermission(perm string) bool
}

// Gate decides whether a RecordingPolicy mutation may proceed against
// the local recorder.
//
//   - id is the recorder's identity; policy_canonical_source = ms is
//     the lockdown trigger. nil identity (test harnesses) means the
//     gate defaults to recorder-canonical (no lockdown) — caller
//     decides if this is acceptable.
//   - principal is the auth principal extracted by the JWT middleware
//     (carries scope + principal_kind from the JWT claims).
//   - localAdminOverride is true when the caller set the
//     X-Local-Admin-Override header AND has admin authority.
func Gate(id *identity.Identity, principal PrincipalLike, localAdminOverride bool) LockdownDecision {
	if id == nil {
		return DecisionAllow
	}
	if id.PolicyCanonicalSource() != identity.CanonicalSourceMS {
		// Pre-import recorder: behave as before slice 4-C.
		return DecisionAllow
	}
	// Locked down. The MS service principal is the only auth-source that
	// passes; otherwise the caller must use the break-glass header.
	if principal != nil && principal.IsServiceAccount() && principal.HasPermission("recording_policy.push") {
		return DecisionAllow
	}
	if localAdminOverride {
		return DecisionAllowBreakglass
	}
	return DecisionRejectMSCanonical
}

// EngageLockdown flips the recorder's identity to
// policy_canonical_source = ms (one-way). Called by the
// policy-mutation handler when it sees its first successful MS-source
// push, OR by the poll-reconcile loop when the MS poll endpoint
// reports a non-zero version_set.
//
// Idempotent on a recorder that's already MS-canonical for policies.
func EngageLockdown(id *identity.Identity) error {
	if id == nil {
		return nil
	}
	return id.SetPolicyCanonicalSource(identity.CanonicalSourceMS)
}
