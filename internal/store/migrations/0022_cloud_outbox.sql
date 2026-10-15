-- +goose Up

-- Cloud-bridge seam. Same shape as notification_outbox. Processor in
-- internal/cloudbridge runs only when system_settings['cloud_endpoint']
-- is non-empty; otherwise a sweeper trims rows older than
-- cloud_outbox_horizon_hours (default 168 = 7d) so unconfigured installs
-- do not grow unboundedly.

CREATE TABLE cloud_outbox (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,                       -- event|health|audit
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT
) STRICT;

CREATE INDEX idx_cloud_outbox_state ON cloud_outbox(state, next_attempt_at);
CREATE INDEX idx_cloud_outbox_created ON cloud_outbox(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_cloud_outbox_created;
DROP INDEX IF EXISTS idx_cloud_outbox_state;
DROP TABLE IF EXISTS cloud_outbox;
