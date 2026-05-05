// Package api: slice 4-B Camera canonical-authority lockdown gate per
// ADR 0016 D5. When the recorder's canonical_source is `ms` (per its
// identity flag), POST / PATCH / PUT / DELETE /v1/cameras* requests
// must come from the MS service principal (JWT with principal_kind =
// service_account + scope contains camera.push) — otherwise they are
// rejected with `camera_canonical_source_is_ms`.
//
// A local-admin diagnostic break-glass path bypasses the gate when the
// caller sends `X-Local-Admin-Override: true` AND the principal is
// local-admin-authoritative (today: any non-service-account whose
// IsServiceAccount() is false; once the role catalog is fully wired,
// gate on a `recorder.local_admin_override` permission). The override
// emits `camera.local_override` audit at high severity per ADR 0016 D7.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/camerasync"
	"github.com/bluenviron/mediamtx/internal/defs"
)

const (
	// HeaderLocalAdminOverride is the request header that triggers the
	// break-glass path. Set on the request side; the recorder verifies
	// it alongside an admin-tier principal. When set under a
	// service-account principal it is ignored (the principal-kind check
	// already passes).
	HeaderLocalAdminOverride = "X-Local-Admin-Override"

	// ErrCodeCameraCanonicalSourceIsMS is the stable error code returned
	// when a non-MS-sourced mutation hits a locked-down recorder.
	ErrCodeCameraCanonicalSourceIsMS = "camera_canonical_source_is_ms"
)

// applyCameraLockdown checks the slice 4-B lockdown gate on the request.
// Returns true when the caller may proceed with the mutation; false +
// 403 written when the recorder is locked down and the principal isn't
// admitted.
//
// On break-glass admission, emits the `camera.local_override` audit
// entry before returning true so the caller's regular
// config.applied audit forms the second half of the per-action chain.
func (a *API) applyCameraLockdown(ctx *gin.Context, cameraID, verb string) bool {
	principal := principalFromContext(ctx)
	headerVal := ctx.GetHeader(HeaderLocalAdminOverride)
	override := headerVal == "true" && !principal.IsServiceAccount()

	decision := camerasync.Gate(a.Identity, principal, override)
	switch decision {
	case camerasync.DecisionAllow:
		return true
	case camerasync.DecisionAllowBreakglass:
		actorID := principal.Sub
		// Audit: high-severity break-glass override. Emit BEFORE the
		// mutation so the chain reflects the operator's intent even if
		// the mutation itself fails downstream.
		//
		// The recorder's AuditLogEntryInput does not carry a Severity
		// field today; the camera-canonical-push.md §9 contract calls
		// for "critical" severity here. We carry it in attributes so
		// reviewers can filter / surface it; future audit-chain work
		// can promote it to a first-class field.
		a.emitAuditLocked(defs.AuditLogEntryInput{
			ActorKind:    principal.PrincipalKind,
			ActorID:      actorID,
			Action:       "camera.local_override",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "camera",
			ResourceID:   cameraID,
			Attributes: map[string]string{
				"camera_id": cameraID,
				"verb":      verb,
				"reason":    "operator-supplied X-Local-Admin-Override header on locked-down recorder",
				"severity":  string(defs.EventSeverityCritical),
			},
		})
		return true
	default:
		a.writeErrorWithCode(ctx, http.StatusForbidden, ErrCodeCameraCanonicalSourceIsMS,
			"this recorder's Camera authority is the Management Server")
		return false
	}
}

// writeErrorWithCode writes the recorder's standard error envelope plus
// a code field. The recorder's APIError shape today carries `error`
// (string) but not `code`; we encode both as a structured response so
// the camera-canonical-push.md §8 catalog stays honored even before
// the recorder migrates to the contract's full error envelope.
func (a *API) writeErrorWithCode(ctx *gin.Context, status int, code, msg string) {
	ctx.AbortWithStatusJSON(status, map[string]any{
		"status": "error",
		"error":  msg,
		"code":   code,
	})
}

// engageLockdownIfMSSourced flips the recorder's canonical_source to
// `ms` (one-way) when the just-completed mutation was sourced from the
// MS service principal AND the recorder hasn't already engaged the
// lockdown. Called from camera-mutation handlers post-success.
//
// Idempotent. Failures to persist are logged but do not roll back the
// mutation — the next successful MS mutation retries the engagement.
func (a *API) engageLockdownIfMSSourced(ctx *gin.Context) {
	if a.Identity == nil {
		return
	}
	principal := principalFromContext(ctx)
	if !principal.IsServiceAccount() || !principal.HasPermission("camera.push") {
		return
	}
	if err := camerasync.EngageLockdown(a.Identity); err != nil {
		// Non-fatal: log via the standard error path. The next
		// MS-sourced mutation re-attempts.
		ctx.Set("camera_lockdown_engage_error", err)
	}
}
