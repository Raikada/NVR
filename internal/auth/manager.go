// Package auth contains the authentication system.
package auth

import (
	"bytes"
	gocrypto "crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/protocols/tls"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// PauseAfterError is the pause to apply after an authentication failure.
	PauseAfterError = 2 * time.Second

	maxInboundBodySize = 128 * 1024
	jwksRefreshPeriod  = 60 * 60 * time.Second
)

func isHTTP(req *Request) bool {
	return req.Protocol == ProtocolHLS || req.Protocol == ProtocolWebRTC ||
		req.Action == conf.AuthActionPlayback ||
		req.Action == conf.AuthActionAPI ||
		req.Action == conf.AuthActionMetrics ||
		req.Action == conf.AuthActionPprof
}

func matchesPermission(perms []conf.AuthInternalUserPermission, req *Request) bool {
	for _, perm := range perms {
		if perm.Action == req.Action {
			if perm.Action == conf.AuthActionPublish ||
				perm.Action == conf.AuthActionRead ||
				perm.Action == conf.AuthActionPlayback {
				switch {
				case perm.Path == "":
					return true

				case strings.HasPrefix(perm.Path, "~"):
					regexp, err := regexp.Compile(perm.Path[1:])
					if err == nil && regexp.MatchString(req.Path) {
						return true
					}

				case perm.Path == req.Path:
					return true
				}
			} else {
				return true
			}
		}
	}

	return false
}

func getToken(tokenInHTTPQuery bool, req *Request) string {
	switch {
	case req.Credentials.Token != "":
		return req.Credentials.Token

	case req.Credentials.Pass != "":
		return req.Credentials.Pass

		// always allow passing tokens through query parameters with RTSP and RTMP since there's no alternative.
	case req.Protocol == ProtocolRTSP || req.Protocol == ProtocolRTMP ||
		(tokenInHTTPQuery && isHTTP(req)):
		v, err := url.ParseQuery(req.Query)
		if err == nil {
			if len(v["token"]) == 1 {
				return v["token"][0]
			}

			// legacy query key
			if len(v["jwt"]) == 1 {
				return v["jwt"][0]
			}
		}
	}

	return ""
}

// LocalJWTKeyFunc is the recorder-local JWT key resolution function.
// Returns the public key + the expected `iss` claim value the recorder
// uses to brand its locally-issued JWTs. nil signals "no recorder-local
// signing key configured" (the auth manager skips the local-JWT path).
//
// Wired by Core at startup once the localauth.Manager has loaded /
// generated the recorder-local signing key. The pre-pairing auth slice
// (2026-05-06) is the only producer; future slices may carry additional
// local issuers (e.g., a recovery-bundle-restored key) by extending
// this hook to return a keyset.
type LocalJWTKeyFunc func() (publicKey any, issuer, audience string, ok bool)

// Manager is the authentication manager.
type Manager struct {
	Method             conf.AuthMethod
	InternalUsers      []conf.AuthInternalUser
	HTTPAddress        string
	HTTPFingerprint    string
	HTTPExclude        []conf.AuthInternalUserPermission
	JWTJWKS            string
	JWTJWKSFingerprint string
	JWTClaimKey        string
	JWTExclude         []conf.AuthInternalUserPermission
	JWTInHTTPQuery     *bool
	JWTIssuer          string
	JWTAudience        string
	// JWTJWKSRootCAs, when non-nil, overrides JWTJWKSFingerprint for
	// the JWKS HTTPS pull and validates the server cert by chain
	// against this pool. Set by Core's pairing-aware auth wiring at
	// startup (the pinned MS root CA per ADR 0012 D5) so recorders
	// can talk to a paired MS whose service cert rotates every 30
	// days per ADR 0011 D4 without re-pinning by leaf each rotation.
	// Nil keeps the legacy fingerprint-pinning path.
	JWTJWKSRootCAs *x509.CertPool
	ReadTimeout    time.Duration

	// LocalJWT is the recorder-local JWT validation hook (pre-pairing
	// auth slice 2026-05-06). When set, every Authenticate call where
	// the request carries a Bearer / token credential tries the local
	// path before / alongside the configured Method. This lets the
	// SPA's Login endpoint mint a JWT that the recorder accepts even
	// when authMethod=internal in mediamtx.yml. nil keeps the legacy
	// behavior.
	LocalJWT LocalJWTKeyFunc

	mutex           sync.RWMutex
	jwksLastRefresh time.Time
	jwtKeyFunc      keyfunc.Keyfunc
}

// ReloadInternalUsers reloads InternalUsers.
func (m *Manager) ReloadInternalUsers(u []conf.AuthInternalUser) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.InternalUsers = u
}

