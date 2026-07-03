-- +goose Up

-- Site-wide k/v config. Seeded with sensible defaults; operators override
-- via PATCH /v1/system/settings. timezone is set from time.Local at first
-- boot via internal/core seeding (cannot be done in pure SQL).

CREATE TABLE system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    updated_by TEXT REFERENCES local_users(id)
) STRICT;

INSERT INTO system_settings (key, value, updated_at, updated_by) VALUES
    ('site_name',                       'My NVR',         strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('language',                        'en',             strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('lockout_threshold',               '5',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('lockout_duration_minutes',        '15',             strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('capability_probe_interval_hours', '24',             strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('health_debounce_seconds',         '5',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('default_policy_id',               'policy_default', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('outbox_workers',                  '4',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('outbox_max_attempts',             '8',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('outbox_request_timeout_seconds',  '5',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('cloud_outbox_horizon_hours',      '168',            strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('cloud_endpoint',                  '',               strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('snapshot_root',                   '',               strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('clip_root',                       '',               strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('webhook_signature_scheme',        'simple',         strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL);

-- timezone, smtp_*, cloud_token_*, mdns_service_name are seeded at
-- runtime by internal/core because they need host introspection or
-- envelope-encryption that pure SQL cannot do.

-- +goose Down
DROP TABLE IF EXISTS system_settings;
