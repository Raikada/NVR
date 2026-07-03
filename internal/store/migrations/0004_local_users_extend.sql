-- +goose Up

-- Extend local_users with role_id (FK to roles), email (for SMTP recipient),
-- language preference. The legacy is_admin column is no longer read; we keep
-- the column (SQLite has limited DROP COLUMN support before 3.35) and treat
-- role_id as authoritative. Backfill role_id from is_admin so any existing
-- bootstrap admins continue to work.

ALTER TABLE local_users ADD COLUMN role_id TEXT REFERENCES roles(id);
ALTER TABLE local_users ADD COLUMN email TEXT;
ALTER TABLE local_users ADD COLUMN language TEXT NOT NULL DEFAULT 'en';

UPDATE local_users
   SET role_id = CASE is_admin WHEN 1 THEN 'role_admin' ELSE 'role_viewer' END
 WHERE role_id IS NULL;

-- +goose Down
-- Cannot drop columns on older SQLite; leave columns in place on rollback.
UPDATE local_users SET role_id = NULL, email = NULL, language = 'en';
