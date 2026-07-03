-- +goose Up

-- SP4: per-type snapshot capture flag. 1 = capture a JPEG when an
-- event of this type arrives; internal connectivity events don't get
-- snapshots.
ALTER TABLE event_types ADD COLUMN capture_snapshot INTEGER NOT NULL DEFAULT 1;
UPDATE event_types SET capture_snapshot = 0 WHERE id IN ('camera_online', 'camera_offline');

-- +goose Down
ALTER TABLE event_types DROP COLUMN capture_snapshot;
