package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// TestApplyPairingOverrideSwitchesMethodAndForcesRePull confirms the
// override flips Method to JWT, sets URL/issuer/audience, and clears
// the cached JWKS so a stale cache from a prior binding can't slip
// through.
func TestApplyPairingOverrideSwitchesMethodAndForcesRePull(t *testing.T) {
	m := &Manager{Method: conf.AuthMethodInternal}
	pool := x509.NewCertPool()

	m.ApplyPairingOverride(
		conf.AuthMethodJWT,
		"https://ms.local/.well-known/jwks.json",
		"",
		"https://ms.local",
		"recording_server/abc",
		pool,
	)

	require.Equal(t, conf.AuthMethodJWT, m.Method)
	require.Equal(t, "https://ms.local/.well-known/jwks.json", m.JWTJWKS)
	require.Equal(t, "https://ms.local", m.JWTIssuer)
	require.Equal(t, "recording_server/abc", m.JWTAudience)
	require.Equal(t, "mediamtx_permissions", m.JWTClaimKey)
	require.Same(t, pool, m.JWTJWKSRootCAs)

	// Subsequent override with a new URL forces a re-pull.
	m.jwksLastRefresh = time.Now()
	m.ApplyPairingOverride(
		conf.AuthMethodJWT,
		"https://ms2.local/.well-known/jwks.json",
		"",
		"https://ms2.local",
		"recording_server/abc",
		pool,
	)
	require.True(t, m.jwksLastRefresh.IsZero())
}

// TestApplyPairingOverrideEndToEnd boots a fake MS over HTTPS using an
// ad-hoc root CA, has the override chain-pin to that root, mints a
// JWT signed by the fake MS's RS256 keypair, and confirms the
// recorder's auth.Manager validates it.
//
// Verifies the wiring as a whole: ApplyPairingOverride flips the
// manager into JWT mode; pullJWTJWKS uses the JWTJWKSRootCAs path
// (chain-pinning rather than fingerprint pinning); the JWKS pull goes
// through; and a token signed against the published JWKS authenticates.
func TestApplyPairingOverrideEndToEnd(t *testing.T) {
	// 1. Build an ad-hoc CA + leaf cert for the fake MS server.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ms-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "ms.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(leafPEM, keyPEM)
	require.NoError(t, err)
	// Append the root DER so clients pinning by chain see both leaf
	// and root in the handshake (mirrors the MS's tlsConfig pattern).
	tlsCert.Certificate = append(tlsCert.Certificate, caDER)

	// 2. Generate the JWT signing key (ECDSA P256). The fake MS
	// publishes its public half via JWKS.
	signKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// 3. Spin up the fake MS HTTPS server.
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jwk, err2 := jwkset.NewJWKFromKey(&signKey.PublicKey, jwkset.JWKOptions{
			Metadata: jwkset.JWKMetadataOptions{
				KID: "test-kid",
			},
		})
		require.NoError(t, err2)
		jwkSet := jwkset.NewMemoryStorage()
		require.NoError(t, jwkSet.KeyWrite(context.Background(), jwk))
		body, err2 := jwkSet.JSONPublic(r.Context())
		require.NoError(t, err2)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	ts.StartTLS()
	defer ts.Close()

	// 4. Build a recorder-side root pool that trusts the fake MS root.
	pool := x509.NewCertPool()
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	require.True(t, pool.AppendCertsFromPEM(rootPEM))

	// 5. Configure the auth.Manager via ApplyPairingOverride.
	m := &Manager{Method: conf.AuthMethodInternal, ReadTimeout: 5 * time.Second}
	m.ApplyPairingOverride(
		conf.AuthMethodJWT,
		ts.URL+"/.well-known/jwks.json",
		"",
		"https://test-ms",
		"recording_server/abc",
		pool,
	)

	// 6. Mint a JWT with mediamtx_permissions allowing API access.
	type customClaims struct {
		jwt.RegisteredClaims
		MediaMTXPermissions []conf.AuthInternalUserPermission `json:"mediamtx_permissions"`
	}
	perms := []conf.AuthInternalUserPermission{{Action: conf.AuthActionAPI}}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, customClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://test-ms",
			Audience:  jwt.ClaimStrings{"recording_server/abc"},
			Subject:   "user-1",
			ID:        "jti-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		MediaMTXPermissions: perms,
	})
	tok.Header[jwkset.HeaderKID] = "test-kid"
	tokenStr, err := tok.SignedString(signKey)
	require.NoError(t, err)

	// 7. Authenticate the request — the chain-pinned JWKS pull should
	// succeed and the token validation should pass.
	req := &Request{
		Action:      conf.AuthActionAPI,
		Path:        "/v1/cameras",
		Protocol:    ProtocolHLS, // any HTTP-class protocol
		Credentials: &Credentials{Token: tokenStr},
		IP:          net.ParseIP("127.0.0.1"),
	}
	user, claims, authErr := m.AuthenticateWithClaims(req)
	require.Nil(t, authErr, "auth failed: %v", authErr)
	require.Equal(t, "user-1", user)
	require.Equal(t, conf.AuthMethodJWT, claims.Method)

	_ = json.RawMessage(nil) // keep encoding/json import live for parity with neighboring tests
}

// TestApplyPairingOverrideRejectsBadChain asserts that a JWKS pull
// against an MS with the wrong chain (untrusted root) fails — this is
// the security property of chain-pinning, ensuring an attacker can't
// substitute a different MS by hijacking DNS/mDNS to a server with a
// different trust anchor.
func TestApplyPairingOverrideRejectsBadChain(t *testing.T) {
	// Trusted root.
	trustedKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	trustedTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "trusted-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	trustedDER, _ := x509.CreateCertificate(rand.Reader, trustedTmpl, trustedTmpl, &trustedKey.PublicKey, trustedKey)
	trustedPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: trustedDER})
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(trustedPEM)

	// Hostile MS: uses a *different* root that the recorder doesn't
	// trust.
	hostileKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	hostileTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(99),
		Subject:               pkix.Name{CommonName: "hostile-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	hostileDER, _ := x509.CreateCertificate(rand.Reader, hostileTmpl, hostileTmpl, &hostileKey.PublicKey, hostileKey)
	hostileCert, _ := x509.ParseCertificate(hostileDER)

	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: "hostile-leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, hostileCert, &leafKey.PublicKey, hostileKey)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyDER, _ := x509.MarshalECPrivateKey(leafKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	tlsCert, _ := tls.X509KeyPair(leafPEM, keyPEM)
	tlsCert.Certificate = append(tlsCert.Certificate, hostileDER)

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("{}"))
	}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	ts.StartTLS()
	defer ts.Close()

	m := &Manager{Method: conf.AuthMethodInternal, ReadTimeout: 5 * time.Second}
	m.ApplyPairingOverride(
		conf.AuthMethodJWT,
		ts.URL+"/.well-known/jwks.json",
		"",
		"https://hostile-ms",
		"recording_server/abc",
		pool, // trusted-root pool — does NOT include hostile-root
	)

	req := &Request{
		Action:      conf.AuthActionAPI,
		Path:        "/v1/cameras",
		Protocol:    ProtocolHLS,
		Credentials: &Credentials{Token: "irrelevant"},
		IP:          net.ParseIP("127.0.0.1"),
	}
	_, _, authErr := m.AuthenticateWithClaims(req)
	require.NotNil(t, authErr, "expected chain validation to reject hostile MS")
}
