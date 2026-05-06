package localauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/store"
)

const (
	// initialAdminPasswordFile is written once at bootstrap so an
	// operator who missed the startup-log banner can still find the
	// auto-generated admin password.
	initialAdminPasswordFile = "initial-admin-password.txt"

	// LockThreshold and LockDuration mirror the management server's
	// LocalUser lockout policy.
	LockThreshold = 5
	LockDuration  = 15 * time.Minute

	// AccessTokenTTL is the recorder-local JWT lifetime. Mirrors the
	// MS's 15-minute access-token cap from ADR 0002 D8 / authentication-
	// flows.md §2.
	AccessTokenTTL = 15 * time.Minute
)

// AuditEmitter is the audit hook the API layer wires in. We emit
// auth.session_started (success) and auth.session_started outcome=failure
// (per ADR 0006 D1) so the recorder's chained audit log records every
// recorder-local login attempt.
type AuditEmitter func(action, outcome, actorKind, actorID string, attrs map[string]string)

// Manager handles recorder-local LocalUser operations: bootstrap admin
// seeding on first start, login (POST /v1/recorder/login), forced
// password change, and JWT issuance + validation.
type Manager struct {
	store      *store.Store
	signingKey *SigningKey
	identityDir string
	tenantID   string
	recorderID string
	emit       AuditEmitter
}

// New constructs a Manager. tenantID is the recorder's bootstrap tenantId
// (per ADR 0011 D2's tenant_id claim), recorderID is the recorder's
// UUIDv7. emit may be nil during early init; set it via SetAuditEmitter
// once the API layer is wired.
func New(s *store.Store, signingKey *SigningKey, identityDir, tenantID, recorderID string) *Manager {
	return &Manager{
		store:       s,
		signingKey:  signingKey,
		identityDir: identityDir,
		tenantID:    tenantID,
		recorderID:  recorderID,
	}
}

// SetAuditEmitter wires the audit emit hook. Called by Core after the
// API layer is constructed and the audit chain is open.
func (m *Manager) SetAuditEmitter(e AuditEmitter) {
	m.emit = e
}

// SigningKID returns the kid of the signing key. Surfaced for the
// /.well-known/jwks-style endpoint if/when added.
func (m *Manager) SigningKID() string {
	return m.signingKey.KID
}

// SigningKey returns the underlying signing key (for JWT validation in
// the auth manager).
func (m *Manager) SigningKey() *SigningKey {
	return m.signingKey
}

// TenantID returns the recorder's bootstrap tenant id (passed to JWT
// `tenant_id` at issuance).
func (m *Manager) TenantID() string {
	return m.tenantID
}

// RecordCount returns the number of LocalUser rows. Used by the
// SetupWizard to detect whether the recorder already has a configured
// admin (zero rows = first boot).
func (m *Manager) RecordCount(ctx context.Context) (int, error) {
	return m.store.LocalUsers.Count(ctx)
}

// BootstrapIfEmpty creates the bootstrap admin user if no LocalUser
// rows exist. Returns the auto-generated initial password when a new
// admin was created, "" when the table was already populated.
//
// The password is also written to <identityDir>/initial-admin-password.txt
// (mode 0600) for operators who missed the startup-log banner. Caller
// (Core) handles the loud log emission.
func (m *Manager) BootstrapIfEmpty(ctx context.Context) (initialPassword string, err error) {
	n, err := m.store.LocalUsers.Count(ctx)
	if err != nil {
		return "", fmt.Errorf("count local users: %w", err)
	}
	if n > 0 {
		return "", nil
	}
	pw, err := generateInitialPassword()
	if err != nil {
		return "", fmt.Errorf("generate initial password: %w", err)
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return "", fmt.Errorf("hash initial password: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate admin uuid: %w", err)
	}
	if err := m.store.LocalUsers.Insert(ctx, &store.LocalUser{
		ID:                 id.String(),
		Username:           "admin",
		DisplayName:        "Bootstrap Admin",
		PasswordHash:       hash,
		IsAdmin:            true,
		IsActive:           true,
		MustChangePassword: true,
	}); err != nil {
		return "", fmt.Errorf("insert admin: %w", err)
	}
	if err := writeFileAtomic(
		filepath.Join(m.identityDir, initialAdminPasswordFile),
		[]byte(pw+"\n"), 0o600,
	); err != nil {
		// Non-fatal: the password is also surfaced to the caller for
		// log emission. We log the write failure but don't abort
		// bootstrap because the admin row is already in place.
		return pw, fmt.Errorf("write initial-admin-password.txt: %w", err)
	}
	return pw, nil
}

