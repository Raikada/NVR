package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Camera is a row in cameras. The most-mutated table at runtime — the
// camera service updates fields like firmware_version, paired_at, and
// last_capability_probe_at out-of-band of the operator-driven Update().
type Camera struct {
	ID                    string
	Name                  string
	DisplayName           string
	GroupID               string // empty when NULL
	Manufacturer          string
	Model                 string
	SerialNumber          string
	FirmwareVersion       string
	MACAddress            string
	IPAddress             string
	Hostname              string
	SourceType            string // 'rtsp'|'rtsps'|'rtmp'|'hls'|'onvif'
	SourceURL             string // template, no userinfo
	OnvifXAddr            string
	RecordingPolicyID     string // empty when NULL → resolver falls back to policy_default
	Enabled               bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
	PairedAt              time.Time // zero if never paired
	LastCapabilityProbeAt time.Time // zero if never probed
}

// ListCamerasFilter is the filter struct for CamerasRepo.List.
type ListCamerasFilter struct {
	GroupID string // empty = any group
	Enabled *bool  // nil = any
	Cursor  string // last seen id (UUIDv7 sortable); empty = page 1
	Limit   int    // capped at 500; 0 → 100
}

// CamerasRepo provides CRUD over cameras.
type CamerasRepo struct {
	db *sql.DB
}

// ErrCameraExists is returned by Insert when the name is already taken.
var ErrCameraExists = errors.New("camera with that name already exists")

// ErrCameraNotFound is returned by per-id mutators when no row matches.
var ErrCameraNotFound = errors.New("camera not found")

// Insert adds a new camera. Sets CreatedAt/UpdatedAt if zero.
func (r *CamerasRepo) Insert(ctx context.Context, c *Camera) error {
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	var pairedAt, probeAt any
	if !c.PairedAt.IsZero() {
		pairedAt = FormatTime(c.PairedAt)
	}
	if !c.LastCapabilityProbeAt.IsZero() {
		probeAt = FormatTime(c.LastCapabilityProbeAt)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cameras (
			id, name, display_name, group_id,
			manufacturer, model, serial_number, firmware_version,
			mac_address, ip_address, hostname,
			source_type, source_url, onvif_xaddr,
			recording_policy_id, enabled,
			created_at, updated_at, paired_at, last_capability_probe_at
		)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''),
		        NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		        NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		        ?, ?, NULLIF(?, ''),
		        NULLIF(?, ''), ?,
		        ?, ?, ?, ?)
	`,
		c.ID, c.Name, c.DisplayName, c.GroupID,
		c.Manufacturer, c.Model, c.SerialNumber, c.FirmwareVersion,
		c.MACAddress, c.IPAddress, c.Hostname,
		c.SourceType, c.SourceURL, c.OnvifXAddr,
		c.RecordingPolicyID, boolToInt(c.Enabled),
		FormatTime(c.CreatedAt), FormatTime(c.UpdatedAt), pairedAt, probeAt,
	)
	if err != nil && isConstraintErr(err) {
		// Differentiate UNIQUE on name from FK violations.
		msg := err.Error()
		if strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "cameras.name") {
			return ErrCameraExists
		}
		return ErrCameraExists
	}
	return err
}

// GetByID fetches a camera by id.
func (r *CamerasRepo) GetByID(ctx context.Context, id string) (*Camera, error) {
	row := r.db.QueryRowContext(ctx, cameraSelect+` WHERE id = ?`, id)
	return scanCamera(row)
}

// GetByName fetches a camera by its unique name.
func (r *CamerasRepo) GetByName(ctx context.Context, name string) (*Camera, error) {
	row := r.db.QueryRowContext(ctx, cameraSelect+` WHERE name = ?`, name)
	return scanCamera(row)
}

// List returns cameras matching filter, paginated by id DESC.
func (r *CamerasRepo) List(ctx context.Context, f ListCamerasFilter) ([]*Camera, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var sb strings.Builder
	sb.WriteString(cameraSelect)
	sb.WriteString(" WHERE 1=1")
	var args []any
	if f.GroupID != "" {
		sb.WriteString(" AND group_id = ?")
		args = append(args, f.GroupID)
	}
	if f.Enabled != nil {
		sb.WriteString(" AND enabled = ?")
		args = append(args, boolToInt(*f.Enabled))
	}
	if f.Cursor != "" {
		sb.WriteString(" AND id < ?")
		args = append(args, f.Cursor)
	}
	sb.WriteString(" ORDER BY id DESC LIMIT ?")
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Camera
	for rows.Next() {
		c, err := scanCamera(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Update writes the full row. Sets UpdatedAt = Now().
func (r *CamerasRepo) Update(ctx context.Context, c *Camera) error {
	var pairedAt, probeAt any
	if !c.PairedAt.IsZero() {
		pairedAt = FormatTime(c.PairedAt)
	}
	if !c.LastCapabilityProbeAt.IsZero() {
		probeAt = FormatTime(c.LastCapabilityProbeAt)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE cameras SET
			name = ?, display_name = NULLIF(?, ''), group_id = NULLIF(?, ''),
			manufacturer = NULLIF(?, ''), model = NULLIF(?, ''),
			serial_number = NULLIF(?, ''), firmware_version = NULLIF(?, ''),
			mac_address = NULLIF(?, ''), ip_address = NULLIF(?, ''),
			hostname = NULLIF(?, ''),
			source_type = ?, source_url = ?, onvif_xaddr = NULLIF(?, ''),
			recording_policy_id = NULLIF(?, ''), enabled = ?,
			updated_at = ?, paired_at = ?, last_capability_probe_at = ?
		WHERE id = ?
	`,
		c.Name, c.DisplayName, c.GroupID,
		c.Manufacturer, c.Model,
		c.SerialNumber, c.FirmwareVersion,
		c.MACAddress, c.IPAddress,
		c.Hostname,
		c.SourceType, c.SourceURL, c.OnvifXAddr,
		c.RecordingPolicyID, boolToInt(c.Enabled),
		Now(), pairedAt, probeAt,
		c.ID,
	)
	if err != nil && isConstraintErr(err) {
		return ErrCameraExists
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCameraNotFound
	}
	c.UpdatedAt = time.Now().UTC()
	return nil
}

