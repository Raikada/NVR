-- +goose Up

-- Pending and in-flight deliveries. The outbox processor (internal/outbox)
-- picks rows where state IN ('pending') AND next_attempt_at <= now(). On
-- delivery, state='delivered'. On 4xx, state='dead'. On 5xx/timeout,
-- attempts++ and state stays 'pending' with next_attempt_at = now() +
-- backoff(attempts). After max_attempts (default 8), state='dead'.

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY,
    target_id TEXT NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL,                      -- pending|in_flight|delivered|failed|dead
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT
) STRICT;

CREATE INDEX idx_outbox_state_next ON notification_outbox(state, next_attempt_at);

-- +goose Down
DROP INDEX IF EXISTS idx_outbox_state_next;
DROP TABLE IF EXISTS notification_outbox;
