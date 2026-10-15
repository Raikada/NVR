-- +goose Up

-- Per-camera credentials, AES-GCM encrypted. The encryption key lives at
-- <identityDir>/cred.key (mode 0600) and is loaded by internal/cameracred.
-- One row per camera; cascades on camera deletion. ONVIF credentials are
-- separate columns because some cameras issue distinct accounts for
-- ONVIF vs RTSP.

CREATE TABLE camera_credentials (
    camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
    username TEXT NOT NULL,
    password_ciphertext BLOB NOT NULL,
    password_nonce BLOB NOT NULL,
    onvif_username TEXT,
    onvif_password_ciphertext BLOB,
    onvif_password_nonce BLOB,
    rotated_at TEXT NOT NULL,
    rotation_due_at TEXT
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS camera_credentials;
