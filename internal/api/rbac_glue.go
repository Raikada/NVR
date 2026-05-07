// Package api: glue between the rbac package and the API's audit chain.
//
// rbac.RequirePerm needs an AuditEmitter on permission denial; this
// file adapts the API's emitAudit helper to that interface.
//
// rbac.Claims is a small read-only struct populated by middlewareAuth
// from the per-request Principal. Handlers prefer rbac.Claims for the
// new endpoints because it carries the high-level Role; the legacy
// principal_kind/scope plumbing remains for endpoints already wired
// against it.
package api //nolint:revive

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/rbac"
)

// rbacAuditEmitter wraps the API's emitAudit helper so it satisfies
// rbac.AuditEmitter (PermissionDenied(ctx, claims, action, ip)).
type rbacAuditEmitter struct {
	api *API
}

// PermissionDenied emits an auth.permission_denied audit row. Mirrors
// the same shape the legacy permission_gate.go emits so external
// dashboards see one pattern.
func (e rbacAuditEmitter) PermissionDenied(_ context.Context, claims rbac.Claims, action, ip string) {
	if e.api == nil {
		return
	}
	actor := defs.AuditActorKindLocalUser
	if claims.UserID == "" {
		actor = defs.AuditActorKindUnauthenticated
	}
	e.api.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor,
		ActorID:      claims.UserID,
		Action:       "auth.permission_denied",
		Outcome:      defs.AuditOutcomeDenied,
		ResourceKind: "session",
		SourceIP:     ip,
		Attributes: map[string]string{
			"endpoint": action,
			"username": claims.Username,
			"role":     string(claims.Role),
		},
	})
}

// auditEmitter returns the rbac.AuditEmitter wired against this API.
// Suitable for passing to rbac.RequirePerm.
func (a *API) auditEmitter() rbac.AuditEmitter {
	return rbacAuditEmitter{api: a}
}

// claimsFromPrincipal derives an rbac.Claims from the per-request
// Principal so middlewareAuth can stash it. The mapping is intentional:
//   - JWT principals carry IsAdmin via the legacy is_admin claim, which
//     we read from the principal's Scope (admin role implies the full
//     AdminPermissions set).
//   - Service-account principals (pre-OQ10 internal/HTTP auth) get
//     RoleAdmin so legacy admin tools continue to work; the
//     GlobalRBACEnforce flag preserves the existing strict/lax knob.
func claimsFromPrincipal(p *Principal) rbac.Claims {
	if p == nil {
		return rbac.Claims{}
	}
	role := rbac.RoleViewer
	if p.PrincipalKind == defs.AuditActorKindServiceAccount {
		// Pre-ADR-0011 internal/HTTP auth — treat as admin.
		role = rbac.RoleAdmin
	}
	// Heuristic: if the principal carries any ADR 0010 admin permission,
	// treat them as admin. The recorder's bootstrap admin always carries
	// the full admin permission set; viewer scopes are a strict subset.
	for _, s := range p.Scope {
		if s == "user.invite" || s == "user.manage" || s == "role.assign" || s == "role.manage" {
			role = rbac.RoleAdmin
			break
		}
	}
	return rbac.Claims{
		UserID:   p.Sub,
		Username: "", // Principal does not carry username today; downstream handlers fetch as needed.
		Role:     role,
	}
}
