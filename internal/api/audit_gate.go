// Package api: admin-action gating per ADR 0006 D6.
//
// When the audit buffer crosses the 80% high-water threshold the
// recorder enters degraded mode: new administrative actions are
// refused with a clear error. Recording, LAN playback for already-
// authenticated users, segment writing, and other media-flow
// operations are NEVER gated — those are the actions whose value lies
// in not stopping per ADR 0006 D6 and system-blueprint.md §3.1.
//
// The gate writes a 503 with a structured error body the operator
// can recognize. The handler uses ctx.IsAborted() / returns false to
// short-circuit further processing.
package api //nolint:revive

import (
	"net/http"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/gin-gonic/gin"
)

// guardAdminAction returns true when the action may proceed. When the
// audit buffer is in degraded mode, it writes a 503 + structured
// error body and returns false; the caller should return immediately.
//
// Per ADR 0006 D6: gating applies only to administrative actions —
// camera CRUD, recording-policy CRUD, recorder-config PATCH, etc.
// Recording, /v1/streams DELETE, /v1/clips operations, /v1/recordings
// playback, and /v1/recorder/cameras/:id/snapshot are NOT gated.
func (a *API) guardAdminAction(ctx *gin.Context) bool {
	if !defaultAuditSink().IsDegraded() {
		return true
	}
	ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, &defs.APIError{
		Status: defs.APIErrorStatusError,
		Error:  auditAdminBufferDegradedMessage,
	})
	return false
}
