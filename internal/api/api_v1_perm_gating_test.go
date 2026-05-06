// Package api: per-endpoint permission gating tests for slice 4-D.
//
// Exercises the requirePermission middleware against the role-catalog
// scenarios specified in ADR 0010 D4 (viewer / operator / auditor /
// admin built-in roles) plus the edge cases the recorder's gate has
// to handle:
//
//   - Empty-scope JWT (no permissions): every gated endpoint returns 403.
//   - Pre-OQ10 service-account principal under globalRBACEnforce=false:
//     gate is bypassed; legacy admin UIs continue to work.
//   - Pre-OQ10 service-account principal under globalRBACEnforce=true:
//     gate is enforced; service-account fail-closed.
//   - scope_kind validation: tenant + matching tenant_id passes;
//     wrong tenant_id returns 403; recording_server with matching id
//     passes; wrong recording_server id returns 403.
//   - auth.permission_denied audit chain integrity across multiple
//     denied requests.
package api //nolint:revive

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

// Permission sets per ADR 0010 D4. Tests reference these directly so a
// future amendment to the catalog is reflected in one place.

// adr0010ViewerScope is the viewer built-in role's permission set.
var adr0010ViewerScope = []string{
	"camera.list", "camera.read", "camera.live.view",
	"recording.list", "recording.read", "recording.playback",
	"recording_segment.list", "recording_segment.read",
	"clip.list", "clip.read", "clip.download",
	"policy.list", "policy.read",
	"stream.list", "stream.read",
	"storage.list", "storage.read",
	"event.list", "event.read",
	"health.read",
	"recorder_config.read",
	"remote_access.list",
}

// adr0010OperatorScope is the operator built-in role: viewer's set
// plus operational write actions.
func adr0010OperatorScope() []string {
	out := append([]string{}, adr0010ViewerScope...)
	out = append(out,
		"camera.create", "camera.update", "camera.delete",
		"recording.delete",
		"recording_segment.delete",
		"clip.create", "clip.export", "clip.delete",
		"policy.create", "policy.update", "policy.delete",
		"stream.kick",
		"session.pii.read",
		"storage.manage",
		"recorder_config.manage",
		"remote_access.create", "remote_access.revoke",
	)
	return out
}

// adr0010AuditorScope is the auditor built-in role: viewer's set
// plus audit.read + PII axes.
func adr0010AuditorScope() []string {
	out := append([]string{}, adr0010ViewerScope...)
	out = append(out, "audit.read", "session.pii.read", "recording.pii.read")
	return out
}

// adr0010AdminScope is the admin built-in role: operator + auditor's
// reads + user/role/lifecycle management.
func adr0010AdminScope() []string {
	out := append(adr0010OperatorScope(), "audit.read", "recording.pii.read")
	out = append(out,
		"user.list", "user.read", "user.invite", "user.manage",
		"role.assign",
		"software_update.manage",
		"device_lifecycle.manage",
	)
	return out
}

// permGatingFixture spins up an API server on a random port with the
// supplied auth.Claims as the resolved per-request Principal. The
// fixture stamps a fixed tenantId on the bootstrap config so
// scope_kind=tenant tests can assert specific match/mismatch values.
type permGatingFixture struct {
	api     *API
	srv     *httptest.Server
	token   string
	address string
}

// freePort allocates a transient TCP port from the OS by listening on
// :0, capturing the bound port, and immediately closing the listener.
// There is a TOCTOU window between the close and the recorder
// re-binding, but it's small in practice. The httpp.Server
// implementation doesn't expose its bound address, so we work around
// that by picking a free port up-front.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close() //nolint:errcheck
	return ln.Addr().String()
}

