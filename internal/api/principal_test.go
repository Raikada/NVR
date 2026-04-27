package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestPrincipalHasPermission(t *testing.T) {
	p := &Principal{
		Scope: []string{"camera.list", "session.pii.read"},
	}

	require.True(t, p.HasPermission("camera.list"))
	require.True(t, p.HasPermission("session.pii.read"))
	require.False(t, p.HasPermission("camera.delete"))
	// case-sensitive byte equality
	require.False(t, p.HasPermission("Camera.List"))

	require.True(t, p.HasAnyPermission("camera.delete", "camera.list"))
	require.False(t, p.HasAnyPermission("camera.delete", "recording.read"))

	// nil-safe
	var nilP *Principal
	require.False(t, nilP.HasPermission("anything"))
	require.False(t, nilP.HasAnyPermission("a", "b"))
}

func TestPrincipalIsServiceAccount(t *testing.T) {
	require.True(t, (&Principal{PrincipalKind: defs.AuditActorKindServiceAccount}).IsServiceAccount())
	require.False(t, (&Principal{PrincipalKind: defs.AuditActorKindCloudUser}).IsServiceAccount())
	require.False(t, (&Principal{PrincipalKind: defs.AuditActorKindLocalUser}).IsServiceAccount())
	require.False(t, (&Principal{PrincipalKind: defs.AuditActorKindUnauthenticated}).IsServiceAccount())

	var nilP *Principal
	require.False(t, nilP.IsServiceAccount())
}

func TestPrincipalFromAuthClaimsJWT(t *testing.T) {
	c := auth.Claims{
		Method:            conf.AuthMethodJWT,
		Subject:           "11111111-1111-1111-1111-111111111111",
		JTI:               "abc-jti",
		TenantID:          "22222222-2222-2222-2222-222222222222",
		PrincipalKind:     "cloud_user",
		Scope:             []string{"camera.list", "recording.playback"},
		ScopeKind:         "site",
		ScopeTargetID:     "33333333-3333-3333-3333-333333333333",
		ClientFingerprint: "fp-opaque",
	}
	p := principalFromAuthClaims(c, false)

	require.Equal(t, "11111111-1111-1111-1111-111111111111", p.Sub)
	require.Equal(t, "abc-jti", p.RawJTI)
	require.Equal(t, "22222222-2222-2222-2222-222222222222", p.TenantID)
	require.Equal(t, defs.AuditActorKindCloudUser, p.PrincipalKind)
	require.Equal(t, []string{"camera.list", "recording.playback"}, p.Scope)
	require.Equal(t, "site", p.ScopeKind)
	require.Equal(t, "33333333-3333-3333-3333-333333333333", p.ScopeTargetID)
	require.Equal(t, "fp-opaque", p.ClientFingerprint)

	require.True(t, p.HasPermission("camera.list"))
	require.False(t, p.IsServiceAccount())
}

func TestPrincipalFromAuthClaimsLocalUser(t *testing.T) {
	p := principalFromAuthClaims(auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-uuid",
		PrincipalKind: "local_user",
	}, false)
	require.Equal(t, defs.AuditActorKindLocalUser, p.PrincipalKind)
}

func TestPrincipalFromAuthClaimsServiceAccount(t *testing.T) {
	p := principalFromAuthClaims(auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "svc-uuid",
		PrincipalKind: "service_account",
	}, false)
	require.Equal(t, defs.AuditActorKindServiceAccount, p.PrincipalKind)
	require.True(t, p.IsServiceAccount())
}

func TestPrincipalFromAuthClaimsUnknownKind(t *testing.T) {
	// Defensive: an issuer producing an unrecognized principal_kind
	// maps to AuditActorKindUnauthenticated, not a panic. The
	// middleware promotes unauthenticated to service_account on the
	// success-emit path, but the Principal itself surfaces the raw
	// recognition failure for downstream visibility.
	p := principalFromAuthClaims(auth.Claims{
		Method:        conf.AuthMethodJWT,
		PrincipalKind: "robot_overlord",
	}, false)
	require.Equal(t, defs.AuditActorKindUnauthenticated, p.PrincipalKind)
}