// ApplyPairingOverride re-points the auth manager at JWT auth using
// the bound MS's published JWKS / issuer / audience. Used by Core at
// startup when the recorder is paired (per pairing-flows §4) so the
// recorder accepts MS-issued operator JWTs even though mediamtx.yml
// continues to say `authMethod: internal`. The on-disk config is
// unchanged; the override is in-process only.
//
// rootCAs is the recorder's pinned MS root pool per ADR 0012 D5.
// When non-nil it supersedes any prior fingerprint pinning for the
// JWKS HTTPS pull (the MS service cert rotates every 30 days per ADR
// 0011 D4; chain-pinning to the operator-pinned root tolerates that
// rotation without re-pairing).
//
// Idempotent: calling with the same JWKS URL is a no-op for the
// cache; calling with a new URL forces the next request to re-pull.
// Safe to call concurrently with Authenticate — the mutex guards the
// field swap and the JWKS pull holds the same mutex.
func (m *Manager) ApplyPairingOverride(method conf.AuthMethod, jwks, jwksFingerprint, issuer, audience string, rootCAs *x509.CertPool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	jwksChanged := m.JWTJWKS != jwks
	m.Method = method
	m.JWTJWKS = jwks
	m.JWTJWKSFingerprint = jwksFingerprint
	m.JWTJWKSRootCAs = rootCAs
	m.JWTIssuer = issuer
	m.JWTAudience = audience
	if jwksChanged {
		// Force re-pull on next Authenticate so a stale cached JWKS
		// from a previous binding doesn't slip through.
		m.jwksLastRefresh = time.Time{}
		m.jwtKeyFunc = nil
	}
	// JWTClaimKey defaults to "mediamtx_permissions" — the value MS-
	// issued JWTs carry per camera-canonical-push.md §6.1. Operators
	// can override in mediamtx.yml.
	if m.JWTClaimKey == "" {
		m.JWTClaimKey = "mediamtx_permissions"
	}
}

// Claims carries ADR 0011 D2 JWT claims surfaced to callers that want
// to build a per-request principal (e.g., the API middleware). Fields
// are populated only on the JWT path; the Internal and HTTP paths
// produce a zero-value Claims and the caller treats those requests as
// pre-OQ10 service-account auth.
//
// Fields mirror the ADR 0011 D2 token shape verbatim. Absence of a
// field in the token is represented as the zero value (empty string,
// nil slice).
type Claims struct {
	// Method is the auth method that produced these claims. Lets the
	// caller distinguish JWT (real ADR 0011 claims) from Internal /
	// HTTP (no claims, zero-value struct).
	Method conf.AuthMethod

	// JWT registered claims.
	Subject string
	JTI     string

	// ADR 0011 D2 user-level claims.
	TenantID          string
	PrincipalKind     string
	Scope             []string
	ScopeKind         string
	ScopeTargetID     string
	ClientFingerprint string
}

// Authenticate authenticates a request.
// It returns the user name.
func (m *Manager) Authenticate(req *Request) (string, *Error) {
	user, _, err := m.AuthenticateWithClaims(req)
	return user, err
}

// AuthenticateWithClaims authenticates a request and returns the
// resolved user identifier alongside the parsed Claims. For the JWT
// auth path the Claims carries ADR 0011 D2 fields; for Internal /
// HTTP the Claims is zero-valued (with Method set so callers can
// branch). Added per ADR 0011 to let the API layer build a Principal
// without re-parsing the JWT.
//
// Recorder-local JWT path (pre-pairing auth slice 2026-05-06): when
// LocalJWT is set and the request carries a Bearer / token credential,
// we try the recorder-local validation first. On match, we return
// claims with Method=AuthMethodJWT (so the API layer's principal
// builder treats it as a JWT-authed request). On miss, we fall through
// to the configured Method path.
func (m *Manager) AuthenticateWithClaims(req *Request) (string, Claims, *Error) {
	// Always extract the token: the recorder-local JWT path is tried
	// regardless of m.Method, so even Method=Internal deployments
	// surface a Bearer token on the request.
	token := getToken(m.Method == conf.AuthMethodJWT && m.JWTInHTTPQuery != nil && *m.JWTInHTTPQuery, req)
	if token == "" && (m.Method == conf.AuthMethodHTTP || m.Method == conf.AuthMethodJWT) {
		// Re-extract for HTTP/JWT methods (preserves prior behavior for
		// query-string token forms).
		token = getToken(m.Method == conf.AuthMethodJWT && m.JWTInHTTPQuery != nil && *m.JWTInHTTPQuery, req)
	}

	// Recorder-local JWT path (additive, runs before the configured
	// Method). Tried only when a token is present; on miss we fall
	// through silently. On match we short-circuit with Method=JWT so
	// the per-route requirePermission middleware sees a populated Scope.
	if token != "" && m.LocalJWT != nil {
		if user, claims, ok := m.tryLocalJWT(req, token); ok {
			return user, claims, nil
		}
	}

	var user string
	var claims Claims
	claims.Method = m.Method
	var err error

	switch m.Method {
	case conf.AuthMethodInternal:
		user, err = m.authenticateInternal(req)

	case conf.AuthMethodHTTP:
		user, err = m.authenticateHTTP(req, token)

	default:
		user, claims, err = m.authenticateJWTWithClaims(req, token)
	}

	if err != nil {
		return "", Claims{}, &Error{
			Wrapped:        err,
			AskCredentials: (req.Credentials.User == "" && req.Credentials.Pass == "" && token == ""),
		}
	}

	return user, claims, nil
}

