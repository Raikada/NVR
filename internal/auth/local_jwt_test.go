package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// TestLocalJWT_AccepteWhenIssuerMatches confirms the recorder-local
// JWT validation path accepts a token signed by the LocalJWT key
// even when Method=Internal (the pre-pairing default in mediamtx.yml).
// Closes the integrator-flow gap: a fresh recorder with no MS
// configured must still authenticate /v1 callers.
func TestLocalJWT_AcceptWhenIssuerMatches(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	const issuer = "recorder/test-recorder-id"
	const audience = "recording_server/test-recorder-id"

	mgr := &Manager{
		Method:      conf.AuthMethodInternal,
		ReadTimeout: 1 * time.Second,
		LocalJWT: func() (any, string, string, bool) {
			return &priv.PublicKey, issuer, audience, true
		},
	}

	// Mint a token with mediamtx_permissions: api.
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"iss":            issuer,
		"sub":            "user-uuid",
		"aud":            audience,
		"iat":            now.Unix(),
		"exp":            now.Add(15 * time.Minute).Unix(),
		"jti":            "jti-1",
		"principal_kind": "local_user",
		"tenant_id":      "tenant-uuid",
		"scope":          []string{"camera.list"},
		"mediamtx_permissions": []map[string]string{
			{"action": "api"},
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	signed, err := tok.SignedString(priv)
	require.NoError(t, err)

	req := &Request{
		Action: conf.AuthActionAPI,
		IP:     net.ParseIP("10.0.0.5"),
		Credentials: &Credentials{
			Token: signed,
		},
	}
	user, parsed, aerr := mgr.AuthenticateWithClaims(req)
	require.Nil(t, aerr)
	require.Equal(t, "user-uuid", user)
	require.Equal(t, conf.AuthMethodJWT, parsed.Method)
	require.Equal(t, "tenant-uuid", parsed.TenantID)
	require.Equal(t, "local_user", parsed.PrincipalKind)
	require.Equal(t, []string{"camera.list"}, parsed.Scope)
}

// TestLocalJWT_RejectWrongIssuer verifies that a token signed with
// the right key but bearing a different `iss` claim falls through to
// the configured Method (which here is Internal and rejects).
func TestLocalJWT_RejectWrongIssuer(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	const issuer = "recorder/this-recorder"
	const audience = "recording_server/this-recorder"

	mgr := &Manager{
		Method:      conf.AuthMethodInternal,
		ReadTimeout: 1 * time.Second,
		LocalJWT: func() (any, string, string, bool) {
			return &priv.PublicKey, issuer, audience, true
		},
	}

	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"iss":            "recorder/some-other-recorder",
		"aud":            audience,
		"sub":            "user-uuid",
		"iat":            now.Unix(),
		"exp":            now.Add(15 * time.Minute).Unix(),
		"principal_kind": "local_user",
		"mediamtx_permissions": []map[string]string{
			{"action": "api"},
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	signed, err := tok.SignedString(priv)
	require.NoError(t, err)

	req := &Request{
		Action: conf.AuthActionAPI,
		IP:     net.ParseIP("127.0.0.1"),
		Credentials: &Credentials{
			Token: signed,
		},
	}
	// LocalJWT rejects (wrong iss), then falls through to Internal —
	// which has no users configured here, so it also rejects.
	_, _, aerr := mgr.AuthenticateWithClaims(req)
	require.NotNil(t, aerr)
}

// TestLocalJWT_DisabledWhenHookReturnsFalse confirms that LocalJWT is
// inert when the hook returns ok=false (e.g., before localauth is
// fully initialized).
func TestLocalJWT_DisabledWhenHookReturnsFalse(t *testing.T) {
	mgr := &Manager{
		Method:      conf.AuthMethodInternal,
		ReadTimeout: 1 * time.Second,
		LocalJWT: func() (any, string, string, bool) {
			return nil, "", "", false
		},
	}
	req := &Request{
		Action: conf.AuthActionAPI,
		IP:     net.ParseIP("127.0.0.1"),
		Credentials: &Credentials{
			Token: "anything",
		},
	}
	_, _, aerr := mgr.AuthenticateWithClaims(req)
	require.NotNil(t, aerr)
}
