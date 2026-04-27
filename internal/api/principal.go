package api

import (
	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
)

// principalContextKey is the gin.Context key under which the per-request
// Principal is stored by middlewareAuth and read by handlers via
// principalFromContext. Keep this string stable: handlers in other
// files key off it directly via gin.Context.Get.
const principalContextKey = "raikada.principal"

// Principal is the authenticated caller of an API request, mirroring
// the ADR 0011 D2 JWT claim shape. The middleware extracts a Principal
// from the auth manager's parsed claims and stashes it on the
// gin.Context; handlers read it back via principalFromContext.
//
// This commit only wires the principal-on-context plumbing. Handlers
// do not yet gate behavior on Scope or ScopeKind / ScopeTargetID; that
// follows in the PII-unmasking commit.
type Principal struct {
	// Sub is the canonical User.id (a UUID) per ADR 0011 D2's `sub`.
	// Empty for unauthenticated and for service-account principals
	// produced by the pre-OQ10 internal/HTTP auth paths (which carry
	// no user identity).
	Sub string

	// TenantID is the tenant the token is scoped to per ADR 0011 D2's
	// `tenant_id`. Required on JWT-auth'd requests under ADR 0011;
	// empty when the token predates ADR 0011 or the request authed
	// via the pre-OQ10 internal/HTTP path.
	TenantID string

	// PrincipalKind is one of defs.AuditActorKind* — derived from the
	// token's `principal_kind` claim, or AuditActorKindServiceAccount
	// for the pre-OQ10 internal/HTTP path, or AuditActorKindUnauthenticated
	// for paths reached without auth.
	PrincipalKind defs.AuditActorKind

	// Scope is the granted permission strings per ADR 0010, taken
	// verbatim from the token's `scope` claim. Empty for principals
	// produced by the pre-OQ10 paths (which are privileged-by-default
	// at the API layer until ADR 0011 token-issuance lands end-to-end).
	Scope []string

	// ScopeKind is one of "tenant" / "organization" / "site" /
	// "recording_server" per ADR 0011 D2 — the scope-target tier the
	// scope applies at.
	ScopeKind string

	// ScopeTargetID is the UUID of the scope target.
	ScopeTargetID string

	// ClientFingerprint is the opaque token-binding fingerprint per
	// ADR 0011 D2's `client_fingerprint`. Surfaced for audit
	// correlation; not currently validated against the connection.
	ClientFingerprint string

	// RawJTI is the JWT's `jti` claim, surfaced for audit correlation
	// (per ADR 0006: distinct request-level audit entries that share a
	// jti collapse to the same logical session).
	RawJTI string
}

// HasPermission reports whether the principal's Scope grants the
// given permission string. Comparison is case-sensitive byte equality
// against ADR 0010's role-catalog strings.
//
// A principal produced by the pre-OQ10 internal/HTTP auth path has an
// empty Scope and therefore HasPermission returns false for every
// permission string. The API middleware does not gate handlers on
// HasPermission today; downstream commits will add that gating, with a
// migration plan for pre-OQ10-authed deployments.
func (p *Principal) HasPermission(perm string) bool {
	if p == nil {
		return false
	}
	for _, s := range p.Scope {
		if s == perm {
			return true
		}
	}
	return false
}

// HasAnyPermission reports whether the principal holds at least one of
// the given permissions. Useful for handlers that accept either a
// narrow "read-own" permission or a broader "read-all" permission.
func (p *Principal) HasAnyPermission(perms ...string) bool {
	if p == nil {
		return false
	}
	for _, perm := range perms {
		if p.HasPermission(perm) {
			return true
		}
	}
	return false
}

// IsServiceAccount reports whether the principal is a service account
// — either a real ADR 0011 token with `principal_kind=service_account`
// or a principal produced by the pre-OQ10 internal/HTTP auth path
// (which is treated as service-account at the audit layer per
// existing api.go middlewareAuth behavior).
func (p *Principal) IsServiceAccount() bool {
	if p == nil {
		return false
	}
	return p.PrincipalKind == defs.AuditActorKindServiceAccount
}

