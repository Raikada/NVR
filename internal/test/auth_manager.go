// Package test contains test utilities.
package test

import "github.com/bluenviron/mediamtx/internal/auth"

// AuthManager is a dummy auth manager.
type AuthManager struct {
	AuthenticateImpl           func(req *auth.Request) (string, *auth.Error)
	AuthenticateWithClaimsImpl func(req *auth.Request) (string, auth.Claims, *auth.Error)
	RefreshJWTJWKSImpl         func()
}

// Authenticate replicates auth.Manager.Authenticate.
func (m *AuthManager) Authenticate(req *auth.Request) (string, *auth.Error) {
	return m.AuthenticateImpl(req)
}

// AuthenticateWithClaims replicates auth.Manager.AuthenticateWithClaims.
// If AuthenticateWithClaimsImpl is unset, it falls back to AuthenticateImpl
// and returns a zero-value Claims — matches the pre-ADR-0011 behavior.
func (m *AuthManager) AuthenticateWithClaims(req *auth.Request) (string, auth.Claims, *auth.Error) {
	if m.AuthenticateWithClaimsImpl != nil {
		return m.AuthenticateWithClaimsImpl(req)
	}
	user, err := m.AuthenticateImpl(req)
	return user, auth.Claims{}, err
}

// RefreshJWTJWKS is a function that simulates a JWKS refresh.
func (m *AuthManager) RefreshJWTJWKS() {
	m.RefreshJWTJWKSImpl()
}

// NilAuthManager is an auth manager that accepts everything.
var NilAuthManager = &AuthManager{
	AuthenticateImpl: func(_ *auth.Request) (string, *auth.Error) {
		return "", nil
	},
}