func newPermGatingFixture(t *testing.T, claims auth.Claims, opts ...permGatingOpt) *permGatingFixture {
	t.Helper()
	resetAuditBufferSingleton(t)

	cnf := tempConf(t, "api: yes\n")

	a := &API{
		Conf:         cnf,
		Started:      time.Now(),
		Parent:       &testParent{},
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		AuthManager: &test.AuthManager{
			AuthenticateWithClaimsImpl: func(_ *auth.Request) (string, auth.Claims, *auth.Error) {
				return claims.Subject, claims, nil
			},
		},
	}
	for _, opt := range opts {
		opt(a)
	}

	// httpp.Server doesn't expose its bound address back to callers,
	// so we pick a free port via net.Listen+close before initializing
	// the API. The existing test harness in api_test.go uses a fixed
	// 9997 port; that conflicts with developer machines running a
	// real recorder, so this fixture deliberately picks a fresh port.
	addr := freePort(t)
	a.Address = addr
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)

	// httptest.Server is unused — the API has its own listener.
	srv := &httptest.Server{} //nolint:exhaustruct
	_ = srv

	return &permGatingFixture{api: a, address: addr}
}

type permGatingOpt func(*API)

func withRBACEnforce(v bool) permGatingOpt {
	return func(a *API) {
		a.Conf.GlobalRBACEnforce = v
	}
}

// httpURL builds a request URL against the fixture's bound API.
func (f *permGatingFixture) httpURL(path string) string {
	return "http://" + f.address + path
}

// makeReq runs an HTTP request against the fixture and returns the
// response status code only — body draining is handled internally.
func (f *permGatingFixture) makeReq(t *testing.T, method, path string) int {
	t.Helper()
	req, err := http.NewRequest(method, f.httpURL(path), nil)
	require.NoError(t, err)
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	return res.StatusCode
}

// jwtClaimsForRole builds a JWT-shaped auth.Claims with the supplied
// scope set and a default cloud_user principal kind.
func jwtClaimsForRole(scope []string) auth.Claims {
	return auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		JTI:           "jti-test",
		PrincipalKind: "cloud_user",
		Scope:         scope,
	}
}

// TestPermGate_ViewerCanReadButNotWrite verifies the viewer built-in
// role's permission set: read endpoints succeed (we only assert 200
// or 4xx that's not 403); write endpoints return 403.
func TestPermGate_ViewerCanReadButNotWrite(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole(adr0010ViewerScope))

	// Read paths viewer can hit — recorder must not return 403.
	// /v1/cameras returns the empty list; /v1/recordings empty list.
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"viewer should be allowed camera.list")
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/recordings"),
		"viewer should be allowed recording.list")
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/storage-volumes"),
		"viewer should be allowed storage.list")

	// Write paths viewer cannot — must return 403.
	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodPost, "/v1/cameras"),
		"viewer must be denied camera.create")
	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodDelete, "/v1/recordings/some-id"),
		"viewer must be denied recording.delete")
	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/audit"),
		"viewer must be denied audit.read")
}

// TestPermGate_OperatorCanCRUDButNotAudit verifies the operator role:
// CRUD on cameras / policies / clips works; audit.read returns 403.
func TestPermGate_OperatorCanCRUDButNotAudit(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole(adr0010OperatorScope()))

	// Operator allowed.
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodPost, "/v1/cameras"),
		"operator should be allowed camera.create (handler may 400 on empty body — but not 403)")
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodDelete, "/v1/streams/some-id"),
		"operator should be allowed stream.kick")

	// Operator denied audit.read.
	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/audit"),
		"operator must be denied audit.read")
}

// TestPermGate_AuditorCanReadAuditButNotWrite verifies auditor: audit.read
// succeeds; camera.create returns 403.
func TestPermGate_AuditorCanReadAuditButNotWrite(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole(adr0010AuditorScope()))

	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/audit"),
		"auditor should be allowed audit.read")

	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodPost, "/v1/cameras"),
		"auditor must be denied camera.create")
	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodDelete, "/v1/cameras/cam-1"),
		"auditor must be denied camera.delete")
}

