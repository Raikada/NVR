-- +goose Up

-- Phase A B2: the consumer role set grows from admin/viewer to five
-- tiers. Permission grants live in code (internal/rbac); these rows
-- exist for FK integrity + display.
INSERT INTO roles (id, name, description, created_at) VALUES
    ('role_owner',    'owner',    'Account holder; full control',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('role_operator', 'operator', 'Day-to-day: view, acknowledge, export clips, PTZ',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('role_audit',    'audit',    'Compliance: logs and metadata, no media, no mutations',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

-- +goose Down
DELETE FROM roles WHERE id IN ('role_owner', 'role_operator', 'role_audit');
