-- +goose Up

-- Recorder-local LocalUser table — pre-pairing operator authentication.
--
-- Mirrors management/internal/store/migrations/0001_init.sql + 0004's
-- display_name addition. Kept narrow: the recorder is not the canonical
-- LocalUser store for paired deployments — that's the MS (per ADR 0010
-- D4 + ADR 0011 D2). This table exists so an integrator can log in to
-- a fresh recorder before pairing, complete first-run setup (network
-- identity, hostname, etc.) and trigger pairing. Once paired, MS-issued
-- JWTs validate via the existing applyPairingAwareAuth path; recorder-
-- local JWTs continue to be accepted as a local-admin fallback.
--
-- argon2id PHC-format hash in password_hash mirrors management exactly
-- (internal/auth/password.go). is_admin is the bootstrap-admin flag;
-- multi-user RBAC on the recorder is a future slice.
--
-- must_change_password is the forced-rotation flag for the bootstrap
-- admin's auto-generated initial password (printed at first start +
-- written to <identityDir>/initial-admin-password.txt). Cleared on
-- first successful POST /v1/recorder/local-users/me/password.

CREATE TABLE local_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT,
    password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_login_at TEXT,
    failed_login_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until TEXT
) STRICT;

CREATE INDEX idx_local_users_username ON local_users(username);

-- +goose Down
DROP INDEX IF EXISTS idx_local_users_username;
DROP TABLE IF EXISTS local_users;
