// Package api: small helpers shared across Phase 5 handlers.
//
// emitMutationAudit is the convenience wrapper most Phase 5 handlers
// use to record a single mutation. Pulls actor info from the principal
// stashed by middlewareAuth + the client IP from the gin context.
package api //nolint:revive

import (
	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// emitMutationAudit records a mutation against the recorder's audit
// chain. action is one of the canonical action names (e.g.
// "camera.credentials_rotated"); resourceKind names the entity type
// ("camera", "notification_target", ...); attrs is per-action metadata
// the chain serializer encodes verbatim.
func (a *API) emitMutationAudit(ctx *gin.Context, action, resourceKind, resourceID string, attrs map[string]string) {
	principal := principalFromContext(ctx)
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    principal.PrincipalKind,
		ActorID:      principal.Sub,
		Action:       action,
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: resourceKind,
		ResourceID:   resourceID,
		SourceIP:     clientIP(ctx),
		Attributes:   attrs,
	})
}

// clientIP returns the gin context's resolved client IP (or empty).
// Tiny helper kept here so handler files don't repeat the nil-check.
func clientIP(ctx *gin.Context) string {
	if ctx == nil || ctx.Request == nil {
		return ""
	}
	return ctx.ClientIP()
}
