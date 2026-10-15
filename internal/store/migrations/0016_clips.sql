-- +goose Up

-- Clips are lazy-extraction handles. state goes requested -> preparing
-- -> ready (or failed). output_path is populated only on ready. event_id
-- is nullable (ad-hoc clips have no event); on the event being deleted,
-- the clip stays but its event link is nulled (SET NULL). On the camera
-- being deleted, clips cascade.

CREATE TABLE clips (
    id TEXT PRIMARY KEY,
    event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    start_time TEXT NOT NULL,
    end_time TEXT NOT NULL,
    pre_roll_seconds INTEGER NOT NULL DEFAULT 0,
    post_roll_seconds INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL,                      -- requested|preparing|ready|failed
    format TEXT NOT NULL DEFAULT 'fmp4',
    output_path TEXT,
    size_bytes INTEGER,
    checksum TEXT,
    created_by TEXT REFERENCES local_users(id),
    created_at TEXT NOT NULL,
    ready_at TEXT,
    expires_at TEXT
) STRICT;

CREATE INDEX idx_clips_camera_time ON clips(camera_id, start_time);
CREATE INDEX idx_clips_event ON clips(event_id);

-- +goose Down
DROP INDEX IF EXISTS idx_clips_event;
DROP INDEX IF EXISTS idx_clips_camera_time;
DROP TABLE IF EXISTS clips;
