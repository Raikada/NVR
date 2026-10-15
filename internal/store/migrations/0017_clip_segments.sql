-- +goose Up

-- Many-to-many between clips and source recording segments. Populated by
-- the clip extractor (sub-project 4). segment_path is relative to the
-- recordstore root.

CREATE TABLE clip_segments (
    clip_id TEXT NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
    segment_path TEXT NOT NULL,
    start_offset_ms INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    PRIMARY KEY (clip_id, segment_path)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS clip_segments;
