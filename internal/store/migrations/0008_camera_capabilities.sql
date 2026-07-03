-- +goose Up

-- ONVIF / vendor capability snapshot. Populated by the capability probe
-- background worker (sub-project 2). profiles_json is the raw ONVIF
-- GetProfiles response, JSON-encoded; selected_profile_token is the one
-- the recorder is recording from.

CREATE TABLE camera_capabilities (
    camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
    profiles_json TEXT NOT NULL,
    selected_profile_token TEXT,
    has_audio INTEGER NOT NULL DEFAULT 0,
    has_ptz INTEGER NOT NULL DEFAULT 0,
    has_motion INTEGER NOT NULL DEFAULT 0,
    has_io INTEGER NOT NULL DEFAULT 0,
    has_imaging INTEGER NOT NULL DEFAULT 0,
    vendor_capabilities_json TEXT,
    probed_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS camera_capabilities;
