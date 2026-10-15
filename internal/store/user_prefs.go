// Package store: per-user UI preference rows (Phase A B6).
package store

import (
	"context"
	"database/sql"
)

// UserPrefsRepo stores opaque per-user JSON preferences.
type UserPrefsRepo struct {
	db *sql.DB
}

// Set upserts one key.
func (r *UserPrefsRepo) Set(ctx context.Context, userID, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_prefs (user_id, key, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, userID, key, value, Now())
	return err
}

// List returns every key→value for the user.
func (r *UserPrefsRepo) List(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM user_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