// tryLocalJWT attempts to validate the token against the recorder-local
// signing key. Returns (user, claims, true) on success; on miss
// (signature invalid / iss mismatch / token malformed) returns ("", _,
// false) and the caller falls through to the configured Method.
//
// When the token verifies but doesn't carry the recorder-local
// permissions for this Action, we still return false — the caller
// checks all configured methods, and a token that has the wrong scope
// for the request is just an authentication miss as far as the auth
// manager is concerned. The per-route requirePermission middleware
// (slice 4-D) reports the granular forbidden when scope is the only
// gap, but it can only do that if a Method=JWT path that succeeded
// reaches the API layer; here we want the "wrong-scope token from a
// MS-issued issuer" case to fall through to MS validation rather than
// produce a 401 from the local path.
func (m *Manager) tryLocalJWT(req *Request, token string) (string, Claims, bool) {
	pub, issuer, audience, ok := m.LocalJWT()
	if !ok || pub == nil {
		return "", Claims{}, false
	}
	var cc jwtClaims
	cc.permissionsKey = m.JWTClaimKey
	if cc.permissionsKey == "" {
		cc.permissionsKey = "mediamtx_permissions"
	}
	parserOpts := []jwt.ParserOption{
		jwt.WithIssuer(issuer),
	}
	if audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(audience))
	}
	_, err := jwt.ParseWithClaims(token, &cc, func(t *jwt.Token) (interface{}, error) {
		if _, isECDSA := t.Method.(*jwt.SigningMethodECDSA); !isECDSA {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return pub, nil
	}, parserOpts...)
	if err != nil {
		return "", Claims{}, false
	}
	if !matchesPermission(cc.permissions, req) {
		return "", Claims{}, false
	}
	out := Claims{
		Method:            conf.AuthMethodJWT,
		Subject:           cc.Subject,
		JTI:               cc.ID,
		TenantID:          cc.tenantID,
		PrincipalKind:     cc.principalKind,
		Scope:             cc.scope,
		ScopeKind:         cc.scopeKind,
		ScopeTargetID:     cc.scopeTargetID,
		ClientFingerprint: cc.clientFingerprint,
	}
	return cc.Subject, out, true
}

func (m *Manager) authenticateInternal(req *Request) (string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, u := range m.InternalUsers {
		if ok := m.authenticateWithUser(req, &u); ok {
			return req.Credentials.User, nil
		}
	}

	return "", fmt.Errorf("authentication failed")
}

func (m *Manager) authenticateWithUser(
	req *Request,
	u *conf.AuthInternalUser,
) bool {
	if len(u.IPs) != 0 && !u.IPs.Contains(req.IP) {
		return false
	}

	if !matchesPermission(u.Permissions, req) {
		return false
	}

	if u.User != "any" {
		if req.CustomVerifyFunc != nil {
			if ok := req.CustomVerifyFunc(string(u.User), string(u.Pass)); !ok {
				return false
			}
		} else {
			if !u.User.Check(req.Credentials.User) || !u.Pass.Check(req.Credentials.Pass) {
				return false
			}
		}
	}

	return true
}

func (m *Manager) authenticateHTTP(req *Request, token string) (string, error) {
	if matchesPermission(m.HTTPExclude, req) {
		return "", nil
	}

	enc, _ := json.Marshal(struct {
		IP       string     `json:"ip"`
		User     string     `json:"user"`
		Password string     `json:"password"`
		Token    string     `json:"token"`
		Action   string     `json:"action"`
		Path     string     `json:"path"`
		Protocol string     `json:"protocol"`
		ID       *uuid.UUID `json:"id"`
		Query    string     `json:"query"`
	}{
		IP:       req.IP.String(),
		User:     req.Credentials.User,
		Password: req.Credentials.Pass,
		Token:    token,
		Action:   string(req.Action),
		Path:     req.Path,
		Protocol: string(req.Protocol),
		ID:       req.ID,
		Query:    req.Query,
	})

	tr := &http.Transport{
		TLSClientConfig: tls.MakeConfig(m.HTTPFingerprint),
	}
	defer tr.CloseIdleConnections()

	httpClient := &http.Client{
		Timeout:   m.ReadTimeout,
		Transport: tr,
	}

	res, err := httpClient.Post(m.HTTPAddress, "application/json", bytes.NewReader(enc))
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		resBody, err2 := io.ReadAll(&customLimitReader{res.Body, maxInboundBodySize})
		if err2 == nil && len(resBody) != 0 {
			return "", fmt.Errorf("server replied with code %d: %s", res.StatusCode, string(resBody))
		}

		return "", fmt.Errorf("server replied with code %d", res.StatusCode)
	}

	return req.Credentials.User, nil
}

