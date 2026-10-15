package store

import (
	"context"
	"database/sql"
	"time"
)

// Role is a row in roles. The role->permission map lives in internal/rbac,
// not here; this struct is just storage shape.
type Role struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
}

// RolesRepo provides read access to the roles table. Roles are seeded by
// migration 0003 and not mutable through this repo (the role list is a
// build-time constant; custom roles will be a future migration).
type RolesRepo struct {
	db *sql.DB
}

// GetByID fetches a role by its id (e.g. "role_admin").
func (r *RolesRepo) GetByID(ctx context.Context, id string) (*Role, error) {
	row := r.db.QueryRowContext(ctx, roleSelect+` WHERE id = ?`, id)
	return scanRole(row)
}

// GetByName fetches a role by its human-readable name (e.g. "admin").
func (r *RolesRepo) GetByName(ctx context.Context, name string) (*Role, error) {
	row := r.db.QueryRowContext(ctx, roleSelect+` WHERE name = ?`, name)
	return scanRole(row)
}

// List returns every role, ordered by name.
func (r *RolesRepo) List(ctx context.Context) ([]*Role, error) {
	rows, err := r.db.QueryContext(ctx, roleSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		ro, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, rows.Err()
}

const roleSelect = `SELECT id, name, COALESCE(description, ''), created_at FROM roles`

func scanRole(row rowScanner) (*Role, error) {
	var ro Role
	var createdAt string
	if err := row.Scan(&ro.ID, &ro.Name, &ro.Description, &createdAt); err != nil {
		return nil, err
	}
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	ro.CreatedAt = t
	return &ro, nil
}
