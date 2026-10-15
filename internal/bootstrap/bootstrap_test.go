package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/store"
)

// openTestStore opens an on-disk SQLite store under t.TempDir + applies
// migrations.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestRunFreshSeedsAdmin verifies the empty-store path: bootstrap
// inserts the admin row, persists the password file (mode 0600), sets
// must_change_password, and reports the generated password back.
func TestRunFreshSeedsAdmin(t *testing.T) {
	t.Setenv(envBootstrapPassword, "")
	st := openTestStore(t)
	idDir := t.TempDir()

	res, err := Run(context.Background(), st, idDir, Options{})
	require.NoError(t, err)
	require.True(t, res.AdminCreated)
	require.True(t, res.MustChangePassword)
	require.Len(t, res.InitialPassword, 24)

	// Password file must exist with mode 0600.
	pwPath := filepath.Join(idDir, initialAdminPasswordFile)
	info, err := os.Stat(pwPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	body, err := os.ReadFile(pwPath)
	require.NoError(t, err)
	require.Contains(t, string(body), res.InitialPassword)

	// User row must verify against the generated password.
	u, err := st.LocalUsers.GetByUsername(context.Background(), "admin")
	require.NoError(t, err)
	require.True(t, u.IsAdmin)
	require.True(t, u.MustChangePassword)
	require.Equal(t, "role_admin", u.RoleID)
	ok, err := auth.VerifyPassword(u.PasswordHash, res.InitialPassword)
	require.NoError(t, err)
	require.True(t, ok)
}

// TestRunHonorsEnvVar verifies the env-var path: password is taken from
// RAIKADA_BOOTSTRAP_PASSWORD, must_change_password is false, and no
// password file is written.
func TestRunHonorsEnvVar(t *testing.T) {
	t.Setenv(envBootstrapPassword, "operator-supplied-secret")
	st := openTestStore(t)
	idDir := t.TempDir()

	res, err := Run(context.Background(), st, idDir, Options{})
	require.NoError(t, err)
	require.True(t, res.AdminCreated)
	require.False(t, res.MustChangePassword)
	require.Empty(t, res.InitialPassword)

	pwPath := filepath.Join(idDir, initialAdminPasswordFile)
	_, err = os.Stat(pwPath)
	require.True(t, os.IsNotExist(err), "env-var path must not write a password file")

	// User row authenticates against the supplied password.
	u, err := st.LocalUsers.GetByUsername(context.Background(), "admin")
	require.NoError(t, err)
	require.False(t, u.MustChangePassword)
	ok, err := auth.VerifyPassword(u.PasswordHash, "operator-supplied-secret")
	require.NoError(t, err)
	require.True(t, ok)
}

// TestRunNoOpWhenAdminExists verifies the existing-admin path: bootstrap
// is a no-op when local_users already has rows.
func TestRunNoOpWhenAdminExists(t *testing.T) {
	t.Setenv(envBootstrapPassword, "")
	st := openTestStore(t)
	idDir := t.TempDir()

	res, err := Run(context.Background(), st, idDir, Options{})
	require.NoError(t, err)
	require.True(t, res.AdminCreated)

	res2, err := Run(context.Background(), st, idDir, Options{})
	require.NoError(t, err)
	require.False(t, res2.AdminCreated)
	require.Empty(t, res2.InitialPassword)

	// Only one admin row exists.
	n, err := st.LocalUsers.Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

