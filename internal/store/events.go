package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Event is a row in events. Hot path — heavy reads + sustained writes.
type Event struct {
	ID             string
	CameraID       string
	TypeID         string
	Source         string
	OccurredAt     time.Time
	ReceivedAt     time.Time
	Severity       string // empty when NULL
	PayloadJSON    string
	RegionJSON     string
	AcknowledgedAt time.Time // zero if not acknowledged
	AcknowledgedBy string    // empty if not acknowledged
	ExpiresAt      time.Time
}

// ListEventsFilter is the filter struct for EventsRepo.List.
type ListEventsFilter struct {
	CameraIDs []string  // empty = any
	TypeIDs   []string  // empty = any
	Sources   []string  // empty = any
	From      time.Time // zero = unbounded
	To        time.Time // zero = unbounded
	MinSev    string    // empty = any
	OnlyUnack bool
	Cursor    string // last seen id (UUIDv7 sortable)
	Limit     int    // capped at 500
}

// EventsRepo provides CRUD over events.
type EventsRepo struct {
	db *sql.DB
}

// ErrEventNotFound is returned by GetByID when no row matches.
var ErrEventNotFound = errors.New("event not found")

// Insert adds a new event. Caller is responsible for materializing
// ExpiresAt before calling.
func (r *EventsRepo) Insert(ctx context.Context, e *Event) error {
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = time.Now().UTC()
	}
	var ackAt, ackBy any
	if !e.AcknowledgedAt.IsZero() {
		ackAt = FormatTime(e.AcknowledgedAt)
	}
	if e.AcknowledgedBy != "" {
		ackBy = e.AcknowledgedBy
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO events (
			id, camera_id, type_id, source, occurred_at, received_at,
			severity, payload_json, region_json,
			acknowledged_at, acknowledged_by, expires_at
		)
		VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
	`,
		e.ID, e.CameraID, e.TypeID, e.Source,
		FormatTime(e.OccurredAt), FormatTime(e.ReceivedAt),
		e.Severity, e.PayloadJSON, e.RegionJSON,
		ackAt, ackBy, FormatTime(e.ExpiresAt),
	)
	return err
}

// GetByID fetches an event by id.
func (r *EventsRepo) GetByID(ctx context.Context, id string) (*Event, error) {
	row := r.db.QueryRowContext(ctx, eventSelect+` WHERE id = ?`, id)
	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEventNotFound
	}
	return e, err
}

// List returns events matching filter, paginated by id DESC. Returns
// the page and the next cursor (id of last row, or "" if no more).
func (r *EventsRepo) List(ctx context.Context, f ListEventsFilter) ([]*Event, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var sb strings.Builder
	sb.WriteString(eventSelect)
	sb.WriteString(" WHERE 1=1")
	var args []any

	if len(f.CameraIDs) > 0 {
		sb.WriteString(" AND camera_id IN (" + placeholders(len(f.CameraIDs)) + ")")
		for _, id := range f.CameraIDs {
			args = append(args, id)
		}
	}
	if len(f.TypeIDs) > 0 {
		sb.WriteString(" AND type_id IN (" + placeholders(len(f.TypeIDs)) + ")")
		for _, id := range f.TypeIDs {
			args = append(args, id)
		}
	}
	if len(f.Sources) > 0 {
		sb.WriteString(" AND source IN (" + placeholders(len(f.Sources)) + ")")
		for _, src := range f.Sources {
			args = append(args, src)
		}
	}
	if !f.From.IsZero() {
		sb.WriteString(" AND occurred_at >= ?")
		args = append(args, FormatTime(f.From))
	}
	if !f.To.IsZero() {
		sb.WriteString(" AND occurred_at <= ?")
		args = append(args, FormatTime(f.To))
	}
	if f.MinSev != "" {
		sb.WriteString(" AND severity = ?")
		args = append(args, f.MinSev)
	}
	if f.OnlyUnack {
		sb.WriteString(" AND acknowledged_at IS NULL")
	}
	if f.Cursor != "" {
		sb.WriteString(" AND id < ?")
		args = append(args, f.Cursor)
	}
	sb.WriteString(" ORDER BY id DESC LIMIT ?")
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var nextCursor string
	if len(out) == limit && limit > 0 {
		nextCursor = out[len(out)-1].ID
	}
	return out, nextCursor, nil
}

// Acknowledge marks an event as acknowledged by userID. Idempotent —
// already-acked events are not re-stamped.
func (r *EventsRepo) Acknowledge(ctx context.Context, id, userID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE events SET acknowledged_at = ?, acknowledged_by = ?
		WHERE id = ? AND acknowledged_at IS NULL
	`, Now(), userID, id)
	if err != nil {
		return err
	}
	if _, err := res.RowsAffected(); err != nil {
		return err
	}
	// Idempotent: 0 rows affected is fine when already acked.
	// Verify the id exists at all so callers get ErrEventNotFound if not.
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE id = ?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrEventNotFound
	}
	return nil
}

// DeleteExpired removes up to batchSize rows where expires_at < Now().
// Returns the count of deleted rows.
func (r *EventsRepo) DeleteExpired(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM events
		WHERE id IN (
			SELECT id FROM events WHERE expires_at < ? ORDER BY expires_at LIMIT ?
		)
	`, Now(), batchSize)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// CountByCameraSince counts events for a camera occurring at or after `since`.
func (r *EventsRepo) CountByCameraSince(ctx context.Context, cameraID string, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE camera_id = ? AND occurred_at >= ?
	`, cameraID, FormatTime(since)).Scan(&n)
	return n, err
}

// placeholders returns "?, ?, ?" with n placeholders.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

const eventSelect = `
SELECT id, camera_id, type_id, source, occurred_at, received_at,
       COALESCE(severity, ''), COALESCE(payload_json, ''), COALESCE(region_json, ''),
       COALESCE(acknowledged_at, ''), COALESCE(acknowledged_by, ''), expires_at
FROM events
`

func scanEvent(row rowScanner) (*Event, error) {
	var e Event
	var occurredAt, receivedAt, ackAt, expiresAt string
	if err := row.Scan(
		&e.ID, &e.CameraID, &e.TypeID, &e.Source, &occurredAt, &receivedAt,
		&e.Severity, &e.PayloadJSON, &e.RegionJSON,
		&ackAt, &e.AcknowledgedBy, &expiresAt,
	); err != nil {
		return nil, err
	}
	t, err := ParseTime(occurredAt)
	if err != nil {
		return nil, err
	}
	e.OccurredAt = t
	t, err = ParseTime(receivedAt)
	if err != nil {
		return nil, err
	}
	e.ReceivedAt = t
	if ackAt != "" {
		if t, err := ParseTime(ackAt); err == nil {
			e.AcknowledgedAt = t
		}
	}
	t, err = ParseTime(expiresAt)
	if err != nil {
		return nil, err
	}
	e.ExpiresAt = t
	return &e, nil
}
