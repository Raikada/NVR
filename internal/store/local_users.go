package store

import (
	"context"
	"database/sql"
	"time"
)

// LocalUser is a row in local_users. Mirrors management's shape with
// the addition of MustChangePassword (the bootstrap-admin forced-
// rotation flag).
type LocalUser struct {
	ID                  string
	Username            string
	DisplayName         string // optional; defaults to Username when empty
	PasswordHash        string
	IsAdmin             bool   // legacy; prefer RoleID
	IsActive            bool
	MustChangePassword  bool
	RoleID              string // FK roles.id; empty when unassigned
	Email               string // optional SMTP recipient
	Language            string // user-preferred language (ISO 639-1); defaults "en"
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastLoginAt         time.Time // zero if never logged in
	FailedLoginAttempts int
	LockedUntil         time.Time // zero if not locked
}

// LocalUsersRepo provides CRUD over local_users.
type LocalUsersRepo struct {
	db *sql.DB
}

// Insert adds a new local user.
func (r *LocalUsersRepo) Insert(ctx context.Context, u *LocalUser) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = now
	}
	if u.Language == "" {
		u.Language = "en"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO local_users (id, username, display_name, password_hash,
		                          is_admin, is_active, must_change_password,
		                          role_id, email, language,
		                          created_at, updated_at,
		                          failed_login_attempts)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, 0)
	`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash,
		boolToInt(u.IsAdmin), boolToInt(u.IsActive), boolToInt(u.MustChangePassword),
		u.RoleID, u.Email, u.Language,
		FormatTime(u.CreatedAt), FormatTime(u.UpdatedAt),
	)
	if err != nil && isConstraintErr(err) {
		return ErrLocalUserExists
	}
	return err
}

// GetByUsername fetches the user matching username (case-sensitive).
// Returns sql.ErrNoRows if not found.
func (r *LocalUsersRepo) GetByUsername(ctx context.Context, username string) (*LocalUser, error) {
	row := r.db.QueryRowContext(ctx, localUserSelect+`
		WHERE username = ?
	`, username)
	return scanLocalUser(row)
}

// GetByID fetches by id. Returns sql.ErrNoRows if not found.
func (r *LocalUsersRepo) GetByID(ctx context.Context, id string) (*LocalUser, error) {
	row := r.db.QueryRowContext(ctx, localUserSelect+`
		WHERE id = ?
	`, id)
	return scanLocalUser(row)
}

// Count returns the total number of rows in local_users (active + inactive).
// Used by the bootstrap path to decide whether to seed an admin user.
func (r *LocalUsersRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_users`).Scan(&n)
	return n, err
}

// CountActive returns the number of active local users.
func (r *LocalUsersRepo) CountActive(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_users WHERE is_active = 1`).Scan(&n)
	return n, err
}

// ListAll returns every LocalUser, active or not.
func (r *LocalUsersRepo) ListAll(ctx context.Context) ([]*LocalUser, error) {
	rows, err := r.db.QueryContext(ctx, localUserSelect+`
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LocalUser
	for rows.Next() {
		u, err := scanLocalUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateProfile updates display_name + is_active.
func (r *LocalUsersRepo) UpdateProfile(ctx context.Context, id, displayName string, isActive bool) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users
		SET display_name = NULLIF(?, ''), is_active = ?, updated_at = ?
		WHERE id = ?
	`, displayName, boolToInt(isActive), Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

// SoftDelete marks the user inactive.
func (r *LocalUsersRepo) SoftDelete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET is_active = 0, updated_at = ? WHERE id = ?
	`, Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

// RecordSuccessfulLogin clears failed_login_attempts and sets last_login_at.
func (r *LocalUsersRepo) RecordSuccessfulLogin(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE local_users
		SET last_login_at = ?, failed_login_attempts = 0, locked_until = NULL
		WHERE id = ?
	`, Now(), id)
	return err
}

// RecordFailedLogin increments the failed-login counter and locks the
// account if the threshold is reached.
func (r *LocalUsersRepo) RecordFailedLogin(ctx context.Context, id string, lockThreshold int, lockDuration time.Duration) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE local_users
		SET failed_login_attempts = failed_login_attempts + 1,
		    locked_until = CASE
		      WHEN failed_login_attempts + 1 >= ? THEN ?
		      ELSE locked_until
		    END
		WHERE id = ?
	`, lockThreshold, FormatTime(time.Now().Add(lockDuration)), id)
	return err
}

// SetPassword updates password_hash and clears must_change_password.
func (r *LocalUsersRepo) SetPassword(ctx context.Context, id, hash string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET password_hash = ?, must_change_password = 0,
		                       updated_at = ?
		WHERE id = ?
	`, hash, Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

const localUserSelect = `
SELECT id, username, COALESCE(display_name, ''), password_hash, is_admin,
       is_active, must_change_password,
       COALESCE(role_id, ''), COALESCE(email, ''), language,
       created_at, updated_at, COALESCE(last_login_at, ''),
       failed_login_attempts, COALESCE(locked_until, '')
FROM local_users
`

func scanLocalUser(row rowScanner) (*LocalUser, error) {
	var u LocalUser
	var createdAt, updatedAt, lastLoginAt, lockedUntil string
	var isAdmin, isActive, mustChange int
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&isAdmin, &isActive, &mustChange,
		&u.RoleID, &u.Email, &u.Language,
		&createdAt, &updatedAt, &lastLoginAt,
		&u.FailedLoginAttempts, &lockedUntil); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.IsActive = isActive != 0
	u.MustChangePassword = mustChange != 0
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = t
	t, err = ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	u.UpdatedAt = t
	if lastLoginAt != "" {
		t, _ = ParseTime(lastLoginAt)
		u.LastLoginAt = t
	}
	if lockedUntil != "" {
		t, _ = ParseTime(lockedUntil)
		u.LockedUntil = t
	}
	return &u, nil
}

// SetRole updates role_id (must be a valid roles.id, FK enforced).
func (r *LocalUsersRepo) SetRole(ctx context.Context, id, roleID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET role_id = ?, updated_at = ? WHERE id = ?
	`, roleID, Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

// SetEmail updates the user's email (empty string clears it).
func (r *LocalUsersRepo) SetEmail(ctx context.Context, id, email string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET email = NULLIF(?, ''), updated_at = ? WHERE id = ?
	`, email, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

// SetLanguage updates the user's language preference.
func (r *LocalUsersRepo) SetLanguage(ctx context.Context, id, lang string) error {
	if lang == "" {
		lang = "en"
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET language = ?, updated_at = ? WHERE id = ?
	`, lang, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}
