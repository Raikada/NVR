-- +goose Up

-- Two-role RBAC seed: admin (all permissions) and viewer (read-only).
-- The role -> permission map is in code (internal/rbac/), not in DB.
-- Promotion to DB-defined custom roles is a future migration.

CREATE TABLE roles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at TEXT NOT NULL
) STRICT;

INSERT INTO roles (id, name, description, created_at) VALUES
    ('role_admin',  'admin',  'Full control of cameras, policies, users, settings',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('role_viewer', 'viewer', 'Read-only access to cameras, recordings, events, clips',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

-- +goose Down
DROP TABLE IF EXISTS roles;