// principalFromContext returns the Principal previously stashed on the
// gin.Context by middlewareAuth. Returns the unauthenticated principal
// (never nil) if no Principal is set — defensive against handlers
// reached on routes that bypass the middleware.
func principalFromContext(ctx *gin.Context) *Principal {
	v, ok := ctx.Get(principalContextKey)
	if !ok {
		return unauthenticatedPrincipal()
	}
	p, ok := v.(*Principal)
	if !ok || p == nil {
		return unauthenticatedPrincipal()
	}
	return p
}

// setPrincipalOnContext stashes the Principal on the gin.Context for
// later retrieval via principalFromContext. Called once per request
// from middlewareAuth.
func setPrincipalOnContext(ctx *gin.Context, p *Principal) {
	ctx.Set(principalContextKey, p)
}

// unauthenticatedPrincipal is the principal used for paths reached
// without authentication (e.g., the explicitly-anonymous health
// endpoint when configured open). Empty Scope means HasPermission
// returns false for everything.
func unauthenticatedPrincipal() *Principal {
	return &Principal{
		PrincipalKind: defs.AuditActorKindUnauthenticated,
	}
}

// PermSessionPIIRead is the ADR 0010 D2 permission string that gates
// unmasking of Stream-level PII fields (Stream.remote_addr,
// transport_connections[].remote_addr, WebRTC ICE candidate IPs).
// Closes recorder canonical-divergence D5.
const PermSessionPIIRead = "session.pii.read"

// principalFromAuthClaims builds a Principal from the auth manager's
// parsed Claims. The Internal and HTTP auth paths produce a zero-value
// Claims with Method set; those map to a service-account principal
// with empty Scope (the recorder's privileged-by-default pre-OQ10
// posture). The JWT path produces a real ADR 0011 D2 principal.
//
// globalPIIReadGrant, when true, injects the ADR 0010 `session.pii.read`
// permission into the service-account principal produced by the
// internal/HTTP auth paths. JWT-authed requests carry their own scope
// claim and ignore the flag. Per conf.Conf.GlobalPIIReadGrant; default
// false.
func principalFromAuthClaims(claims auth.Claims, globalPIIReadGrant bool) *Principal {
	if claims.Method != conf.AuthMethodJWT {
		// Pre-OQ10 internal/HTTP auth: no per-user identity, no scope.
		// Treat as service account at the API layer; the audit-emit
		// call in middlewareAuth already records actor_kind=service_account
		// for this path. PII unmasking and scope-gated handlers won't
		// match an empty Scope — deployments still on pre-OQ10 auth
		// will need to migrate to JWT-with-scope before scope gating
		// becomes mandatory.
		//
		// Operator escape hatch: if globalPIIReadGrant is set on the
		// bootstrap config, inject session.pii.read into the static
		// principal's scope so legacy admin UIs still see Stream PII.
		// Default false; secure posture is fail-closed.
		var scope []string
		if globalPIIReadGrant {
			scope = []string{PermSessionPIIRead}
		}
		return &Principal{
			PrincipalKind: defs.AuditActorKindServiceAccount,
			Scope:         scope,
		}
	}

	kind := principalKindFromClaim(claims.PrincipalKind)
	return &Principal{
		Sub:               claims.Subject,
		TenantID:          claims.TenantID,
		PrincipalKind:     kind,
		Scope:             claims.Scope,
		ScopeKind:         claims.ScopeKind,
		ScopeTargetID:     claims.ScopeTargetID,
		ClientFingerprint: claims.ClientFingerprint,
		RawJTI:            claims.JTI,
	}
}

// principalKindFromClaim maps the ADR 0011 D2 `principal_kind` string
// claim to a defs.AuditActorKind. Unknown values map to
// AuditActorKindUnauthenticated — defensive: an issuer producing a
// principal_kind value the recorder doesn't recognize is a config
// drift the recorder should surface as a non-actor.
func principalKindFromClaim(s string) defs.AuditActorKind {
	switch s {
	case "cloud_user":
		return defs.AuditActorKindCloudUser
	case "local_user":
		return defs.AuditActorKindLocalUser
	case "service_account":
		return defs.AuditActorKindServiceAccount
	case "system":
		return defs.AuditActorKindSystem
	default:
		return defs.AuditActorKindUnauthenticated
	}
}
