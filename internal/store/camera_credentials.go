package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CameraCredentials is a row in camera_credentials. The vault writes
// AES-GCM ciphertext+nonce produced by internal/cameracred; this repo is
// opaque to the encryption scheme.
type CameraCredentials struct {
	CameraID                string
	Username                string
	PasswordCiphertext      []byte
	PasswordNonce           []byte
	OnvifUsername           string
	OnvifPasswordCiphertext []byte
	OnvifPasswordNonce      []byte
	RotatedAt               time.Time
	RotationDueAt           time.Time // zero if not set
}

// CameraCredentialsRepo provides upsert/read of credentials.
type CameraCredentialsRepo struct {
	db *sql.DB
}

// ErrCameraCredentialsNotFound is returned by Get when no row matches.
var ErrCameraCredentialsNotFound = errors.New("camera credentials not found")

// Upsert inserts or replaces credentials for a camera; sets RotatedAt = Now().
func (r *CameraCredentialsRepo) Upsert(ctx context.Context, c *CameraCredentials) error {
	rotated := time.Now().UTC()
	c.RotatedAt = rotated
	var rotationDue any
	if !c.RotationDueAt.IsZero() {
		rotationDue = FormatTime(c.RotationDueAt)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_credentials
		    (camera_id, username, password_ciphertext, password_nonce,
		     onvif_username, onvif_password_ciphertext, onvif_password_nonce,
		     rotated_at, rotation_due_at)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)
		ON CONFLICT(camera_id) DO UPDATE SET
		    username = excluded.username,
		    password_ciphertext = excluded.password_ciphertext,
		    password_nonce = excluded.password_nonce,
		    onvif_username = excluded.onvif_username,
		    onvif_password_ciphertext = excluded.onvif_password_ciphertext,
		    onvif_password_nonce = excluded.onvif_password_nonce,
		    rotated_at = excluded.rotated_at,
		    rotation_due_at = excluded.rotation_due_at
	`,
		c.CameraID, c.Username, c.PasswordCiphertext, c.PasswordNonce,
		c.OnvifUsername, c.OnvifPasswordCiphertext, c.OnvifPasswordNonce,
		FormatTime(rotated), rotationDue,
	)
	return err
}

// Get fetches credentials for a camera. Returns ErrCameraCredentialsNotFound
// if no row.
func (r *CameraCredentialsRepo) Get(ctx context.Context, cameraID string) (*CameraCredentials, error) {
	row := r.db.QueryRowContext(ctx, cameraCredentialsSelect+` WHERE camera_id = ?`, cameraID)
	c, err := scanCameraCredentials(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCameraCredentialsNotFound
	}
	return c, err
}

// Exists returns true iff a row exists for cameraID. Used by serializers
// that need to populate password_set without decrypting.
func (r *CameraCredentialsRepo) Exists(ctx context.Context, cameraID string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM camera_credentials WHERE camera_id = ?`, cameraID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

const cameraCredentialsSelect = `
SELECT camera_id, username, password_ciphertext, password_nonce,
       COALESCE(onvif_username, ''),
       onvif_password_ciphertext, onvif_password_nonce,
       rotated_at, COALESCE(rotation_due_at, '')
FROM camera_credentials
`

func scanCameraCredentials(row rowScanner) (*CameraCredentials, error) {
	var c CameraCredentials
	var rotatedAt, rotationDueAt string
	if err := row.Scan(
		&c.CameraID, &c.Username, &c.PasswordCiphertext, &c.PasswordNonce,
		&c.OnvifUsername, &c.OnvifPasswordCiphertext, &c.OnvifPasswordNonce,
		&rotatedAt, &rotationDueAt,
	); err != nil {
		return nil, err
	}
	t, err := ParseTime(rotatedAt)
	if err != nil {
		return nil, err
	}
	c.RotatedAt = t
	if rotationDueAt != "" {
		if t, err := ParseTime(rotationDueAt); err == nil {
			c.RotationDueAt = t
		}
	}
	return &c, nil
}
