package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NotificationTarget is a row in notification_targets. The webhook_secret
// columns hold AES-GCM ciphertext + nonce produced by the cred vault.
type NotificationTarget struct {
	Kind                    string // 'webhook' | 'email'
	ID                      string
	Name                    string
	WebhookURL              string // empty for email targets
	WebhookSecretCiphertext []byte
	WebhookSecretNonce      []byte
	EmailAddress            string // empty for webhook targets
	Enabled                 bool
	CreatedAt               time.Time
}

// NotificationTargetsRepo provides CRUD over notification_targets.
type NotificationTargetsRepo struct {
	db *sql.DB
}

// ErrNotificationTargetNotFound is returned by per-id mutators on miss.
var ErrNotificationTargetNotFound = errors.New("notification target not found")

// Insert adds a new target.
func (r *NotificationTargetsRepo) Insert(ctx context.Context, t *NotificationTarget) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_targets (
			id, kind, name, webhook_url,
			webhook_secret_ciphertext, webhook_secret_nonce,
			email_address, enabled, created_at
		)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?)
	`,
		t.ID, t.Kind, t.Name, t.WebhookURL,
		t.WebhookSecretCiphertext, t.WebhookSecretNonce,
		t.EmailAddress, boolToInt(t.Enabled), FormatTime(t.CreatedAt),
	)
	return err
}

// GetByID fetches a target by id.
func (r *NotificationTargetsRepo) GetByID(ctx context.Context, id string) (*NotificationTarget, error) {
	row := r.db.QueryRowContext(ctx, notificationTargetSelect+` WHERE id = ?`, id)
	return scanNotificationTarget(row)
}

// List returns every target, ordered by created_at ASC.
func (r *NotificationTargetsRepo) List(ctx context.Context) ([]*NotificationTarget, error) {
	rows, err := r.db.QueryContext(ctx, notificationTargetSelect+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationTarget
	for rows.Next() {
		t, err := scanNotificationTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Update mutates name + webhook_url + email_address + enabled.
// Secret rotation is via RotateWebhookSecret.
func (r *NotificationTargetsRepo) Update(ctx context.Context, t *NotificationTarget) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_targets SET
			name = ?, webhook_url = NULLIF(?, ''),
			email_address = NULLIF(?, ''), enabled = ?
		WHERE id = ?
	`, t.Name, t.WebhookURL, t.EmailAddress, boolToInt(t.Enabled), t.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationTargetNotFound
	}
	return nil
}

// SetEnabled flips enabled.
func (r *NotificationTargetsRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_targets SET enabled = ? WHERE id = ?
	`, boolToInt(enabled), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationTargetNotFound
	}
	return nil
}

// Delete removes the target. Cascades into subscriptions and outbox rows.
func (r *NotificationTargetsRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM notification_targets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationTargetNotFound
	}
	return nil
}

// RotateWebhookSecret writes a new ciphertext + nonce.
func (r *NotificationTargetsRepo) RotateWebhookSecret(ctx context.Context, id string, ct, nonce []byte) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notification_targets SET
			webhook_secret_ciphertext = ?, webhook_secret_nonce = ?
		WHERE id = ?
	`, ct, nonce, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotificationTargetNotFound
	}
	return nil
}

const notificationTargetSelect = `
SELECT id, kind, name, COALESCE(webhook_url, ''),
       webhook_secret_ciphertext, webhook_secret_nonce,
       COALESCE(email_address, ''), enabled, created_at
FROM notification_targets
`

func scanNotificationTarget(row rowScanner) (*NotificationTarget, error) {
	var t NotificationTarget
	var enabled int
	var createdAt string
	if err := row.Scan(
		&t.ID, &t.Kind, &t.Name, &t.WebhookURL,
		&t.WebhookSecretCiphertext, &t.WebhookSecretNonce,
		&t.EmailAddress, &enabled, &createdAt,
	); err != nil {
		return nil, err
	}
	t.Enabled = enabled != 0
	tm, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	t.CreatedAt = tm
	return &t, nil
}
