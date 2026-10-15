-- +goose Up

-- Per-event-type retention. The special row id='__default__' is the
-- fallback for any type not explicitly listed; it's NOT a row in
-- event_types (NULL FK is fine because the constraint is on event_types
-- by FK from this table — see below: we make the FK nullable via the
-- absence of NOT NULL on type_id... wait, type_id IS the PK so it cannot
-- be NULL. We use a real type_id='__default__' row in event_types via a
-- separate seed in the seedDefaults helper, or we drop the FK on this
-- table). We choose: this table has NO FK to event_types so we can store
-- arbitrary type ids including '__default__'; the API layer enforces
-- "type must exist or be __default__" on writes.

CREATE TABLE event_retention (
    type_id TEXT PRIMARY KEY,
    keep_duration_seconds INTEGER NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

INSERT INTO event_retention (type_id, keep_duration_seconds, updated_at) VALUES
    ('__default__',    604800,  strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 7d
    ('motion',         7776000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 90d
    ('doorbell',       7776000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 90d
    ('line_cross',     2592000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 30d
    ('tamper',         7776000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 90d
    ('camera_offline', 2592000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 30d
    ('camera_online',  2592000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));  -- 30d

-- +goose Down
DROP TABLE IF EXISTS event_retention;
