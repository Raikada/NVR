package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NotificationOutboxRow is a row in notification_outbox. Hot path —
// workers poll constantly.
type NotificationOutboxRow struct {
	ID            string
	TargetID      string
	EventID       string
	PayloadJSON   string
	State         string // pending|in_flight|delivered|failed|dead
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	DeliveredAt   time.Time
}

// NotificationOutboxRepo provides outbox lifecycle for the dispatcher.
type NotificationOutboxRepo struct {
	db *sql.DB
}

// ErrNotificationOutboxNotFound is returned by per-id mutators on miss.
var ErrNotificationOutboxNotFound = errors.New("notification outbox row not found")

// Insert adds a new outbox row in state 'pending'.
func (r *NotificationOutboxRepo) Insert(ctx context.Context, n *NotificationOutboxRow) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	if n.NextAttemptAt.IsZero() {
		n.NextAttemptAt = n.CreatedAt
	}
	n.State = "pending"
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_outbox (
			id, target_id, event_id, payload_json,
			state, attempts, next_attempt_at, last_error, created_at, delivered_at
		)
		VALUES (?, ?, ?, ?, 'pending', ?, ?, NULLIF(?, ''), ?, NULL)
	`,
		n.ID, n.TargetID, n.EventID, n.PayloadJSON,
		n.Attempts, FormatTime(n.NextAttemptAt), n.LastError, FormatTime(n.CreatedAt),
	)
	return err
}

// ClaimBatch atomically transitions up to batchSize rows from 'pending'
// (where next_attempt_at <= now) to 'in_flight' and returns them.
// Workers consume the returned rows.
func (r *NotificationOutboxRepo) ClaimBatch(ctx context.Context, batchSize int) ([]*NotificationOutboxRow, error) {
	if batchSize <= 0 {
		batchSize = 16
	}
	rows, err := r.db.QueryContext(ctx, `
		UPDATE notification_outbox
		SET state = 'in_flight'
		WHERE id IN (
			SELECT id FROM notification_outbox
			WHERE state = 'pending' AND next_attempt_at <= ?
			ORDER BY next_attempt_at LIMIT ?
		)
		RETURNING id, target_id, event_id, payload_json, attempts,
		          next_attempt_at, created_at, COALESCE(last_error, ''),
		          COALESCE(delivered_at, '')
	`, Now(), batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationOutboxRow
	for rows.Next() {
		var n NotificationOutboxRow
		var nextAttemptAt, createdAt, deliveredAt string
		if err := rows.Scan(&n.ID, &n.TargetID, &n.EventID, &n.PayloadJSON,
			&n.Attempts, &nextAttemptAt, &createdAt, &n.LastError, &deliveredAt); err != nil {
			return nil, err
		}
		t, err := ParseTime(nextAttemptAt)
		if err != nil {
			return nil, err
		}
		n.NextAttemptAt = t
		t, err = ParseTime(createdAt)
		if err != nil {
			return nil, err
		}
		n.CreatedAt = t
		if deliveredAt != "" {
			if t, err := ParseTime(deliveredAt); err == nil {
				n.DeliveredAt = t
			}
		}
		n.State = "in_flight"
		out = append(out, &n)
	}
	return out, rows.Err()
}

// MarkDelivered transitions a row to state='delivered' with delivered_at = Now().
func (r *NotificationOutboxRepo) MarkDelivered(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_outbox SET state = 'delivered', delivered_at = ?, last_error = NULL
		WHERE id = ?
	`, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationOutboxNotFound
	}
	return nil
}

// MarkFailed bumps attempts, sets state back to 'pending' (or 'dead' if
// attempts >= maxAttempts), and reschedules to nextAttemptAt.
func (r *NotificationOutboxRepo) MarkFailed(ctx context.Context, id, lastErr string, nextAttemptAt time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET attempts = attempts + 1,
		    state = 'pending',
		    next_attempt_at = ?,
		    last_error = NULLIF(?, '')
		WHERE id = ?
	`, FormatTime(nextAttemptAt), lastErr, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationOutboxNotFound
	}
	return nil
}

// MarkDead transitions a row to state='dead'.
func (r *NotificationOutboxRepo) MarkDead(ctx context.Context, id, lastErr string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET state = 'dead', attempts = attempts + 1, last_error = NULLIF(?, '')
		WHERE id = ?
	`, lastErr, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationOutboxNotFound
	}
	return nil
}

// Reset returns a dead row to state='pending' with next_attempt_at=Now().
// attempts unchanged. Used by /v1/notification-outbox/{id}/retry.
func (r *NotificationOutboxRepo) Reset(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET state = 'pending', next_attempt_at = ?, last_error = NULL
		WHERE id = ?
	`, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationOutboxNotFound
	}
	return nil
}

// ListRecent returns the most recent rows for the diagnostics endpoint.
func (r *NotificationOutboxRepo) ListRecent(ctx context.Context, limit int) ([]*NotificationOutboxRow, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx, notificationOutboxSelect+`
		ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationOutboxRow
	for rows.Next() {
		n, err := scanNotificationOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

const notificationOutboxSelect = `
SELECT id, target_id, event_id, payload_json,
       state, attempts, next_attempt_at, COALESCE(last_error, ''),
       created_at, COALESCE(delivered_at, '')
FROM notification_outbox
`

func scanNotificationOutbox(row rowScanner) (*NotificationOutboxRow, error) {
	var n NotificationOutboxRow
	var nextAttemptAt, createdAt, deliveredAt string
	if err := row.Scan(&n.ID, &n.TargetID, &n.EventID, &n.PayloadJSON,
		&n.State, &n.Attempts, &nextAttemptAt, &n.LastError,
		&createdAt, &deliveredAt); err != nil {
		return nil, err
	}
	t, err := ParseTime(nextAttemptAt)
	if err != nil {
		return nil, err
	}
	n.NextAttemptAt = t
	t, err = ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	n.CreatedAt = t
	if deliveredAt != "" {
		if t, err := ParseTime(deliveredAt); err == nil {
			n.DeliveredAt = t
		}
	}
	return &n, nil
}
