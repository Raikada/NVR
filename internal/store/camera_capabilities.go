package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CameraCapabilities is a row in camera_capabilities. Populated by the
// capability probe background worker.
type CameraCapabilities struct {
	CameraID               string
	ProfilesJSON           string
	SelectedProfileToken   string
	HasAudio               bool
	HasPTZ                 bool
	HasMotion              bool
	HasIO                  bool
	HasImaging             bool
	VendorCapabilitiesJSON string
	ProbedAt               time.Time
}

// CameraCapabilitiesRepo provides upsert/get/delete of capability snapshots.
type CameraCapabilitiesRepo struct {
	db *sql.DB
}

// ErrCameraCapabilitiesNotFound is returned by Get when no row matches.
var ErrCameraCapabilitiesNotFound = errors.New("camera capabilities not found")

// Upsert inserts or replaces the capability row for a camera; sets
// ProbedAt = Now() if zero.
func (r *CameraCapabilitiesRepo) Upsert(ctx context.Context, c *CameraCapabilities) error {
	if c.ProbedAt.IsZero() {
		c.ProbedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_capabilities
		    (camera_id, profiles_json, selected_profile_token,
		     has_audio, has_ptz, has_motion, has_io, has_imaging,
		     vendor_capabilities_json, probed_at)
		VALUES (?, ?, NULLIF(?, ''),
		        ?, ?, ?, ?, ?,
		        NULLIF(?, ''), ?)
		ON CONFLICT(camera_id) DO UPDATE SET
		    profiles_json = excluded.profiles_json,
		    selected_profile_token = excluded.selected_profile_token,
		    has_audio = excluded.has_audio,
		    has_ptz = excluded.has_ptz,
		    has_motion = excluded.has_motion,
		    has_io = excluded.has_io,
		    has_imaging = excluded.has_imaging,
		    vendor_capabilities_json = excluded.vendor_capabilities_json,
		    probed_at = excluded.probed_at
	`,
		c.CameraID, c.ProfilesJSON, c.SelectedProfileToken,
		boolToInt(c.HasAudio), boolToInt(c.HasPTZ), boolToInt(c.HasMotion),
		boolToInt(c.HasIO), boolToInt(c.HasImaging),
		c.VendorCapabilitiesJSON, FormatTime(c.ProbedAt),
	)
	return err
}

// Get fetches the capability row for a camera.
func (r *CameraCapabilitiesRepo) Get(ctx context.Context, cameraID string) (*CameraCapabilities, error) {
	row := r.db.QueryRowContext(ctx, cameraCapabilitiesSelect+` WHERE camera_id = ?`, cameraID)
	c, err := scanCameraCapabilities(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCameraCapabilitiesNotFound
	}
	return c, err
}

// Delete removes the capability row for a camera. Cascade from cameras
// also fires; this is for diagnostics only.
func (r *CameraCapabilitiesRepo) Delete(ctx context.Context, cameraID string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM camera_capabilities WHERE camera_id = ?`, cameraID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCameraCapabilitiesNotFound
	}
	return nil
}

const cameraCapabilitiesSelect = `
SELECT camera_id, profiles_json, COALESCE(selected_profile_token, ''),
       has_audio, has_ptz, has_motion, has_io, has_imaging,
       COALESCE(vendor_capabilities_json, ''), probed_at
FROM camera_capabilities
`

func scanCameraCapabilities(row rowScanner) (*CameraCapabilities, error) {
	var c CameraCapabilities
	var hasAudio, hasPTZ, hasMotion, hasIO, hasImaging int
	var probedAt string
	if err := row.Scan(
		&c.CameraID, &c.ProfilesJSON, &c.SelectedProfileToken,
		&hasAudio, &hasPTZ, &hasMotion, &hasIO, &hasImaging,
		&c.VendorCapabilitiesJSON, &probedAt,
	); err != nil {
		return nil, err
	}
	c.HasAudio = hasAudio != 0
	c.HasPTZ = hasPTZ != 0
	c.HasMotion = hasMotion != 0
	c.HasIO = hasIO != 0
	c.HasImaging = hasImaging != 0
	t, err := ParseTime(probedAt)
	if err != nil {
		return nil, err
	}
	c.ProbedAt = t
	return &c, nil
}
