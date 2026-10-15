-- +goose Up

-- Weekly time-of-day windows for scheduled-mode policies. Site timezone
-- comes from system_settings['timezone'] (not stored per-row). Wrap-
-- midnight is allowed (end_minute < start_minute means the window crosses
-- into the next day); the resolver in internal/schedule handles it.
-- Multiple windows on the same day are unioned.

CREATE TABLE recording_schedules (
    id TEXT PRIMARY KEY,
    policy_id TEXT NOT NULL REFERENCES recording_policies(id) ON DELETE CASCADE,
    day_of_week INTEGER NOT NULL,             -- 0=Sun..6=Sat
    start_minute INTEGER NOT NULL,            -- 0..1439 site-local
    end_minute INTEGER NOT NULL               -- exclusive; if < start, wraps midnight
) STRICT;

CREATE INDEX idx_schedules_policy ON recording_schedules(policy_id);

-- +goose Down
DROP INDEX IF EXISTS idx_schedules_policy;
DROP TABLE IF EXISTS recording_schedules;
