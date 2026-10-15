-- +goose Up

-- Canonical camera entity. credentials live in camera_credentials (separate
-- row, encrypted at rest). source_url here is the camera-supplied template
-- WITHOUT userinfo; the recorder materializes the userinfo at use time via
-- internal/cameracred/Vault.MaterializeRTSPURL.

CREATE TABLE cameras (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT,
    group_id TEXT REFERENCES camera_groups(id),
    manufacturer TEXT,
    model TEXT,
    serial_number TEXT,
    firmware_version TEXT,
    mac_address TEXT,
    ip_address TEXT,
    hostname TEXT,
    source_type TEXT NOT NULL,
    source_url TEXT NOT NULL,
    onvif_xaddr TEXT,
    recording_policy_id TEXT REFERENCES recording_policies(id) DEFERRABLE INITIALLY DEFERRED,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    paired_at TEXT,
    last_capability_probe_at TEXT
) STRICT;

CREATE INDEX idx_cameras_group ON cameras(group_id);
CREATE INDEX idx_cameras_enabled ON cameras(enabled);

-- +goose Down
DROP INDEX IF EXISTS idx_cameras_enabled;
DROP INDEX IF EXISTS idx_cameras_group;
DROP TABLE IF EXISTS cameras;
