-- +goose Up

-- Webhook URLs and SMTP recipients. webhook_secret is AES-GCM encrypted
-- (same vault as camera_credentials). For email targets, email_address
-- holds the recipient and the SMTP server config lives in system_settings
-- (one server per recorder; many recipients).

CREATE TABLE notification_targets (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,                       -- 'webhook' | 'email'
    name TEXT NOT NULL,
    webhook_url TEXT,
    webhook_secret_ciphertext BLOB,
    webhook_secret_nonce BLOB,
    email_address TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_notif_targets_enabled ON notification_targets(enabled);

-- +goose Down
DROP INDEX IF EXISTS idx_notif_targets_enabled;
DROP TABLE IF EXISTS notification_targets;
