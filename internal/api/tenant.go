package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// tenantID is a vestigial accessor preserved for any handler still
// scoping responses by tenant. The consumer NVR is single-tenant by
// construction; this always returns the empty string.
func (a *API) tenantID() string {
	return ""
}

// globalPIIReadGrant returns the operator escape-hatch flag from the
// bootstrap config. When true, Principals produced by the pre-OQ10
// internal/HTTP authentication paths gain ADR 0010's
// `session.pii.read` permission so legacy admin UIs continue to see
// unmasked Stream PII; JWT-authed requests are unaffected (they carry
// their own scope claim). See conf.Conf.GlobalPIIReadGrant.
//
// Closes recorder canonical-divergence D5 together with the
// per-request scope check on the JWT path.
func (a *API) globalPIIReadGrant() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.Conf == nil {
		return false
	}
	return a.Conf.GlobalPIIReadGrant
}

// globalRBACEnforce returns the operator switch from the bootstrap
// config that controls whether ADR 0010 permission gating applies to
// Principals produced by the pre-OQ10 internal/HTTP authentication
// paths (which carry no per-user scope claim). When false (default),
// service-account Principals from those paths bypass requirePermission
// gating — legacy admin UIs continue to work. When true, every
// principal gates on scope regardless of how it was authenticated.
//
// JWT-authed requests are unaffected: they always enforce scope
// against their `scope` claim per ADR 0011 D2.
//
// See conf.Conf.GlobalRBACEnforce.
func (a *API) globalRBACEnforce() bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.Conf == nil {
		return false
	}
	return a.Conf.GlobalRBACEnforce
}

// recorderID returns the recorder's UUIDv7 string per ADR 0002 D3
// when the identity package is wired (production), or the empty string
// for tests that don't set a.Identity. Used by the scope_kind
// validation in middlewareAuth to compare against the JWT's
// scope_target_id when scope_kind=recording_server.
func (a *API) recorderID() string {
	if a == nil || a.Identity == nil {
		return ""
	}
	return a.Identity.ID().String()
}

// recorderSiteID returns the recorder's bound site_id when known.
// The recorder does not persist a site_id today (pairing-aware
// site_id is a future slice); this helper returns the empty string,
// signalling to validateScopeKind that scope_kind=site requests
// should be accepted with a soft warning per the slice 4-D rollout
// plan rather than rejected. Tightens to a real lookup when site_id
// awareness lands.
func (a *API) recorderSiteID() string {
	// Forward-looking accessor: the recorder will eventually persist
	// a site_id (likely on identity, alongside the canonical-source
	// flags). Until then this returns "" so the validator falls into
	// the scopeKindOutcomeSiteUnknownAccepted branch.
	return ""
}

// readTenantScopedBody reads the request body up to maxInboundConfigSize
// and validates that any tenantId field included in the JSON matches the
// recorder's bound tenant. On mismatch, it writes a 403 and returns
// (nil, false). On read error, it writes a 400 and returns (nil, false).
// On success, it returns the body bytes for the caller to decode itself.
//
// A request body without a tenantId field passes through unchanged — the
// recorder uses its own tenant as the implicit value (per the D1
// resolution).
//
// This camelCase variant is used by escape-hatch endpoints whose body
// shapes inherit MediaMTX-lineage camelCase JSON keys throughout
// (`/v1/recorder/config` wraps `conf.OptionalGlobal`,
// `/v1/recorder/camera-defaults` wraps `conf.OptionalPath`). Using
// snake_case for just the tenant key on those bodies would split the
// body across two casings; the camelCase here is internally consistent
// with the rest of the body, not a stale carve-out from the legacy /v3
// surface. The OpenAPI spec's intro documents the split.
//
// The snake_case sibling readTenantSnakeCaseScopedBody applies to
// escape-hatch endpoints whose body's tenant key matches the canonical
// /v1 snake_case key (e.g., source-config, hooks).
func (a *API) readTenantScopedBody(ctx *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(&customLimitReader{ctx.Request.Body, maxInboundConfigSize})
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return nil, false
	}

	// We only check tenantId here; if the JSON is otherwise malformed,
	// the caller's full decode will surface the parse error.
	var meta struct {
		TenantID *string `json:"tenantId"`
	}
	if jerr := json.Unmarshal(body, &meta); jerr == nil &&
		meta.TenantID != nil && *meta.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return nil, false
	}

	return body, true
}

// readTenantSnakeCaseScopedBody mirrors readTenantScopedBody but reads
// the canonical snake_case `tenant_id` key. Used by escape-hatch
// endpoints whose response shape carries `tenant_id` per the canonical
// /v1 surface convention (e.g., /v1/recorder/cameras/{id}/source-config,
// /v1/recorder/cameras/{id}/hooks); the request body must mirror the
// response key.
func (a *API) readTenantSnakeCaseScopedBody(ctx *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(&customLimitReader{ctx.Request.Body, maxInboundConfigSize})
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return nil, false
	}

	var meta struct {
		TenantID *string `json:"tenant_id"`
	}
	if jerr := json.Unmarshal(body, &meta); jerr == nil &&
		meta.TenantID != nil && *meta.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return nil, false
	}

	return body, true
}