// generateInitialPassword produces a 24-character URL-safe random
// string. ~143 bits of entropy — overkill for a one-shot bootstrap
// password that the operator immediately rotates.
func generateInitialPassword() (string, error) {
	const n = 18 // 18 bytes → 24 chars in base64-url-no-pad
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// LoginResult is what /v1/recorder/login returns on success.
type LoginResult struct {
	Token              string
	ExpiresAt          time.Time
	UserID             string
	Username           string
	IsAdmin            bool
	MustChangePassword bool
	Scope              []string
}

// Sentinel login errors.
var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrAccountLocked       = errors.New("account locked")
	ErrAccountInactive     = errors.New("account inactive")
	ErrPasswordTooShort    = errors.New("password too short")
)

// MinPasswordLength is the recorder-local minimum.
const MinPasswordLength = 8

// Login authenticates a LocalUser and issues a JWT.
//
// On success: emits auth.session_started outcome=success.
// On bad credentials / locked / inactive: emits outcome=failure.
func (m *Manager) Login(ctx context.Context, username, password, sourceIP string) (*LoginResult, error) {
	user, err := m.store.LocalUsers.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			m.emitFailure(username, sourceIP, "unknown_user")
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("look up user: %w", err)
	}
	if !user.IsActive {
		m.emitFailure(username, sourceIP, "inactive")
		return nil, ErrAccountInactive
	}
	if !user.LockedUntil.IsZero() && user.LockedUntil.After(time.Now()) {
		m.emitFailure(username, sourceIP, "locked")
		return nil, ErrAccountLocked
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		_ = m.store.LocalUsers.RecordFailedLogin(ctx, user.ID, LockThreshold, LockDuration)
		m.emitFailure(username, sourceIP, "wrong_password")
		return nil, ErrInvalidCredentials
	}
	_ = m.store.LocalUsers.RecordSuccessfulLogin(ctx, user.ID)

	tok, exp, scope, err := m.issueToken(user)
	if err != nil {
		return nil, fmt.Errorf("issue token: %w", err)
	}
	m.emitSuccess(user.ID, username, sourceIP)
	return &LoginResult{
		Token:              tok,
		ExpiresAt:          exp,
		UserID:             user.ID,
		Username:           user.Username,
		IsAdmin:            user.IsAdmin,
		MustChangePassword: user.MustChangePassword,
		Scope:              scope,
	}, nil
}

// ChangePassword performs the forced-rotation flow. Verifies the
// current password, hashes the new one, persists it, clears
// must_change_password, and re-issues a JWT.
func (m *Manager) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (*LoginResult, error) {
	if len(newPassword) < MinPasswordLength {
		return nil, ErrPasswordTooShort
	}
	user, err := m.store.LocalUsers.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrAccountInactive
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, currentPassword)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	if err := m.store.LocalUsers.SetPassword(ctx, user.ID, newHash); err != nil {
		return nil, err
	}
	// Re-fetch so MustChangePassword is current.
	user, err = m.store.LocalUsers.GetByID(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	tok, exp, scope, err := m.issueToken(user)
	if err != nil {
		return nil, err
	}
	// Drop the bootstrap initial-password file once it's been rotated:
	// the password it documents is no longer valid.
	_ = os.Remove(filepath.Join(m.identityDir, initialAdminPasswordFile))
	return &LoginResult{
		Token:              tok,
		ExpiresAt:          exp,
		UserID:             user.ID,
		Username:           user.Username,
		IsAdmin:            user.IsAdmin,
		MustChangePassword: user.MustChangePassword,
		Scope:              scope,
	}, nil
}

// AdminPermissions is the permission set the bootstrap admin's JWT
// carries — the full ADR 0010 admin role. Mirrors
// management/internal/auth.RoleAdmin permissions exactly because the
// recorder's per-endpoint requirePermission middleware (slice 4-D)
// gates against the same string set.
//
// Kept here as a literal slice so the recorder doesn't depend on the
// management module. The strings must match the gates registered in
// internal/api/api.go's route table.
func AdminPermissions() []string {
	return []string{
		"audit.read",
		"camera.create",
		"camera.delete",
		"camera.list",
		"camera.live.view",
		"camera.read",
		"camera.update",
		"clip.create",
		"clip.delete",
		"clip.download",
		"clip.export",
		"clip.list",
		"clip.read",
		"device_lifecycle.manage",
		"event.list",
		"event.read",
		"health.read",
		"policy.create",
		"policy.delete",
		"policy.list",
		"policy.read",
		"policy.update",
		"recorder_config.manage",
		"recorder_config.read",
		"recording.delete",
		"recording.list",
		"recording.pii.read",
		"recording.playback",
		"recording.read",
		"recording_segment.delete",
		"recording_segment.list",
		"recording_segment.read",
		"role.assign",
		"role.manage",
		"session.pii.read",
		"software_update.manage",
		"storage.list",
		"storage.manage",
		"storage.read",
		"stream.kick",
		"stream.list",
		"stream.read",
		"user.invite",
		"user.list",
		"user.manage",
		"user.read",
	}
}

