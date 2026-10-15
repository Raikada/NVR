-- +goose Up

-- One row per camera, upserted by internal/cameras/healthCollector
-- (sub-project 2). rtsp_state is the current path-manager source state.
-- last_keyframe_at and last_event_at are taps the recorder publishes to
-- the in-process bus.

CREATE TABLE camera_health (
    camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
    rtsp_state TEXT NOT NULL,
    last_keyframe_at TEXT,
    last_event_at TEXT,
    last_seen_at TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    updated_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS camera_health;
