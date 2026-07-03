package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustOpenStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "recorder.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_AppliesMigrations(t *testing.T) {
	s := mustOpenStore(t)
	// Verify the local_users table exists by counting rows.
	n, err := s.LocalUsers.Count(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected empty local_users; got %d", n)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recorder.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	_ = s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()
}

func TestLocalUsers_InsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{
		ID:                 uuid.NewString(),
		Username:           "admin",
		DisplayName:        "Bootstrap Admin",
		PasswordHash:       "$argon2id$v=19$m=65536,t=3,p=2$AAAA$BBBB",
		IsAdmin:            true,
		IsActive:           true,
		MustChangePassword: true,
	}
	if err := s.LocalUsers.Insert(ctx, u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.LocalUsers.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("get by username: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID: got %s want %s", got.ID, u.ID)
	}
	if got.DisplayName != "Bootstrap Admin" {
		t.Errorf("DisplayName: got %q", got.DisplayName)
	}
	if !got.IsAdmin || !got.IsActive || !got.MustChangePassword {
		t.Errorf("flags wrong: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps zero: %+v", got)
	}

	// GetByID also works.
	g2, err := s.LocalUsers.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if g2.Username != "admin" {
		t.Errorf("get by id mismatch: %+v", g2)
	}
}

func TestLocalUsers_DuplicateUsernameRejected(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{ID: uuid.NewString(), Username: "x", PasswordHash: "h", IsActive: true}
	if err := s.LocalUsers.Insert(ctx, u); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	u2 := &LocalUser{ID: uuid.NewString(), Username: "x", PasswordHash: "h", IsActive: true}
	err := s.LocalUsers.Insert(ctx, u2)
	if !errors.Is(err, ErrLocalUserExists) {
		t.Fatalf("expected ErrLocalUserExists; got %v", err)
	}
}

func TestLocalUsers_GetByUsername_NotFound(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.LocalUsers.GetByUsername(context.Background(), "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows; got %v", err)
	}
}

func TestLocalUsers_SetPasswordClearsMustChange(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{
		ID: uuid.NewString(), Username: "admin", PasswordHash: "old",
		IsAdmin: true, IsActive: true, MustChangePassword: true,
	}
	if err := s.LocalUsers.Insert(ctx, u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.LocalUsers.SetPassword(ctx, u.ID, "newhash"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	got, _ := s.LocalUsers.GetByID(ctx, u.ID)
	if got.MustChangePassword {
		t.Errorf("must_change_password should be cleared")
	}
	if got.PasswordHash != "newhash" {
		t.Errorf("hash not updated: %s", got.PasswordHash)
	}
}

func TestLocalUsers_RecordLoginsAndLockout(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{ID: uuid.NewString(), Username: "u", PasswordHash: "h", IsActive: true}
	if err := s.LocalUsers.Insert(ctx, u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := s.LocalUsers.RecordFailedLogin(ctx, u.ID, 5, time.Minute); err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
	}
	got, _ := s.LocalUsers.GetByID(ctx, u.ID)
	if got.FailedLoginAttempts != 4 {
		t.Errorf("failed counter: got %d", got.FailedLoginAttempts)
	}
	if !got.LockedUntil.IsZero() {
		t.Errorf("should not be locked yet")
	}
	// 5th failure crosses threshold.
	if err := s.LocalUsers.RecordFailedLogin(ctx, u.ID, 5, time.Minute); err != nil {
		t.Fatalf("5th fail: %v", err)
	}
	got, _ = s.LocalUsers.GetByID(ctx, u.ID)
	if got.LockedUntil.IsZero() || got.LockedUntil.Before(time.Now()) {
		t.Errorf("expected locked_until in the future; got %v", got.LockedUntil)
	}
	// Successful login clears counter + lock.
	if err := s.LocalUsers.RecordSuccessfulLogin(ctx, u.ID); err != nil {
		t.Fatalf("success: %v", err)
	}
	got, _ = s.LocalUsers.GetByID(ctx, u.ID)
	if got.FailedLoginAttempts != 0 {
		t.Errorf("counter not cleared")
	}
	if !got.LockedUntil.IsZero() {
		t.Errorf("locked_until not cleared")
	}
	if got.LastLoginAt.IsZero() {
		t.Errorf("last_login_at not set")
	}
}

func TestLocalUsers_UpdateProfileAndSoftDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{
		ID: uuid.NewString(), Username: "u", PasswordHash: "h", IsActive: true,
	}
	_ = s.LocalUsers.Insert(ctx, u)
	if err := s.LocalUsers.UpdateProfile(ctx, u.ID, "New Name", false); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.LocalUsers.GetByID(ctx, u.ID)
	if got.DisplayName != "New Name" || got.IsActive {
		t.Errorf("update didn't take: %+v", got)
	}

	if err := s.LocalUsers.SoftDelete(ctx, u.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// Update on non-existent id returns ErrLocalUserNotFound.
	err := s.LocalUsers.UpdateProfile(ctx, "missing-id", "x", true)
	if !errors.Is(err, ErrLocalUserNotFound) {
		t.Errorf("expected ErrLocalUserNotFound; got %v", err)
	}
}

func TestLocalUsers_CountAndListAll(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = s.LocalUsers.Insert(ctx, &LocalUser{
			ID: uuid.NewString(), Username: "u" + string(rune('0'+i)),
			PasswordHash: "h", IsActive: i%2 == 0,
		})
	}
	n, _ := s.LocalUsers.Count(ctx)
	if n != 3 {
		t.Errorf("count: got %d", n)
	}
	a, _ := s.LocalUsers.CountActive(ctx)
	if a != 2 { // i=0,2
		t.Errorf("count active: got %d", a)
	}
	all, _ := s.LocalUsers.ListAll(ctx)
	if len(all) != 3 {
		t.Errorf("list len: got %d", len(all))
	}
}

func TestLocalUsers_RoleEmailLanguage(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{
		ID:           uuid.NewString(),
		Username:     "alice",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$AAAA$BBBB",
		IsActive:     true,
		RoleID:       "role_admin",
		Email:        "alice@example.com",
		Language:     "en",
	}
	if err := s.LocalUsers.Insert(ctx, u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.LocalUsers.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RoleID != "role_admin" || got.Email != "alice@example.com" || got.Language != "en" {
		t.Errorf("got role=%q email=%q lang=%q", got.RoleID, got.Email, got.Language)
	}

	if err := s.LocalUsers.SetRole(ctx, u.ID, "role_viewer"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := s.LocalUsers.SetEmail(ctx, u.ID, "alice2@example.com"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if err := s.LocalUsers.SetLanguage(ctx, u.ID, "es"); err != nil {
		t.Fatalf("set lang: %v", err)
	}
	got, _ = s.LocalUsers.GetByID(ctx, u.ID)
	if got.RoleID != "role_viewer" || got.Email != "alice2@example.com" || got.Language != "es" {
		t.Errorf("post-update got role=%q email=%q lang=%q", got.RoleID, got.Email, got.Language)
	}
}
