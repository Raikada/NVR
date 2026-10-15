-- +goose Up

-- Phase A B3: bookmarks are plain events — timeline markers, retention
-- and export come free. No snapshot capture: the marked instant's
-- frame comes from playback, not a capture at creation time.
INSERT INTO event_types (id, display_name, vendor, description, capture_snapshot) VALUES
    ('bookmark', 'Bookmark', 'internal', 'User-placed timeline marker', 0);
INSERT INTO event_retention (type_id, keep_duration_seconds, updated_at) VALUES
    ('bookmark', 31536000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));  -- 365d

-- +goose Down
DELETE FROM event_retention WHERE type_id = 'bookmark';
DELETE FROM event_types WHERE id = 'bookmark';
