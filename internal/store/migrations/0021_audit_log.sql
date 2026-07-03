-- +goose Up

-- Append-only mutation log. Never expires by default; admin-only manual
-- purge endpoint exists. Redaction is enforced at write time by the
-- audit emit helpers (per-domain explicit allow-list).

CREATE TABLE audit_log (
    id TEXT PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    actor_user_id TEXT REFERENCES local_users(id),
    actor_username TEXT,
    actor_ip TEXT,
    action TEXT NOT NULL,
    target_kind TEXT,
    target_id TEXT,
    before_json TEXT,
    after_json TEXT,
    details TEXT
) STRICT;

CREATE INDEX idx_audit_occurred ON audit_log(occurred_at);
CREATE INDEX idx_audit_actor ON audit_log(actor_user_id, occurred_at);
CREATE INDEX idx_audit_action ON audit_log(action, occurred_at);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_action;
DROP INDEX IF EXISTS idx_audit_actor;
DROP INDEX IF EXISTS idx_audit_occurred;
DROP TABLE IF EXISTS audit_log;