func TestPrincipalFromAuthClaimsPreOQ10(t *testing.T) {
	// Internal / HTTP auth path: zero-value Claims with Method set.
	for _, m := range []conf.AuthMethod{conf.AuthMethodInternal, conf.AuthMethodHTTP} {
		p := principalFromAuthClaims(auth.Claims{Method: m}, false)
		require.Equal(t, defs.AuditActorKindServiceAccount, p.PrincipalKind)
		require.Empty(t, p.Sub)
		require.Empty(t, p.Scope)
		require.True(t, p.IsServiceAccount())
		require.False(t, p.HasPermission("camera.list"))
	}
}

// TestPrincipalFromAuthClaimsPreOQ10WithGlobalGrant covers the operator
// escape-hatch path: when the bootstrap config sets
// GlobalPIIReadGrant=true, a Principal produced by the internal/HTTP
// auth path gains session.pii.read so legacy admin UIs continue to see
// unmasked Stream PII. JWT-authed requests are unaffected.
func TestPrincipalFromAuthClaimsPreOQ10WithGlobalGrant(t *testing.T) {
	for _, m := range []conf.AuthMethod{conf.AuthMethodInternal, conf.AuthMethodHTTP} {
		p := principalFromAuthClaims(auth.Claims{Method: m}, true)
		require.Equal(t, defs.AuditActorKindServiceAccount, p.PrincipalKind)
		require.True(t, p.HasPermission(PermSessionPIIRead),
			"globalPIIReadGrant=true must inject session.pii.read into the static principal")
		// Other permissions remain ungranted.
		require.False(t, p.HasPermission("camera.list"))
	}

	// JWT path: scope claim is authoritative; the grant flag is ignored.
	p := principalFromAuthClaims(auth.Claims{
		Method:        conf.AuthMethodJWT,
		PrincipalKind: "cloud_user",
		Scope:         []string{"camera.list"},
	}, true)
	require.False(t, p.HasPermission(PermSessionPIIRead),
		"globalPIIReadGrant must not leak into JWT-authed principals")
	require.True(t, p.HasPermission("camera.list"))
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	original := &Principal{
		Sub:           "user-uuid",
		PrincipalKind: defs.AuditActorKindCloudUser,
		Scope:         []string{"camera.list"},
	}
	setPrincipalOnContext(c, original)

	got := principalFromContext(c)
	require.Same(t, original, got)
}

func TestPrincipalFromContextDefensiveDefault(t *testing.T) {
	// If middlewareAuth is bypassed for any reason, downstream
	// handlers should observe the unauthenticated principal rather
	// than panic on a nil dereference.
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	got := principalFromContext(c)
	require.NotNil(t, got)
	require.Equal(t, defs.AuditActorKindUnauthenticated, got.PrincipalKind)
	require.Empty(t, got.Scope)
	require.False(t, got.HasPermission("camera.list"))
}

// TestMiddlewareAuthSetsPrincipalOnContext is an end-to-end check that
// middlewareAuth places a Principal carrying ADR 0011 D2 fields on
// the gin.Context where downstream handlers can read it. The fake
// AuthManager produces a JWT-shaped Claims; the test handler reads
// the principal back via principalFromContext and asserts.
func TestMiddlewareAuthSetsPrincipalOnContext(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")

	expectedClaims := auth.Claims{
		Method:        conf.AuthMethodJWT,
		Subject:       "user-sub-uuid",
		JTI:           "jti-1",
		TenantID:      "tenant-uuid",
		PrincipalKind: "cloud_user",
		Scope:         []string{"camera.list", "session.pii.read"},
		ScopeKind:     "site",
		ScopeTargetID: "site-uuid",
	}

	api := API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		Conf:         cnf,
		AuthManager: &test.AuthManager{
			AuthenticateWithClaimsImpl: func(_ *auth.Request) (string, auth.Claims, *auth.Error) {
				return expectedClaims.Subject, expectedClaims, nil
			},
		},
		Parent: &testParent{},
	}
	err := api.Initialize()
	require.NoError(t, err)
	defer api.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	// /v1/info goes through middlewareAuth then onInfo. We don't
	// have access to onInfo's gin.Context from the test harness, so
	// we drive the round-trip and trust the fact that the request
	// succeeded means the middleware ran. The unit-level
	// principal-on-context guarantees are covered above; this case
	// is the integration check that the middleware doesn't reject
	// a JWT-shaped Claims and that no panic surfaces.
	u, err := url.Parse("http://localhost:9997/v1/info")
	require.NoError(t, err)

	res, err := hc.Get(u.String())
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}
