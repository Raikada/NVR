-- +goose Up

-- Canonical CameraEvent records. id is UUIDv7 so ORDER BY id DESC equals
-- chronological. expires_at is materialized at insert time from
-- received_at + event_retention[type_id].keep_duration so retention sweep
-- is a fast index scan and changing retention does not retroactively
-- expire historical rows.

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    type_id TEXT NOT NULL REFERENCES event_types(id),
    source TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    severity TEXT,                            -- info|warning|critical
    payload_json TEXT,
    region_json TEXT,
    acknowledged_at TEXT,
    acknowledged_by TEXT REFERENCES local_users(id),
    expires_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_events_camera_time ON events(camera_id, occurred_at);
CREATE INDEX idx_events_type ON events(type_id);
CREATE INDEX idx_events_expires ON events(expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_events_expires;
DROP INDEX IF EXISTS idx_events_type;
DROP INDEX IF EXISTS idx_events_camera_time;
DROP TABLE IF EXISTS events;
