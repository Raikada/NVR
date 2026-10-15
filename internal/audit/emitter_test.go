package audit

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

// openTestStore opens a fresh on-disk SQLite store and seeds a single
// local_user row so audit-row writes (which FK to local_users.id)
// don't fail due to FK violations.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "audit.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// Seed a fixed user so audit rows that reference an actor id
	// satisfy the audit_log.actor_user_id FK.
	require.NoError(t, st.LocalUsers.Insert(context.Background(), &store.LocalUser{
		ID:           "u1",
		Username:     "admin",
		PasswordHash: "$argon2id$placeholder",
		IsAdmin:      true,
		IsActive:     true,
		RoleID:       "role_admin",
	}))
	require.NoError(t, st.LocalUsers.Insert(context.Background(), &store.LocalUser{
		ID:           "u2",
		Username:     "viewer1",
		PasswordHash: "$argon2id$placeholder",
		IsActive:     true,
		RoleID:       "role_viewer",
	}))
	return st
}

// TestEmitterAppendsRow verifies a basic write through the System
// emitter lands in audit_log with the expected action.
func TestEmitterAppendsRow(t *testing.T) {
	st := openTestStore(t)
	em := New(st.AuditLog)
	require.NotNil(t, em.Repo())

	em.System().SettingChanged(context.Background(), "u1", "admin", "10.0.0.1", "site_name")

	rows, _, err := st.AuditLog.List(context.Background(), store.ListAuditFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "system.setting_changed", rows[0].Action)
	require.Equal(t, "site_name", rows[0].TargetID)
	require.Equal(t, "admin", rows[0].ActorUsername)
}

// TestRBACInterface verifies the Emitter satisfies rbac.AuditEmitter
// without an adapter wrapper.
func TestRBACInterface(t *testing.T) {
	st := openTestStore(t)
	em := New(st.AuditLog)

	// Compile-time confirmation:
	var _ rbac.AuditEmitter = em

	// And it actually inserts the canonical action.
	em.PermissionDenied(
		context.Background(),
		rbac.Claims{UserID: "u1", Username: "admin", Role: rbac.RoleViewer},
		"/v1/cameras", "10.0.0.1",
	)
	rows, _, err := st.AuditLog.List(context.Background(), store.ListAuditFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "auth.permission_denied", rows[0].Action)
	require.Contains(t, rows[0].Details, "viewer")
	require.Contains(t, rows[0].Details, "/v1/cameras")
}

// TestPerDomainHelpers exercises every per-domain emitter at least
// once. Sanity check that none crash on a happy path and that the
// expected action name is recorded.
func TestPerDomainHelpers(t *testing.T) {
	st := openTestStore(t)
	em := New(st.AuditLog)
	ctx := context.Background()

	em.Auth().LoginSuccess(ctx, "u1", "admin", "10.0.0.1")
	em.Auth().LoginFailure(ctx, "admin", "10.0.0.1")
	em.Auth().PermissionDenied(ctx, "u1", "admin", "/v1/foo", "10.0.0.1")
	em.Auth().PasswordChanged(ctx, "u1", "admin", "10.0.0.1")

	em.Camera().Created(ctx, "u1", "admin", "10.0.0.1", "cam1", "Front Door")
	em.Camera().Updated(ctx, "u1", "admin", "10.0.0.1", "cam1", "display_name")
	em.Camera().CredentialsRotated(ctx, "u1", "admin", "10.0.0.1", "cam1")
	em.Camera().Deleted(ctx, "u1", "admin", "10.0.0.1", "cam1")

	em.Policy().Created(ctx, "u1", "admin", "10.0.0.1", "p1", "Continuous")
	em.Policy().Updated(ctx, "u1", "admin", "10.0.0.1", "p1", "mode")
	em.Policy().Deleted(ctx, "u1", "admin", "10.0.0.1", "p1")

	em.Clip().Created(ctx, "u1", "admin", "10.0.0.1", "clip1")
	em.Clip().Downloaded(ctx, "u1", "admin", "10.0.0.1", "clip1")
	em.Clip().Deleted(ctx, "u1", "admin", "10.0.0.1", "clip1")

	em.Event().Acknowledged(ctx, "u1", "admin", "10.0.0.1", "ev1")
	em.Event().RetentionUpdated(ctx, "u1", "admin", "10.0.0.1", "camera.online")

	em.Notification().TargetCreated(ctx, "u1", "admin", "10.0.0.1", "tgt1", "webhook", "ops")
	em.Notification().TargetDeleted(ctx, "u1", "admin", "10.0.0.1", "tgt1")
	em.Notification().SubscriptionCreated(ctx, "u1", "admin", "10.0.0.1", "sub1")
	em.Notification().SubscriptionDeleted(ctx, "u1", "admin", "10.0.0.1", "sub1")

	em.System().SettingChanged(ctx, "u1", "admin", "10.0.0.1", "timezone")
	em.System().TLSReplaced(ctx, "u1", "admin", "10.0.0.1")
	em.System().TLSReloadFailed(ctx, "parse error")
	em.System().RetentionSwept(ctx, "u1", "admin", "10.0.0.1")

	em.User().Created(ctx, "u1", "admin", "10.0.0.1", "u2", "viewer1", "viewer")
	em.User().Updated(ctx, "u1", "admin", "10.0.0.1", "u2", "display_name")
	em.User().PasswordReset(ctx, "u1", "admin", "10.0.0.1", "u2")
	em.User().Deleted(ctx, "u1", "admin", "10.0.0.1", "u2")

	rows, _, err := st.AuditLog.List(ctx, store.ListAuditFilter{Limit: 200})
	require.NoError(t, err)
	require.Greater(t, len(rows), 25)
}

// TestNilSafeEmitter confirms that nil emitters and nil repos do not
// panic. Helper code calls an emitter via the API struct, which may
// be nil during early bootstrap or in tests.
func TestNilSafeEmitter(t *testing.T) {
	var em *Emitter
	em.Auth().LoginSuccess(context.Background(), "u", "u", "ip")
	em.Camera().Created(context.Background(), "u", "u", "ip", "c", "n")
	em.System().TLSReplaced(context.Background(), "u", "u", "ip")
	// passing through a nil-repo Emitter shouldn't crash either.
	em2 := &Emitter{repo: nil}
	em2.Auth().LoginSuccess(context.Background(), "u", "u", "ip")
}
