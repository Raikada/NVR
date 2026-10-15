package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CameraGroup is a row in camera_groups; an optional grouping for cameras.
type CameraGroup struct {
	ID           string
	Name         string
	DisplayOrder int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CameraGroupsRepo provides CRUD over camera_groups.
type CameraGroupsRepo struct {
	db *sql.DB
}

// ErrCameraGroupExists is returned by Insert when the name is already taken.
var ErrCameraGroupExists = errors.New("camera group already exists")

// ErrCameraGroupNotFound is returned by per-id mutators when no row matches.
var ErrCameraGroupNotFound = errors.New("camera group not found")

// Insert adds a new group.
func (r *CameraGroupsRepo) Insert(ctx context.Context, g *CameraGroup) error {
	now := time.Now().UTC()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = now
	}
	if g.UpdatedAt.IsZero() {
		g.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_groups (id, name, display_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, g.ID, g.Name, g.DisplayOrder, FormatTime(g.CreatedAt), FormatTime(g.UpdatedAt))
	if err != nil && isConstraintErr(err) {
		return ErrCameraGroupExists
	}
	return err
}

// GetByID fetches a group by id.
func (r *CameraGroupsRepo) GetByID(ctx context.Context, id string) (*CameraGroup, error) {
	row := r.db.QueryRowContext(ctx, cameraGroupSelect+` WHERE id = ?`, id)
	return scanCameraGroup(row)
}

// GetByName fetches a group by its unique name.
func (r *CameraGroupsRepo) GetByName(ctx context.Context, name string) (*CameraGroup, error) {
	row := r.db.QueryRowContext(ctx, cameraGroupSelect+` WHERE name = ?`, name)
	return scanCameraGroup(row)
}

// List returns every group, ordered by display_order ASC, then name ASC.
func (r *CameraGroupsRepo) List(ctx context.Context) ([]*CameraGroup, error) {
	rows, err := r.db.QueryContext(ctx, cameraGroupSelect+` ORDER BY display_order ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CameraGroup
	for rows.Next() {
		g, err := scanCameraGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Update replaces name and display_order, bumps updated_at.
func (r *CameraGroupsRepo) Update(ctx context.Context, g *CameraGroup) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE camera_groups SET name = ?, display_order = ?, updated_at = ?
		WHERE id = ?
	`, g.Name, g.DisplayOrder, Now(), g.ID)
	if err != nil && isConstraintErr(err) {
		return ErrCameraGroupExists
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCameraGroupNotFound
	}
	return nil
}

// Delete removes the group; cameras with that group_id are left with a
// NULL group_id (FK is nullable, no CASCADE on this table).
func (r *CameraGroupsRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM camera_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCameraGroupNotFound
	}
	return nil
}

const cameraGroupSelect = `
SELECT id, name, display_order, created_at, updated_at
FROM camera_groups
`

func scanCameraGroup(row rowScanner) (*CameraGroup, error) {
	var g CameraGroup
	var createdAt, updatedAt string
	if err := row.Scan(&g.ID, &g.Name, &g.DisplayOrder, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	g.CreatedAt = t
	t, err = ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	g.UpdatedAt = t
	return &g, nil
}
