// Package api: per-endpoint permission gate per ADR 0010's role
// catalog and ADR 0011 D2's JWT scope claim.
//
// requirePermission is the middleware applied per-route in the API's
// route registration; each /v1/* route names the ADR 0010 permission
// it requires. Operating in concert with:
//
//   - middlewareAuth (api.go) which produces the per-request *Principal
//     from the auth.Manager's parsed Claims and stashes it on
//     gin.Context.
//   - guardAdminAction (audit_gate.go) which enforces the ADR 0006 D6
//     buffer-degraded gate for administrative actions; orthogonal to
//     RBAC and applied alongside.
//
// ADR 0010 D2 strings are case-sensitive byte-equality matched against
// principal.Scope per HasPermission. Denials emit auth.permission_denied
// per ADR 0006 D7 (carrying endpoint, method, required permission as
// attributes) and return 403 with the recorder's standard structured
// error envelope.
//
// Pre-OQ10 service-account compatibility: deployments that haven't yet
// migrated to JWT-with-scope still authenticate via the internal/HTTP
// methods. Those produce a service-account *Principal with empty
// Scope, which would otherwise fail every HasPermission check. The
// conf.Conf.GlobalRBACEnforce flag (default false) lets operators
// preserve legacy admin-UI access during the migration window:
//
//   - GlobalRBACEnforce=false (default): service-account principals
//     bypass the gate. JWT-authed principals always enforce scope
//     against their `scope` claim.
//   - GlobalRBACEnforce=true: every principal gates on scope. Service-
//     account principals from the legacy auth methods now fail-closed
//     unless their (currently-empty) Scope is populated by a future
//     auth.Manager refinement.
package api //nolint:revive

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// requirePermission returns a gin middleware that enforces the named
// ADR 0010 permission against the per-request *Principal stashed by
// middlewareAuth. On grant, the request continues; on denial, the
// middleware emits an auth.permission_denied audit entry and writes a
// 403 with a structured error body — the request short-circuits.
//
// Apply this per-route in the registration table (see api.go around
// the /v1 group). guardAdminAction (when applicable) is applied
// alongside as a separate concern; both must pass.
func (a *API) requirePermission(perm string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		principal := principalFromContext(ctx)

		// Pre-OQ10 compatibility: a service-account principal produced
		// by the internal/HTTP auth path has an empty Scope. When
		// GlobalRBACEnforce is false (default), the recorder treats
		// those principals as having every permission so legacy admin
		// UIs continue to work during the migration window.
		//
		// JWT-authed principals always gate on scope regardless of the
		// flag — a JWT principal's scope claim is the source of truth
		// per ADR 0011 D2.
		if principal.IsServiceAccount() && !a.globalRBACEnforce() && len(principal.Scope) == 0 {
			ctx.Next()
			return
		}

		if principal.HasPermission(perm) {
			ctx.Next()
			return
		}

		// Denial path: emit auth.permission_denied per ADR 0006 D7
		// (severity is encoded in attributes since defs.AuditLogEntryInput
		// doesn't carry a first-class Severity field — same approach as
		// camera_lockdown_gate.go) and return 403.
		actorKind := principal.PrincipalKind
		if actorKind == "" {
			actorKind = defs.AuditActorKindUnauthenticated
		}
		a.emitAudit(defs.AuditLogEntryInput{
			ActorKind:    actorKind,
			ActorID:      principal.Sub,
			Action:       "auth.permission_denied",
			Outcome:      defs.AuditOutcomeDenied,
			ResourceKind: "session",
			Attributes: map[string]string{
				"endpoint":            ctx.FullPath(),
				"method":              ctx.Request.Method,
				"required_permission": perm,
				"severity":            string(defs.EventSeverityInfo),
			},
		})

		ctx.AbortWithStatusJSON(http.StatusForbidden, &defs.APIError{
			Status: defs.APIErrorStatusError,
			Error:  "permission denied: " + perm,
		})
	}
}
