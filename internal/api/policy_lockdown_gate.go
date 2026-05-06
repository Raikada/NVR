// Package api: slice 4-C RecordingPolicy canonical-authority lockdown
// gate per ADR 0017 D5. When the recorder's policy_canonical_source is
// `ms` (per its identity flag), POST / PATCH / DELETE
// /v1/recording-policies* requests must come from the MS service
// principal (JWT with principal_kind = service_account + scope
// recording_policy.push) — otherwise they are rejected with
// `recording_policy_canonical_source_is_ms`.
//
// Mirrors the slice-4-B camera_lockdown_gate.go shape; the only
// differences are the scope claim checked (`recording_policy.push`
// instead of `camera.push`) and the error code returned.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/policysync"
)

const (
	// ErrCodeRecordingPolicyCanonicalSourceIsMS is the stable error
	// code returned when a non-MS-sourced mutation hits a locked-down
	// recorder for RecordingPolicy operations.
	ErrCodeRecordingPolicyCanonicalSourceIsMS = "recording_policy_canonical_source_is_ms"
)

// applyPolicyLockdown checks the slice 4-C lockdown gate on the
// request. Returns true when the caller may proceed with the
// mutation; false + 403 written when the recorder is locked down and
// the principal isn't admitted.
//
// On break-glass admission, emits the
// `recording_policy.local_override` audit entry before returning true
// so the chain reflects the operator's intent even if the mutation
// itself fails downstream.
func (a *API) applyPolicyLockdown(ctx *gin.Context, policyID, verb string) bool {
	principal := principalFromContext(ctx)
	headerVal := ctx.GetHeader(HeaderLocalAdminOverride)
	override := headerVal == "true" && !principal.IsServiceAccount()

	decision := policysync.Gate(a.Identity, principal, override)
	switch decision {
	case policysync.DecisionAllow:
		return true
	case policysync.DecisionAllowBreakglass:
		actorID := principal.Sub
		a.emitAuditLocked(defs.AuditLogEntryInput{
			ActorKind:    principal.PrincipalKind,
			ActorID:      actorID,
			Action:       "recording_policy.local_override",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "recording_policy",
			ResourceID:   policyID,
			Attributes: map[string]string{
				"policy_id": policyID,
				"verb":      verb,
				"reason":    "operator-supplied X-Local-Admin-Override header on locked-down recorder",
				"severity":  string(defs.EventSeverityCritical),
			},
		})
		return true
	default:
		a.writeErrorWithCode(ctx, http.StatusForbidden, ErrCodeRecordingPolicyCanonicalSourceIsMS,
			"this recorder's RecordingPolicy authority is the Management Server")
		return false
	}
}

// engagePolicyLockdownIfMSSourced flips the recorder's
// policy_canonical_source to `ms` (one-way) when the just-completed
// mutation was sourced from the MS service principal AND the
// lockdown isn't already engaged. Idempotent.
func (a *API) engagePolicyLockdownIfMSSourced(ctx *gin.Context) {
	if a.Identity == nil {
		return
	}
	principal := principalFromContext(ctx)
	if !principal.IsServiceAccount() || !principal.HasPermission("recording_policy.push") {
		return
	}
	if err := policysync.EngageLockdown(a.Identity); err != nil {
		ctx.Set("recording_policy_lockdown_engage_error", err)
	}
}
