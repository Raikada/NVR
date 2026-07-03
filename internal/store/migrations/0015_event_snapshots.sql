-- +goose Up

-- Disk-path-only event snapshots (no inline BLOB per spec Section 2).
-- Populated by the snapshot fetcher in sub-project 4. path is relative
-- to system_settings['snapshot_root'] (default <recordings_root>/snapshots).

CREATE TABLE event_snapshots (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,                       -- 'full' | 'thumb'
    width INTEGER,
    height INTEGER,
    path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    fetched_at TEXT NOT NULL,
    PRIMARY KEY (event_id, kind)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS event_snapshots;
