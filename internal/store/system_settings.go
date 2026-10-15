package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// SystemSetting is a row in system_settings.
type SystemSetting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string // empty when system-set
}

// SystemSettingsRepo provides k/v access to system_settings plus typed
// convenience accessors that tolerate missing/malformed rows.
type SystemSettingsRepo struct {
	db *sql.DB
}

// ErrSystemSettingNotFound is returned by Get when no row matches.
var ErrSystemSettingNotFound = errors.New("system setting not found")

// Get fetches a setting by key.
func (r *SystemSettingsRepo) Get(ctx context.Context, key string) (*SystemSetting, error) {
	row := r.db.QueryRowContext(ctx, systemSettingSelect+` WHERE key = ?`, key)
	s, err := scanSystemSetting(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	return s, err
}

// GetAll returns every setting as a key->value map for warm-cache builders.
func (r *SystemSettingsRepo) GetAll(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM system_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Upsert inserts or replaces a single setting.
func (r *SystemSettingsRepo) Upsert(ctx context.Context, key, value, updatedBy string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO system_settings (key, value, updated_at, updated_by)
		VALUES (?, ?, ?, NULLIF(?, ''))
		ON CONFLICT(key) DO UPDATE SET
		    value = excluded.value,
		    updated_at = excluded.updated_at,
		    updated_by = excluded.updated_by
	`, key, value, Now(), updatedBy)
	return err
}

// UpsertMany applies a batch of settings atomically.
func (r *SystemSettingsRepo) UpsertMany(ctx context.Context, kv map[string]string, updatedBy string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := Now()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO system_settings (key, value, updated_at, updated_by)
			VALUES (?, ?, ?, NULLIF(?, ''))
			ON CONFLICT(key) DO UPDATE SET
			    value = excluded.value,
			    updated_at = excluded.updated_at,
			    updated_by = excluded.updated_by
		`, k, v, now, updatedBy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetInt returns the integer value of key, falling back to defaultVal
// when the row is missing or its value cannot be parsed.
func (r *SystemSettingsRepo) GetInt(ctx context.Context, key string, defaultVal int) (int, error) {
	s, err := r.Get(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultVal, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(s.Value)
	if err != nil {
		return defaultVal, nil
	}
	return n, nil
}

// GetDuration returns the time.Duration value of key, falling back when
// missing or unparseable.
func (r *SystemSettingsRepo) GetDuration(ctx context.Context, key string, defaultVal time.Duration) (time.Duration, error) {
	s, err := r.Get(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultVal, nil
	}
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(s.Value)
	if err != nil {
		return defaultVal, nil
	}
	return d, nil
}

// GetBool returns the bool value of key, falling back when missing or
// unparseable.
func (r *SystemSettingsRepo) GetBool(ctx context.Context, key string, defaultVal bool) (bool, error) {
	s, err := r.Get(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultVal, nil
	}
	if err != nil {
		return false, err
	}
	b, err := strconv.ParseBool(s.Value)
	if err != nil {
		return defaultVal, nil
	}
	return b, nil
}

const systemSettingSelect = `
SELECT key, value, updated_at, COALESCE(updated_by, '')
FROM system_settings
`

func scanSystemSetting(row rowScanner) (*SystemSetting, error) {
	var s SystemSetting
	var updatedAt string
	if err := row.Scan(&s.Key, &s.Value, &updatedAt, &s.UpdatedBy); err != nil {
		return nil, err
	}
	t, err := ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	s.UpdatedAt = t
	return &s, nil
}