// SetEnabled flips the enabled flag.
func (r *CamerasRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE cameras SET enabled = ?, updated_at = ? WHERE id = ?
	`, boolToInt(enabled), Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// SetFirmwareVersion records the firmware version observed during probe.
func (r *CamerasRepo) SetFirmwareVersion(ctx context.Context, id, version string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE cameras SET firmware_version = NULLIF(?, ''), updated_at = ? WHERE id = ?
	`, version, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// SetPaired records when the camera was paired (post-handshake or manual).
func (r *CamerasRepo) SetPaired(ctx context.Context, id string, t time.Time) error {
	var v any
	if !t.IsZero() {
		v = FormatTime(t)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE cameras SET paired_at = ?, updated_at = ? WHERE id = ?
	`, v, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// SetLastCapabilityProbe records the time of the most recent probe.
func (r *CamerasRepo) SetLastCapabilityProbe(ctx context.Context, id string, t time.Time) error {
	var v any
	if !t.IsZero() {
		v = FormatTime(t)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE cameras SET last_capability_probe_at = ?, updated_at = ? WHERE id = ?
	`, v, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// Delete removes the camera. Schema cascades take credentials, capabilities,
// health, events, clips, snapshots; the camera service is responsible for
// tearing down ONVIF subscriptions BEFORE calling this.
func (r *CamerasRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM cameras WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCameraNotFound
	}
	return nil
}

const cameraSelect = `
SELECT id, name, COALESCE(display_name, ''), COALESCE(group_id, ''),
       COALESCE(manufacturer, ''), COALESCE(model, ''),
       COALESCE(serial_number, ''), COALESCE(firmware_version, ''),
       COALESCE(mac_address, ''), COALESCE(ip_address, ''),
       COALESCE(hostname, ''),
       source_type, source_url, COALESCE(onvif_xaddr, ''),
       COALESCE(recording_policy_id, ''), enabled,
       created_at, updated_at,
       COALESCE(paired_at, ''), COALESCE(last_capability_probe_at, '')
FROM cameras
`

func scanCamera(row rowScanner) (*Camera, error) {
	var c Camera
	var enabled int
	var createdAt, updatedAt, pairedAt, probeAt string
	if err := row.Scan(
		&c.ID, &c.Name, &c.DisplayName, &c.GroupID,
		&c.Manufacturer, &c.Model,
		&c.SerialNumber, &c.FirmwareVersion,
		&c.MACAddress, &c.IPAddress,
		&c.Hostname,
		&c.SourceType, &c.SourceURL, &c.OnvifXAddr,
		&c.RecordingPolicyID, &enabled,
		&createdAt, &updatedAt,
		&pairedAt, &probeAt,
	); err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	t, err = ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	c.UpdatedAt = t
	if pairedAt != "" {
		if t, err := ParseTime(pairedAt); err == nil {
			c.PairedAt = t
		}
	}
	if probeAt != "" {
		if t, err := ParseTime(probeAt); err == nil {
			c.LastCapabilityProbeAt = t
		}
	}
	return &c, nil
}
