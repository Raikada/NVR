// Package bootstrap is the canonical first-start seeder for the
// recorder. Run is called once early in core.createResources, after
// store.Open and identity.Open, before any service is wired.
//
// Responsibilities:
//
//   - Seed the bootstrap admin LocalUser when local_users is empty.
//     Honors the RAIKADA_BOOTSTRAP_PASSWORD env var so headless
//     deployments (containers, kiosk installers) can supply a known
//     password without parsing the startup log.
//   - Persist the auto-generated password to
//     <identityDir>/initial-admin-password.txt (mode 0600) so a
//     foreground operator who missed the log banner can still find it.
//     The file is unlinked by the password-rotation handler after the
//     forced first-login change.
//   - Populate runtime-only system_settings (timezone, snapshot_root,
//     clip_root) when the corresponding row is missing.
//
// The package depends only on internal/store + internal/auth so it is
// safe to import from internal/core without dragging in API or
// service dependencies.
package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/store"
)

const (
	// initialAdminPasswordFile is written ONCE on bootstrap so an
	// operator who missed the log banner can still find the auto-
	// generated password. Removed by the password-rotation handler
	// after the forced first-login change.
	initialAdminPasswordFile = "initial-admin-password.txt"

	// envBootstrapPassword lets headless deployments supply a known
	// password. When set + non-empty, the bootstrap admin is created
	// without must_change_password and without writing the password
	// file (caller already knows the password).
	envBootstrapPassword = "RAIKADA_BOOTSTRAP_PASSWORD"

	// roleAdminID is the canonical role id for the bootstrap admin.
	// Mirrors the seed in store/migrations/*_roles.sql.
	roleAdminID = "role_admin"

	// passwordChars is the byte length the random generator pulls; 18
	// bytes -> 24 chars in base64-url-no-pad. ~143 bits of entropy,
	// overkill for a one-shot bootstrap password.
	passwordBytes = 18
)

// Result describes what bootstrap.Run did. Surfaced to the caller so
// core can emit the appropriate startup banner.
type Result struct {
	// AdminCreated is true when the bootstrap admin was inserted
	// during this Run. Existing-admin runs return false.
	AdminCreated bool

	// InitialPassword carries the generated password when AdminCreated
	// is true and the env var was NOT set. Empty otherwise.
	InitialPassword string

	// MustChangePassword reports whether the forced-rotation flag was
	// set on the admin row. False when the env-var path was taken
	// (the operator already knows the password and consents to it).
	MustChangePassword bool

	// SettingsSeeded is the count of system_settings rows the seeder
	// actually wrote (i.e., the row was missing pre-Run).
	SettingsSeeded int
}

// Options carries optional inputs for Run. RecordingsRoot is the
// recorder's configured recordings directory (used to derive the
// default snapshot_root + clip_root); empty disables those defaults.
type Options struct {
	RecordingsRoot string
}

// Run is the canonical first-start seeder. It is idempotent: rerunning
// is safe — only missing rows are inserted.
func Run(ctx context.Context, st *store.Store, identityDir string, opts Options) (Result, error) {
	if st == nil {
		return Result{}, errors.New("bootstrap: nil store")
	}
	if identityDir == "" {
		return Result{}, errors.New("bootstrap: empty identity dir")
	}

	var res Result

	if err := seedAdmin(ctx, st, identityDir, &res); err != nil {
		return res, err
	}
	// System settings seeding (timezone + snapshot_root + clip_root) is
	// added by Task 6.3 in a follow-up commit (see settings.go).
	_ = opts // referenced once Task 6.3's seedSettings ships.
	return res, nil
}

func seedAdmin(ctx context.Context, st *store.Store, identityDir string, res *Result) error {
	n, err := st.LocalUsers.Count(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: count local_users: %w", err)
	}
	if n > 0 {
		return nil
	}

	envPW := os.Getenv(envBootstrapPassword)
	pw := envPW
	mustChange := false
	if pw == "" {
		gen, err := generateInitialPassword()
		if err != nil {
			return fmt.Errorf("bootstrap: gen password: %w", err)
		}
		pw = gen
		mustChange = true
	}

	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("bootstrap: hash password: %w", err)
	}

	adminID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("bootstrap: gen admin id: %w", err)
	}

	if err := st.LocalUsers.Insert(ctx, &store.LocalUser{
		ID:                 adminID.String(),
		Username:           "admin",
		DisplayName:        "Bootstrap Admin",
		PasswordHash:       hash,
		IsAdmin:            true,
		IsActive:           true,
		MustChangePassword: mustChange,
		RoleID:             roleAdminID,
	}); err != nil {
		return fmt.Errorf("bootstrap: insert admin: %w", err)
	}

	res.AdminCreated = true
	res.MustChangePassword = mustChange

	// Only persist the password file when we generated it. When the
	// env var supplies the password the caller already knows it; we
	// must not leave a copy on disk.
	if envPW == "" {
		res.InitialPassword = pw
		filePath := filepath.Join(identityDir, initialAdminPasswordFile)
		if err := writeFileAtomic(filePath, []byte(pw+"\n"), 0o600); err != nil {
			// Non-fatal: the password is also surfaced via res.InitialPassword
			// so the caller can still log it. Return the error so the
			// caller decides whether to abort.
			return fmt.Errorf("bootstrap: write %q: %w", filePath, err)
		}
	}
	return nil
}

// generateInitialPassword produces a 24-character URL-safe random string.
func generateInitialPassword() (string, error) {
	buf := make([]byte, passwordBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// writeFileAtomic writes data via a temp file + rename so readers never
// observe a partial write. Mirrors identity.writeFileAtomic.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