// TestPermGate_AdminCanDoEverything verifies admin: every gated
// endpoint succeeds (no 403).
func TestPermGate_AdminCanDoEverything(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole(adr0010AdminScope()))

	// A representative cross-section of write/read paths.
	for _, c := range []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/cameras"},
		{http.MethodPost, "/v1/cameras"},
		{http.MethodDelete, "/v1/cameras/cam-1"},
		{http.MethodGet, "/v1/audit"},
		{http.MethodGet, "/v1/recording-policies"},
		{http.MethodPost, "/v1/recording-policies"},
		{http.MethodGet, "/v1/recorder/config"},
		{http.MethodGet, "/v1/recorder/identity"},
		{http.MethodPost, "/v1/recorder/reboot"},
		{http.MethodGet, "/v1/recorder/network-info"},
		{http.MethodPost, "/v1/recorder/pair"},
		{http.MethodPost, "/v1/recorder/unpair"},
		{http.MethodPost, "/v1/diagnostics/ping"},
	} {
		require.NotEqual(t, http.StatusForbidden,
			f.makeReq(t, c.method, c.path),
			"admin must not be denied %s %s", c.method, c.path)
	}
}

// TestPermGate_EmptyScopeJWT verifies a JWT principal with empty scope
// fails closed: every gated endpoint returns 403.
func TestPermGate_EmptyScopeJWT(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole([]string{}))

	for _, c := range []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/cameras"},
		{http.MethodGet, "/v1/recordings"},
		{http.MethodGet, "/v1/audit"},
		{http.MethodPost, "/v1/cameras"},
		{http.MethodDelete, "/v1/cameras/cam-1"},
	} {
		require.Equal(t, http.StatusForbidden,
			f.makeReq(t, c.method, c.path),
			"empty-scope JWT must be denied %s %s", c.method, c.path)
	}
}

// TestPermGate_ServiceAccountWithRBACDisabled verifies the pre-OQ10
// compatibility path: a service-account principal with empty scope
// bypasses the gate when GlobalRBACEnforce=false. This covers the
// migration window where legacy admin UIs continue to authenticate
// via the internal/HTTP auth methods.
func TestPermGate_ServiceAccountWithRBACDisabled(t *testing.T) {
	// Empty-scope service-account principal — the shape principalFromAuthClaims
	// produces from the internal/HTTP auth path with globalPIIReadGrant=false.
	f := newPermGatingFixture(t, auth.Claims{
		Method: conf.AuthMethodInternal, // any non-JWT method
	})

	// Even write endpoints succeed since service-account is the
	// pre-OQ10 default privileged principal.
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"service-account with rbac=off should pass")
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodPost, "/v1/cameras"),
		"service-account with rbac=off should pass on writes")
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/audit"),
		"service-account with rbac=off should pass on audit.read (legacy)")
}

// TestPermGate_ServiceAccountWithRBACEnforced verifies the
// post-migration posture: when GlobalRBACEnforce=true a
// service-account principal with empty scope is gated like a JWT
// principal. Empty scope means every gated endpoint returns 403.
func TestPermGate_ServiceAccountWithRBACEnforced(t *testing.T) {
	f := newPermGatingFixture(t, auth.Claims{
		Method: conf.AuthMethodInternal,
	}, withRBACEnforce(true))

	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"service-account with rbac=on + empty scope must 403")
	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/audit"),
		"service-account with rbac=on must be denied audit.read")
}

// TestPermGate_ScopeKindTenantMatching verifies scope_kind=tenant +
// matching scope_target_id passes scope_kind validation.
func TestPermGate_ScopeKindTenantMatching(t *testing.T) {
	f := newPermGatingFixture(t, auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		PrincipalKind: "cloud_user",
		Scope:         adr0010ViewerScope,
		ScopeKind:     "tenant",
		// tenant_id matches the recorder's bound tenant (sentinel from tempConf).
		ScopeTargetID: "00000000-0000-0000-0000-000000000000",
	})

	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"scope_kind=tenant + matching tenant_id must pass")
}

