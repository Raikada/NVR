-- +goose Up

-- Optional organizational grouping (front, back, garage, ...). Cameras
-- have a nullable group_id; a camera with no group renders as ungrouped
-- in the UI. No constraint that groups must be non-empty.

CREATE TABLE camera_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_camera_groups_order ON camera_groups(display_order);

-- +goose Down
DROP INDEX IF EXISTS idx_camera_groups_order;
DROP TABLE IF EXISTS camera_groups;
