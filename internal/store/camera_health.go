package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CameraHealth is a row in camera_health. Hot path — upserted on every
// state change observed by internal/cameras/healthCollector.
type CameraHealth struct {
	CameraID            string
	RTSPState           string
	LastKeyframeAt      time.Time
	LastEventAt         time.Time
	LastSeenAt          time.Time
	ConsecutiveFailures int
	LastError           string
	UpdatedAt           time.Time
}

// CameraHealthRepo provides upsert + counter helpers.
type CameraHealthRepo struct {
	db *sql.DB
}

// ErrCameraHealthNotFound is returned by Get when no row matches.
var ErrCameraHealthNotFound = errors.New("camera health not found")

// Upsert inserts or replaces the health row; sets UpdatedAt = Now().
func (r *CameraHealthRepo) Upsert(ctx context.Context, h *CameraHealth) error {
	now := time.Now().UTC()
	h.UpdatedAt = now
	var lastKey, lastEvt, lastSeen any
	if !h.LastKeyframeAt.IsZero() {
		lastKey = FormatTime(h.LastKeyframeAt)
	}
	if !h.LastEventAt.IsZero() {
		lastEvt = FormatTime(h.LastEventAt)
	}
	if !h.LastSeenAt.IsZero() {
		lastSeen = FormatTime(h.LastSeenAt)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_health
		    (camera_id, rtsp_state, last_keyframe_at, last_event_at,
		     last_seen_at, consecutive_failures, last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)
		ON CONFLICT(camera_id) DO UPDATE SET
		    rtsp_state = excluded.rtsp_state,
		    last_keyframe_at = excluded.last_keyframe_at,
		    last_event_at = excluded.last_event_at,
		    last_seen_at = excluded.last_seen_at,
		    consecutive_failures = excluded.consecutive_failures,
		    last_error = excluded.last_error,
		    updated_at = excluded.updated_at
	`,
		h.CameraID, h.RTSPState, lastKey, lastEvt, lastSeen,
		h.ConsecutiveFailures, h.LastError, FormatTime(now),
	)
	return err
}

// Get fetches the health row for a camera.
func (r *CameraHealthRepo) Get(ctx context.Context, cameraID string) (*CameraHealth, error) {
	row := r.db.QueryRowContext(ctx, cameraHealthSelect+` WHERE camera_id = ?`, cameraID)
	h, err := scanCameraHealth(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCameraHealthNotFound
	}
	return h, err
}

// IncrementFailure atomically bumps consecutive_failures and writes
// last_error + updated_at. Inserts a row with rtsp_state='failed' if none
// exists.
func (r *CameraHealthRepo) IncrementFailure(ctx context.Context, cameraID, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_health
		    (camera_id, rtsp_state, consecutive_failures, last_error, updated_at)
		VALUES (?, 'failed', 1, NULLIF(?, ''), ?)
		ON CONFLICT(camera_id) DO UPDATE SET
		    consecutive_failures = consecutive_failures + 1,
		    last_error = excluded.last_error,
		    updated_at = excluded.updated_at
	`, cameraID, errMsg, Now())
	return err
}

// ResetFailures sets consecutive_failures=0 and clears last_error.
func (r *CameraHealthRepo) ResetFailures(ctx context.Context, cameraID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE camera_health SET consecutive_failures = 0, last_error = NULL, updated_at = ?
		WHERE camera_id = ?
	`, Now(), cameraID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCameraHealthNotFound
	}
	return nil
}

// TouchKeyframe sets last_keyframe_at and last_seen_at.
func (r *CameraHealthRepo) TouchKeyframe(ctx context.Context, cameraID string, t time.Time) error {
	formatted := FormatTime(t)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_health
		    (camera_id, rtsp_state, last_keyframe_at, last_seen_at, updated_at)
		VALUES (?, 'connected', ?, ?, ?)
		ON CONFLICT(camera_id) DO UPDATE SET
		    last_keyframe_at = excluded.last_keyframe_at,
		    last_seen_at = excluded.last_seen_at,
		    updated_at = excluded.updated_at
	`, cameraID, formatted, formatted, Now())
	return err
}

// TouchEvent sets last_event_at and last_seen_at.
func (r *CameraHealthRepo) TouchEvent(ctx context.Context, cameraID string, t time.Time) error {
	formatted := FormatTime(t)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO camera_health
		    (camera_id, rtsp_state, last_event_at, last_seen_at, updated_at)
		VALUES (?, 'connected', ?, ?, ?)
		ON CONFLICT(camera_id) DO UPDATE SET
		    last_event_at = excluded.last_event_at,
		    last_seen_at = excluded.last_seen_at,
		    updated_at = excluded.updated_at
	`, cameraID, formatted, formatted, Now())
	return err
}

const cameraHealthSelect = `
SELECT camera_id, rtsp_state,
       COALESCE(last_keyframe_at, ''), COALESCE(last_event_at, ''),
       COALESCE(last_seen_at, ''),
       consecutive_failures, COALESCE(last_error, ''), updated_at
FROM camera_health
`

func scanCameraHealth(row rowScanner) (*CameraHealth, error) {
	var h CameraHealth
	var lastKey, lastEvt, lastSeen, updatedAt string
	if err := row.Scan(
		&h.CameraID, &h.RTSPState,
		&lastKey, &lastEvt, &lastSeen,
		&h.ConsecutiveFailures, &h.LastError, &updatedAt,
	); err != nil {
		return nil, err
	}
	if lastKey != "" {
		if t, err := ParseTime(lastKey); err == nil {
			h.LastKeyframeAt = t
		}
	}
	if lastEvt != "" {
		if t, err := ParseTime(lastEvt); err == nil {
			h.LastEventAt = t
		}
	}
	if lastSeen != "" {
		if t, err := ParseTime(lastSeen); err == nil {
			h.LastSeenAt = t
		}
	}
	t, err := ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	h.UpdatedAt = t
	return &h, nil
}
