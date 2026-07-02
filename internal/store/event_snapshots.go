package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// EventSnapshot is a row in event_snapshots. Composite PK (event_id, kind).
// Snapshots are immutable; replace by Delete + Insert.
type EventSnapshot struct {
	EventID   string
	Kind      string // 'full' | 'thumb'
	Width     int
	Height    int
	Path      string
	SizeBytes int64
	FetchedAt time.Time
}

// EventSnapshotsRepo provides CRUD over event_snapshots.
type EventSnapshotsRepo struct {
	db *sql.DB
}

// ErrEventSnapshotNotFound is returned by Get/Delete when no row matches.
var ErrEventSnapshotNotFound = errors.New("event snapshot not found")

// ErrEventSnapshotExists is returned by Insert when (event_id, kind) is taken.
var ErrEventSnapshotExists = errors.New("event snapshot already exists")

// Insert adds a new snapshot row.
func (r *EventSnapshotsRepo) Insert(ctx context.Context, e *EventSnapshot) error {
	if e.FetchedAt.IsZero() {
		e.FetchedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO event_snapshots (event_id, kind, width, height, path, size_bytes, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, e.EventID, e.Kind, e.Width, e.Height, e.Path, e.SizeBytes, FormatTime(e.FetchedAt))
	if err != nil && isConstraintErr(err) {
		return ErrEventSnapshotExists
	}
	return err
}

// Get fetches a snapshot by composite key.
func (r *EventSnapshotsRepo) Get(ctx context.Context, eventID, kind string) (*EventSnapshot, error) {
	row := r.db.QueryRowContext(ctx, eventSnapshotSelect+` WHERE event_id = ? AND kind = ?`, eventID, kind)
	e, err := scanEventSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEventSnapshotNotFound
	}
	return e, err
}

// ListByEvent returns every snapshot row for an event.
func (r *EventSnapshotsRepo) ListByEvent(ctx context.Context, eventID string) ([]*EventSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, eventSnapshotSelect+` WHERE event_id = ? ORDER BY kind`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EventSnapshot
	for rows.Next() {
		e, err := scanEventSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Delete removes a snapshot by composite key.
func (r *EventSnapshotsRepo) Delete(ctx context.Context, eventID, kind string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM event_snapshots WHERE event_id = ? AND kind = ?`, eventID, kind)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEventSnapshotNotFound
	}
	return nil
}

const eventSnapshotSelect = `
SELECT event_id, kind, COALESCE(width, 0), COALESCE(height, 0), path, size_bytes, fetched_at
FROM event_snapshots
`

func scanEventSnapshot(row rowScanner) (*EventSnapshot, error) {
	var e EventSnapshot
	var fetchedAt string
	if err := row.Scan(&e.EventID, &e.Kind, &e.Width, &e.Height, &e.Path, &e.SizeBytes, &fetchedAt); err != nil {
		return nil, err
	}
	t, err := ParseTime(fetchedAt)
	if err != nil {
		return nil, err
	}
	e.FetchedAt = t
	return &e, nil
}

// ListPathsForExpiredEvents returns the on-disk paths of snapshots whose
// parent event has expired — collected BEFORE the event rows are
// deleted (the FK cascade takes the snapshot rows; the files need an
// explicit unlink pass, SP4).
func (r *EventSnapshotsRepo) ListPathsForExpiredEvents(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT es.path FROM event_snapshots es
		JOIN events e ON es.event_id = e.id
		WHERE e.expires_at < ?
		LIMIT ?
	`, Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