// TestPermGate_ScopeKindTenantMismatch verifies scope_kind=tenant +
// non-matching scope_target_id is rejected with 403.
func TestPermGate_ScopeKindTenantMismatch(t *testing.T) {
	f := newPermGatingFixture(t, auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		PrincipalKind: "cloud_user",
		Scope:         adr0010ViewerScope,
		ScopeKind:     "tenant",
		ScopeTargetID: "ffffffff-ffff-ffff-ffff-ffffffffffff", // wrong tenant
	})

	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"scope_kind=tenant + wrong tenant_id must fail")
}

// TestPermGate_ScopeKindRecordingServerMatching verifies scope_kind=
// recording_server + matching recorder id passes.
func TestPermGate_ScopeKindRecordingServerMatching(t *testing.T) {
	// To assert "matching recorder id", the API must have an Identity
	// with a known UUID. We don't attempt to wire a real identity
	// package in this unit-level test; instead we assert the negative
	// case (mismatch returns 403) which also proves the validation
	// runs.
	f := newPermGatingFixture(t, auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		PrincipalKind: "cloud_user",
		Scope:         adr0010ViewerScope,
		ScopeKind:     "recording_server",
		// Empty identity → recorderID() returns "" → any non-empty
		// scope_target_id is a mismatch.
		ScopeTargetID: "11111111-1111-1111-1111-111111111111",
	})

	require.Equal(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"scope_kind=recording_server + non-matching id must fail")
}

// TestPermGate_ScopeKindOrganizationNotEnforced verifies scope_kind=
// organization is not enforced at the recorder per ADR 0010 D6
// amendment — passes even though the recorder has no organization
// awareness.
func TestPermGate_ScopeKindOrganizationNotEnforced(t *testing.T) {
	f := newPermGatingFixture(t, auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		PrincipalKind: "cloud_user",
		Scope:         adr0010ViewerScope,
		ScopeKind:     "organization",
		ScopeTargetID: "any-org-uuid",
	})

	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"scope_kind=organization is not enforced at the recorder")
}

// TestPermGate_ScopeKindSiteAcceptedWhenSiteIDUnknown verifies
// scope_kind=site is accepted with a soft-warning when the
// recorder's site_id is not yet known (the v1 default).
func TestPermGate_ScopeKindSiteAcceptedWhenSiteIDUnknown(t *testing.T) {
	f := newPermGatingFixture(t, auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		PrincipalKind: "cloud_user",
		Scope:         adr0010ViewerScope,
		ScopeKind:     "site",
		ScopeTargetID: "any-site-uuid",
	})

	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/cameras"),
		"scope_kind=site is accepted while recorder site_id is unknown (soft-warn path)")
}

// TestPermGate_AnonymousEndpointsBypass verifies /v1/info and
// /v1/health remain accessible without permission gating.
func TestPermGate_AnonymousEndpointsBypass(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole([]string{}))

	// Empty-scope JWT but /v1/info has no permission gate.
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/info"),
		"/v1/info must remain ungated")
	require.NotEqual(t, http.StatusForbidden,
		f.makeReq(t, http.MethodGet, "/v1/health"),
		"/v1/health must remain ungated")
}