// issueToken signs a JWT for the given LocalUser.
//
// Claim shape mirrors what the MS produces for operator JWTs (see
// management/internal/auth/auth.go's buildAccessClaims) so the recorder's
// existing JWT-validation path (auth.Manager.authenticateJWTWithClaims)
// + slice 4-D's principal building handle local + MS tokens identically.
//
// The mediamtx_permissions claim is required by the recorder's
// JWT-claims unmarshaller (auth/jwt_claims.go); it carries the same
// permission strings as scope but in mediamtx's path/action shape so
// the path/action gates (publish/read/playback/api) accept the token.
func (m *Manager) issueToken(user *store.LocalUser) (string, time.Time, []string, error) {
	now := time.Now().UTC()
	exp := now.Add(AccessTokenTTL)
	scope := AdminPermissions() // recorder-local users are always bootstrap admin in v1
	if !user.IsAdmin {
		// Defensive: if a non-admin user ever exists, issue a viewer-
		// only token. Future slices may grow this surface; for v1 the
		// bootstrap admin is the only path.
		scope = []string{"camera.list", "camera.read", "health.read"}
	}

	jti := uuid.NewString()
	issuer := "recorder/" + m.recorderID
	audience := "recording_server/" + m.recorderID

	mediamtxPerms := mediamtxPermissionsForScope(scope)

	claims := jwt.MapClaims{
		"iss":                  issuer,
		"sub":                  user.ID,
		"aud":                  audience,
		"iat":                  now.Unix(),
		"exp":                  exp.Unix(),
		"jti":                  jti,
		"typ":                  "access",
		"username":             user.Username,
		"is_admin":             user.IsAdmin,
		"principal_kind":       "local_user",
		"tenant_id":            m.tenantID,
		"scope":                scope,
		"scope_kind":           "tenant",
		"scope_target_id":      m.tenantID,
		"client_fingerprint":   "",
		"mediamtx_permissions": mediamtxPerms,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = m.signingKey.KID
	signed, err := tok.SignedString(m.signingKey.Priv)
	if err != nil {
		return "", time.Time{}, nil, err
	}
	return signed, exp, scope, nil
}

// mediamtxPermissionsForScope translates recorder-permission strings
// (ADR 0010 D2) into the mediamtx_permissions claim shape the recorder's
// auth manager expects. The minimum set needed for /v1/* access is the
// `api` action (which the api/metrics/pprof routes match against).
//
// We grant `api` unconditionally for any recorder-local user (admin
// gate is enforced at the per-endpoint requirePermission level via
// the `scope` claim, not here). For media-pipeline access (publish/
// read/playback) we project the recorder-permission set onto the
// matching mediamtx actions when present in scope.
func mediamtxPermissionsForScope(scope []string) []map[string]string {
	out := []map[string]string{
		{"action": "api"},
	}
	has := func(p string) bool {
		for _, s := range scope {
			if s == p {
				return true
			}
		}
		return false
	}
	if has("camera.live.view") || has("recording.playback") {
		out = append(out, map[string]string{"action": "playback"})
	}
	if has("stream.read") || has("camera.live.view") {
		out = append(out, map[string]string{"action": "read"})
	}
	if has("camera.create") || has("camera.update") {
		out = append(out, map[string]string{"action": "publish"})
	}
	return out
}

// VerifyToken validates a recorder-local JWT against this manager's
// signing key. Returns the parsed claims on success.
func (m *Manager) VerifyToken(token string) (jwt.MapClaims, error) {
	parsed, err := jwt.Parse(token,
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return &m.signingKey.Priv.PublicKey, nil
		},
		jwt.WithIssuer("recorder/"+m.recorderID),
		jwt.WithAudience("recording_server/"+m.recorderID),
	)
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func (m *Manager) emitSuccess(userID, username, sourceIP string) {
	if m.emit == nil {
		return
	}
	m.emit("auth.session_started", "success", "local_user", userID, map[string]string{
		"username":   username,
		"source_ip":  sourceIP,
		"issuer":     "recorder/" + m.recorderID,
	})
}

func (m *Manager) emitFailure(username, sourceIP, reason string) {
	if m.emit == nil {
		return
	}
	m.emit("auth.session_started", "failure", "unauthenticated", "", map[string]string{
		"username":  username,
		"source_ip": sourceIP,
		"reason":    reason,
	})
}

// Issuer returns the JWT iss claim used by recorder-local tokens.
// Surfaced so the recorder's auth.Manager can branch on iss when
// validating (recorder-local vs MS-issued).
func (m *Manager) Issuer() string {
	return "recorder/" + m.recorderID
}

// Audience returns the JWT aud claim used by recorder-local tokens.
func (m *Manager) Audience() string {
	return "recording_server/" + m.recorderID
}

// SanitizeUsername normalizes the username field on incoming login
// requests. Trim whitespace; lowercase is intentionally NOT applied
// (case-sensitive matches the LocalUsersRepo.GetByUsername query).
func SanitizeUsername(s string) string {
	return strings.TrimSpace(s)
}
