-- +goose Up

-- Filter rows: a target receives an event iff a matching subscription
-- exists. Quiet hours live on the subscription (not the target) so one
-- target can power both an always-on critical-alerts subscription and a
-- daytime-only info-events subscription.

CREATE TABLE notification_subscriptions (
    id TEXT PRIMARY KEY,
    target_id TEXT NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
    event_type_id TEXT REFERENCES event_types(id),  -- nullable = all types
    camera_id TEXT REFERENCES cameras(id),           -- nullable = all cameras
    min_severity TEXT,                               -- nullable = all severities
    quiet_hours_start_minute INTEGER,
    quiet_hours_end_minute INTEGER
) STRICT;

CREATE INDEX idx_notif_subs_target ON notification_subscriptions(target_id);
CREATE INDEX idx_notif_subs_camera ON notification_subscriptions(camera_id);

-- +goose Down
DROP INDEX IF EXISTS idx_notif_subs_camera;
DROP INDEX IF EXISTS idx_notif_subs_target;
DROP TABLE IF EXISTS notification_subscriptions;