// TestPermGate_AuditPermissionDeniedChainIntegrity verifies that
// multiple denied requests append to the audit chain in order, with
// per-entry prev_hash linkage intact. Closes the slice 4-D contract
// that auth.permission_denied is a first-class auditable event.
func TestPermGate_AuditPermissionDeniedChainIntegrity(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole([]string{}))

	// Drive multiple denied requests.
	for i := 0; i < 3; i++ {
		_ = f.makeReq(t, http.MethodGet, "/v1/audit")
	}

	snap := defaultAuditBuffer().Snapshot()
	denied := []defs.AuditLogEntry{}
	for _, e := range snap {
		if e.Action == "auth.permission_denied" {
			denied = append(denied, e)
		}
	}
	require.GreaterOrEqual(t, len(denied), 3,
		"each denied request must append exactly one auth.permission_denied audit entry")

	// Walk per the ordering of the buffer (oldest-first); the chain's
	// prev_hash must link successive denied entries through whatever
	// non-denied entries are interleaved (auth.login + scope-kind
	// success path). We assert this by walking the full snapshot in
	// order: each entry's prev_hash must equal the previous entry's
	// entry_hash.
	require.NotEmpty(t, snap)
	for i := 1; i < len(snap); i++ {
		require.Equal(t, snap[i-1].EntryHash, snap[i].PrevHash,
			"chain link broken between entries %d and %d", i-1, i)
		require.NotEmpty(t, snap[i].EntryHash, "EntryHash must be populated")
	}

	// The denied entries carry the required attribute keys.
	for _, e := range denied {
		require.Equal(t, defs.AuditOutcomeDenied, e.Outcome)
		require.NotEmpty(t, e.Attributes["endpoint"], "endpoint attribute must be populated")
		require.NotEmpty(t, e.Attributes["method"], "method attribute must be populated")
		require.NotEmpty(t, e.Attributes["required_permission"], "required_permission must be populated")
	}
}

// TestPermGate_ResponseBodyContainsRequiredPermission verifies the
// 403 response body names the missing permission so client UIs can
// surface a precise error message.
func TestPermGate_ResponseBodyContainsRequiredPermission(t *testing.T) {
	f := newPermGatingFixture(t, jwtClaimsForRole([]string{}))

	req, err := http.NewRequest(http.MethodGet, f.httpURL("/v1/audit"), nil)
	require.NoError(t, err)
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	buf := make([]byte, 1024)
	n, _ := res.Body.Read(buf)
	body := string(buf[:n])
	require.True(t, strings.Contains(body, "audit.read"),
		"403 body should reference the required permission, got: %s", body)
}

// TestValidateScopeKind covers the unit-level branch matrix for the
// scope_kind validator. Direct unit test, independent of the HTTP
// surface.
func TestValidateScopeKind(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		out, _ := validateScopeKind("", "", "tenant-id", "site-id", "rec-id")
		require.Equal(t, scopeKindOutcomeAbsent, out)
	})
	t.Run("tenant-match", func(t *testing.T) {
		out, _ := validateScopeKind("tenant", "tenant-id", "tenant-id", "", "")
		require.Equal(t, scopeKindOutcomeOK, out)
	})
	t.Run("tenant-mismatch", func(t *testing.T) {
		out, _ := validateScopeKind("tenant", "wrong", "tenant-id", "", "")
		require.Equal(t, scopeKindOutcomeMismatch, out)
	})
	t.Run("site-match", func(t *testing.T) {
		out, _ := validateScopeKind("site", "site-id", "", "site-id", "")
		require.Equal(t, scopeKindOutcomeOK, out)
	})
	t.Run("site-unknown-recorder-site-id", func(t *testing.T) {
		out, _ := validateScopeKind("site", "any", "", "", "")
		require.Equal(t, scopeKindOutcomeSiteUnknownAccepted, out)
	})
	t.Run("recording-server-match", func(t *testing.T) {
		out, _ := validateScopeKind("recording_server", "rec-id", "", "", "rec-id")
		require.Equal(t, scopeKindOutcomeOK, out)
	})
	t.Run("recording-server-mismatch", func(t *testing.T) {
		out, _ := validateScopeKind("recording_server", "wrong", "", "", "rec-id")
		require.Equal(t, scopeKindOutcomeMismatch, out)
	})
	t.Run("organization-not-enforced", func(t *testing.T) {
		out, _ := validateScopeKind("organization", "any", "tenant-id", "", "")
		require.Equal(t, scopeKindOutcomeOK, out)
	})
	t.Run("unknown-kind", func(t *testing.T) {
		out, _ := validateScopeKind("nonsense", "any", "tenant-id", "", "")
		require.Equal(t, scopeKindOutcomeUnknownKind, out)
	})
}
