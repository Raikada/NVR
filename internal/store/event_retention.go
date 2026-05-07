package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DefaultEventRetentionTypeID is the special row id used for the
// fallback retention duration applied to event types without a specific
// retention configuration.
const DefaultEventRetentionTypeID = "__default__"

// EventRetention is a row in event_retention. The special row
// type_id='__default__' is the fallback for event types not explicitly
// listed.
type EventRetention struct {
	TypeID              string
	KeepDurationSeconds int64
	UpdatedAt           time.Time
}

// EventRetentionRepo provides per-type retention duration management.
type EventRetentionRepo struct {
	db *sql.DB
}

// ErrEventRetentionNotFound is returned by Get when no row matches.
var ErrEventRetentionNotFound = errors.New("event retention row not found")

// Get fetches the retention row for a type id. Returns sql.ErrNoRows if
// none; callers may fall back to '__default__'.
func (r *EventRetentionRepo) Get(ctx context.Context, typeID string) (*EventRetention, error) {
	row := r.db.QueryRowContext(ctx, eventRetentionSelect+` WHERE type_id = ?`, typeID)
	return scanEventRetention(row)
}

// GetEffective returns the retention duration for typeID, falling back
// to '__default__' when no row exists for typeID.
func (r *EventRetentionRepo) GetEffective(ctx context.Context, typeID string) (time.Duration, error) {
	if typeID != DefaultEventRetentionTypeID {
		row, err := r.Get(ctx, typeID)
		if err == nil {
			return time.Duration(row.KeepDurationSeconds) * time.Second, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	row, err := r.Get(ctx, DefaultEventRetentionTypeID)
	if err != nil {
		return 0, err
	}
	return time.Duration(row.KeepDurationSeconds) * time.Second, nil
}

// Upsert inserts or replaces the retention row; sets UpdatedAt = Now().
func (r *EventRetentionRepo) Upsert(ctx context.Context, e *EventRetention) error {
	now := time.Now().UTC()
	e.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO event_retention (type_id, keep_duration_seconds, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(type_id) DO UPDATE SET
		    keep_duration_seconds = excluded.keep_duration_seconds,
		    updated_at = excluded.updated_at
	`, e.TypeID, e.KeepDurationSeconds, FormatTime(now))
	return err
}

// List returns every retention row, ordered by type_id.
func (r *EventRetentionRepo) List(ctx context.Context) ([]*EventRetention, error) {
	rows, err := r.db.QueryContext(ctx, eventRetentionSelect+` ORDER BY type_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EventRetention
	for rows.Next() {
		e, err := scanEventRetention(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const eventRetentionSelect = `
SELECT type_id, keep_duration_seconds, updated_at
FROM event_retention
`

func scanEventRetention(row rowScanner) (*EventRetention, error) {
	var e EventRetention
	var updatedAt string
	if err := row.Scan(&e.TypeID, &e.KeepDurationSeconds, &updatedAt); err != nil {
		return nil, err
	}
	t, err := ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	e.UpdatedAt = t
	return &e, nil
}
