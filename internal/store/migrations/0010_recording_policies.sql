-- +goose Up

-- Canonical recording policies. Replaces the conf.RecordingPolicies map
-- as source of truth (legacy YAML map is logged-and-ignored at runtime).
-- The 'policy_default' row is seeded and cannot be deleted (enforced in
-- the API layer). pre/post_event_seconds default to 5 per Section 5.

CREATE TABLE recording_policies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    mode TEXT NOT NULL,                       -- continuous|motion|scheduled|off
    retention_duration_seconds INTEGER NOT NULL,
    container TEXT NOT NULL,                  -- fmp4|mpegts
    min_segment_duration_seconds INTEGER NOT NULL,
    max_segment_duration_seconds INTEGER NOT NULL,
    part_duration_ms INTEGER NOT NULL,
    max_part_size_bytes INTEGER NOT NULL,
    pre_event_seconds INTEGER NOT NULL DEFAULT 5,
    post_event_seconds INTEGER NOT NULL DEFAULT 5,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

INSERT INTO recording_policies (
    id, name, mode, retention_duration_seconds, container,
    min_segment_duration_seconds, max_segment_duration_seconds,
    part_duration_ms, max_part_size_bytes,
    pre_event_seconds, post_event_seconds, enabled,
    created_at, updated_at
) VALUES (
    'policy_default', 'Default Policy', 'continuous', 1209600, 'fmp4',
    60, 300, 1000, 1048576,
    5, 5, 1,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
);

-- +goose Down
DROP TABLE IF EXISTS recording_policies;
