package store

import (
	"context"
	"database/sql"
	"errors"
)

// NotificationSubscription is a row in notification_subscriptions. A
// target receives an event iff a matching subscription exists.
type NotificationSubscription struct {
	ID                    string
	TargetID              string
	EventTypeID           string // empty when applies to all types
	CameraID              string // empty when applies to all cameras
	MinSeverity           string // empty when applies to all severities
	QuietHoursStartMinute int    // -1 when unset
	QuietHoursEndMinute   int    // -1 when unset
}

// NotificationSubscriptionWithTarget is the denormalized join used by
// the dispatcher: a subscription plus the parent target's relevant fields.
type NotificationSubscriptionWithTarget struct {
	NotificationSubscription
	TargetKind                    string
	TargetName                    string
	TargetWebhookURL              string
	TargetWebhookSecretCiphertext []byte
	TargetWebhookSecretNonce      []byte
	TargetEmailAddress            string
	TargetEnabled                 bool
}

// NotificationSubscriptionsRepo provides CRUD over notification_subscriptions.
type NotificationSubscriptionsRepo struct {
	db *sql.DB
}

// ErrNotificationSubscriptionNotFound is returned by per-id mutators on miss.
var ErrNotificationSubscriptionNotFound = errors.New("notification subscription not found")

// Insert adds a new subscription.
func (r *NotificationSubscriptionsRepo) Insert(ctx context.Context, n *NotificationSubscription) error {
	var qhStart, qhEnd any
	if n.QuietHoursStartMinute >= 0 {
		qhStart = n.QuietHoursStartMinute
	}
	if n.QuietHoursEndMinute >= 0 {
		qhEnd = n.QuietHoursEndMinute
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_subscriptions (
			id, target_id, event_type_id, camera_id, min_severity,
			quiet_hours_start_minute, quiet_hours_end_minute
		)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)
	`, n.ID, n.TargetID, n.EventTypeID, n.CameraID, n.MinSeverity, qhStart, qhEnd)
	return err
}

// GetByID fetches a subscription by id.
func (r *NotificationSubscriptionsRepo) GetByID(ctx context.Context, id string) (*NotificationSubscription, error) {
	row := r.db.QueryRowContext(ctx, notificationSubscriptionSelect+` WHERE id = ?`, id)
	return scanNotificationSubscription(row)
}

// ListAllJoined returns every subscription joined to its parent target,
// skipping subscriptions whose target is disabled. This is the cached
// view consumed by the dispatcher.
func (r *NotificationSubscriptionsRepo) ListAllJoined(ctx context.Context) ([]*NotificationSubscriptionWithTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.target_id, COALESCE(s.event_type_id, ''),
		       COALESCE(s.camera_id, ''), COALESCE(s.min_severity, ''),
		       COALESCE(s.quiet_hours_start_minute, -1),
		       COALESCE(s.quiet_hours_end_minute, -1),
		       t.kind, t.name, COALESCE(t.webhook_url, ''),
		       t.webhook_secret_ciphertext, t.webhook_secret_nonce,
		       COALESCE(t.email_address, ''), t.enabled
		FROM notification_subscriptions s
		JOIN notification_targets t ON s.target_id = t.id
		WHERE t.enabled = 1
		ORDER BY s.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationSubscriptionWithTarget
	for rows.Next() {
		var sj NotificationSubscriptionWithTarget
		var enabled int
		if err := rows.Scan(
			&sj.ID, &sj.TargetID, &sj.EventTypeID,
			&sj.CameraID, &sj.MinSeverity,
			&sj.QuietHoursStartMinute, &sj.QuietHoursEndMinute,
			&sj.TargetKind, &sj.TargetName, &sj.TargetWebhookURL,
			&sj.TargetWebhookSecretCiphertext, &sj.TargetWebhookSecretNonce,
			&sj.TargetEmailAddress, &enabled,
		); err != nil {
			return nil, err
		}
		sj.TargetEnabled = enabled != 0
		out = append(out, &sj)
	}
	return out, rows.Err()
}

// Delete removes a subscription.
func (r *NotificationSubscriptionsRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM notification_subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationSubscriptionNotFound
	}
	return nil
}

const notificationSubscriptionSelect = `
SELECT id, target_id, COALESCE(event_type_id, ''), COALESCE(camera_id, ''),
       COALESCE(min_severity, ''),
       COALESCE(quiet_hours_start_minute, -1),
       COALESCE(quiet_hours_end_minute, -1)
FROM notification_subscriptions
`

func scanNotificationSubscription(row rowScanner) (*NotificationSubscription, error) {
	var n NotificationSubscription
	if err := row.Scan(&n.ID, &n.TargetID, &n.EventTypeID, &n.CameraID,
		&n.MinSeverity, &n.QuietHoursStartMinute, &n.QuietHoursEndMinute); err != nil {
		return nil, err
	}
	return &n, nil
}
