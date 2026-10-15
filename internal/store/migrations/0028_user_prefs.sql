-- +goose Up

-- Phase A B6: per-user UI preferences (tours, default grid, dwell).
-- Values are opaque JSON owned by the client; the server only scopes
-- them to the account so they follow logins across devices.
CREATE TABLE user_prefs (
    user_id TEXT NOT NULL REFERENCES local_users(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, key)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS user_prefs;
