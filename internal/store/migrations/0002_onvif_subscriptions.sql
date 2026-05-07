-- +goose Up

-- ONVIF PullPoint subscription persistence (Wave A1).
--
-- Wave 3 (recorder@da1378e1) shipped the ONVIF subscription manager with
-- in-memory state only — recorder restart dropped every active
-- subscription, requiring an operator round-trip to re-subscribe each
-- camera. This table persists enough state to rehydrate active
-- subscriptions on startup: the camera-issued subscription URL plus
-- whatever credentials the manager needs to renew/pull/unsubscribe.
--
-- Credentials (username/password) are stored alongside the URL because
-- the recorder's pre-pairing trust model already keeps userinfo in the
-- recorder-local sqlite (mirrors LocalUser password_hash); the recorder
-- DB sits in the identity dir which factory-wipe removes wholesale (per
-- recorder/internal/api/api_v1_recorder_lifecycle.go), so this row is
-- auto-cleared on D8 factory wipe — no separate teardown wiring needed.
--
-- camera_id is opaque (recorder canonical Camera id when the caller
-- supplied one). state mirrors the in-memory record: active | failed
-- | terminated. Only "active" rows are rehydrated at startup; terminated
-- rows are discarded on a successful rehydrate pass to keep the table
-- bounded over time.

CREATE TABLE onvif_subscriptions (
    id TEXT PRIMARY KEY,
    camera_id TEXT NOT NULL,
    xaddr TEXT NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    password TEXT NOT NULL DEFAULT '',
    subscription_url TEXT NOT NULL,
    termination_time TEXT NOT NULL,
    created_at TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'active',
    last_error TEXT NOT NULL DEFAULT '',
    last_event_at TEXT,
    event_count INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX idx_onvif_subscriptions_camera_id ON onvif_subscriptions(camera_id);

-- +goose Down
DROP INDEX IF EXISTS idx_onvif_subscriptions_camera_id;
DROP TABLE IF EXISTS onvif_subscriptions;
