package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CloudOutboxRow is a row in cloud_outbox. Same shape as
// NotificationOutboxRow but kind-tagged.
type CloudOutboxRow struct {
	ID            string
	Kind          string // 'event'|'health'|'audit'
	PayloadJSON   string
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	DeliveredAt   time.Time
}

// CloudOutboxRepo provides cloud-bridge outbox lifecycle.
type CloudOutboxRepo struct {
	db *sql.DB
}

// ErrCloudOutboxNotFound is returned by per-id mutators on miss.
var ErrCloudOutboxNotFound = errors.New("cloud outbox row not found")

// Insert adds a new outbox row in state 'pending'.
func (r *CloudOutboxRepo) Insert(ctx context.Context, n *CloudOutboxRow) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	if n.NextAttemptAt.IsZero() {
		n.NextAttemptAt = n.CreatedAt
	}
	n.State = "pending"
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cloud_outbox (
			id, kind, payload_json,
			state, attempts, next_attempt_at, last_error, created_at, delivered_at
		)
		VALUES (?, ?, ?, 'pending', ?, ?, NULLIF(?, ''), ?, NULL)
	`,
		n.ID, n.Kind, n.PayloadJSON,
		n.Attempts, FormatTime(n.NextAttemptAt), n.LastError, FormatTime(n.CreatedAt),
	)
	return err
}

// ClaimBatch atomically transitions up to batchSize rows from 'pending'
// to 'in_flight' and returns them.
func (r *CloudOutboxRepo) ClaimBatch(ctx context.Context, batchSize int) ([]*CloudOutboxRow, error) {
	if batchSize <= 0 {
		batchSize = 16
	}
	rows, err := r.db.QueryContext(ctx, `
		UPDATE cloud_outbox
		SET state = 'in_flight'
		WHERE id IN (
			SELECT id FROM cloud_outbox
			WHERE state = 'pending' AND next_attempt_at <= ?
			ORDER BY next_attempt_at LIMIT ?
		)
		RETURNING id, kind, payload_json, attempts, next_attempt_at, created_at,
		          COALESCE(last_error, ''), COALESCE(delivered_at, '')
	`, Now(), batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CloudOutboxRow
	for rows.Next() {
		var n CloudOutboxRow
		var nextAttemptAt, createdAt, deliveredAt string
		if err := rows.Scan(&n.ID, &n.Kind, &n.PayloadJSON,
			&n.Attempts, &nextAttemptAt, &createdAt,
			&n.LastError, &deliveredAt); err != nil {
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

// MarkDelivered transitions to delivered.
func (r *CloudOutboxRepo) MarkDelivered(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE cloud_outbox SET state = 'delivered', delivered_at = ?, last_error = NULL
		WHERE id = ?
	`, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCloudOutboxNotFound
	}
	return nil
}

// MarkFailed bumps attempts and reschedules.
func (r *CloudOutboxRepo) MarkFailed(ctx context.Context, id, lastErr string, nextAttemptAt time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE cloud_outbox
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
		return ErrCloudOutboxNotFound
	}
	return nil
}

// MarkDead transitions to dead.
func (r *CloudOutboxRepo) MarkDead(ctx context.Context, id, lastErr string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE cloud_outbox
		SET state = 'dead', attempts = attempts + 1, last_error = NULLIF(?, '')
		WHERE id = ?
	`, lastErr, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCloudOutboxNotFound
	}
	return nil
}

// DeleteOlderThan removes rows with created_at < t. Returns count.
// Used by the unconfigured-install sweeper.
func (r *CloudOutboxRepo) DeleteOlderThan(ctx context.Context, t time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM cloud_outbox WHERE created_at < ?`, FormatTime(t))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