func (m *Manager) authenticateJWTWithClaims(req *Request, token string) (string, Claims, error) {
	if matchesPermission(m.JWTExclude, req) {
		return "", Claims{Method: conf.AuthMethodJWT}, nil
	}

	keyfunc, err := m.pullJWTJWKS()
	if err != nil {
		return "", Claims{}, err
	}

	if token == "" {
		return "", Claims{}, fmt.Errorf("JWT not provided")
	}

	var opts []jwt.ParserOption
	if m.JWTIssuer != "" {
		opts = append(opts, jwt.WithIssuer(m.JWTIssuer))
	}
	if m.JWTAudience != "" {
		opts = append(opts, jwt.WithAudience(m.JWTAudience))
	}

	var cc jwtClaims
	cc.permissionsKey = m.JWTClaimKey
	_, err = jwt.ParseWithClaims(token, &cc, keyfunc, opts...)
	if err != nil {
		return "", Claims{}, err
	}

	if !matchesPermission(cc.permissions, req) {
		return "", Claims{}, fmt.Errorf("user doesn't have permission to perform action")
	}

	out := Claims{
		Method:            conf.AuthMethodJWT,
		Subject:           cc.Subject,
		JTI:               cc.ID,
		TenantID:          cc.tenantID,
		PrincipalKind:     cc.principalKind,
		Scope:             cc.scope,
		ScopeKind:         cc.scopeKind,
		ScopeTargetID:     cc.scopeTargetID,
		ClientFingerprint: cc.clientFingerprint,
	}
	return cc.Subject, out, nil
}

func (m *Manager) pullJWTJWKS() (jwt.Keyfunc, error) {
	now := time.Now()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	if now.Sub(m.jwksLastRefresh) >= jwksRefreshPeriod {
		var tlsCfg *gocrypto.Config
		switch {
		case m.JWTJWKSRootCAs != nil:
			// Pairing-aware path: chain-pin to the MS root CA per ADR
			// 0012 D5. InsecureSkipVerify=false (default) — Go's stdlib
			// performs full chain validation against RootCAs. We skip
			// hostname verification because the MS's service cert may
			// only carry URI SANs (raikada://management/<id>) per ADR
			// 0011 D3, not DNS SANs matching the resolved URL host.
			// VerifyConnection re-asserts chain validity manually with
			// hostname check stripped.
			tlsCfg = &gocrypto.Config{
				InsecureSkipVerify: true, //nolint:gosec
				VerifyConnection: func(cs gocrypto.ConnectionState) error {
					if len(cs.PeerCertificates) == 0 {
						return fmt.Errorf("no peer certificates")
					}
					opts := x509.VerifyOptions{
						Roots:         m.JWTJWKSRootCAs,
						Intermediates: x509.NewCertPool(),
					}
					for _, c := range cs.PeerCertificates[1:] {
						opts.Intermediates.AddCert(c)
					}
					_, err := cs.PeerCertificates[0].Verify(opts)
					return err
				},
			}
		default:
			tlsCfg = tls.MakeConfig(m.JWTJWKSFingerprint)
		}
		tr := &http.Transport{
			TLSClientConfig: tlsCfg,
		}
		defer tr.CloseIdleConnections()

		httpClient := &http.Client{
			Timeout:   (m.ReadTimeout),
			Transport: tr,
		}

		res, err := httpClient.Get(m.JWTJWKS)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()

		var raw json.RawMessage
		err = json.NewDecoder(&customLimitReader{res.Body, maxInboundBodySize}).Decode(&raw)
		if err != nil {
			return nil, err
		}

		tmp, err := keyfunc.NewJWKSetJSON(raw)
		if err != nil {
			return nil, err
		}

		m.jwtKeyFunc = tmp
		m.jwksLastRefresh = now
	}

	return m.jwtKeyFunc.Keyfunc, nil
}

// RefreshJWTJWKS refreshes the JWT JWKS.
func (m *Manager) RefreshJWTJWKS() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.jwksLastRefresh = time.Time{}
}
