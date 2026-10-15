package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Clip is a row in clips. State machine: requested -> preparing -> ready
// (or failed). OutputPath is populated only on ready.
type Clip struct {
	ID              string
	EventID         string // empty for ad-hoc clips (no parent event)
	CameraID        string
	StartTime       time.Time
	EndTime         time.Time
	PreRollSeconds  int
	PostRollSeconds int
	State           string // requested|preparing|ready|failed
	Format          string // 'fmp4'
	OutputPath      string
	SizeBytes       int64
	Checksum        string
	CreatedBy       string
	CreatedAt       time.Time
	ReadyAt         time.Time
	ExpiresAt       time.Time
}

// ListClipsFilter is the filter struct for ClipsRepo.List.
type ListClipsFilter struct {
	CameraID string
	EventID  string
	State    string
	From     time.Time
	To       time.Time
	Cursor   string
	Limit    int
}

// ClipsRepo provides CRUD over clips.
type ClipsRepo struct {
	db *sql.DB
}

// ErrClipNotFound is returned by per-id mutators when no row matches.
var ErrClipNotFound = errors.New("clip not found")

// Insert adds a new clip with state='requested'. Sets CreatedAt = Now().
func (r *ClipsRepo) Insert(ctx context.Context, c *Clip) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.State == "" {
		c.State = "requested"
	}
	if c.Format == "" {
		c.Format = "fmp4"
	}
	var expiresAt any
	if !c.ExpiresAt.IsZero() {
		expiresAt = FormatTime(c.ExpiresAt)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clips (
			id, event_id, camera_id, start_time, end_time,
			pre_roll_seconds, post_roll_seconds,
			state, format, output_path, size_bytes, checksum,
			created_by, created_at, ready_at, expires_at
		)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, NULLIF(?, ''), ?, NULL, ?)
	`,
		c.ID, c.EventID, c.CameraID,
		FormatTime(c.StartTime), FormatTime(c.EndTime),
		c.PreRollSeconds, c.PostRollSeconds,
		c.State, c.Format,
		c.CreatedBy, FormatTime(c.CreatedAt), expiresAt,
	)
	return err
}

// GetByID fetches a clip by id.
func (r *ClipsRepo) GetByID(ctx context.Context, id string) (*Clip, error) {
	row := r.db.QueryRowContext(ctx, clipSelect+` WHERE id = ?`, id)
	c, err := scanClip(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClipNotFound
	}
	return c, err
}

// List returns clips matching filter.
func (r *ClipsRepo) List(ctx context.Context, f ListClipsFilter) ([]*Clip, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var sb strings.Builder
	sb.WriteString(clipSelect)
	sb.WriteString(" WHERE 1=1")
	var args []any
	if f.CameraID != "" {
		sb.WriteString(" AND camera_id = ?")
		args = append(args, f.CameraID)
	}
	if f.EventID != "" {
		sb.WriteString(" AND event_id = ?")
		args = append(args, f.EventID)
	}
	if f.State != "" {
		sb.WriteString(" AND state = ?")
		args = append(args, f.State)
	}
	if !f.From.IsZero() {
		sb.WriteString(" AND start_time >= ?")
		args = append(args, FormatTime(f.From))
	}
	if !f.To.IsZero() {
		sb.WriteString(" AND start_time <= ?")
		args = append(args, FormatTime(f.To))
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
	var out []*Clip
	for rows.Next() {
		c, err := scanClip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetPreparing flips a clip to state='preparing'.
func (r *ClipsRepo) SetPreparing(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE clips SET state = 'preparing' WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrClipNotFound
	}
	return nil
}

// SetReady flips a clip to state='ready' and records output metadata.
func (r *ClipsRepo) SetReady(ctx context.Context, id, outputPath, checksum string, sizeBytes int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE clips SET state = 'ready', output_path = ?, size_bytes = ?, checksum = NULLIF(?, ''),
		    ready_at = ?
		WHERE id = ?
	`, outputPath, sizeBytes, checksum, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrClipNotFound
	}
	return nil
}

// SetFailed flips a clip to state='failed' and records the last error.
func (r *ClipsRepo) SetFailed(ctx context.Context, id, lastErr string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE clips SET state = 'failed', checksum = NULLIF(?, '') WHERE id = ?
	`, lastErr, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrClipNotFound
	}
	return nil
}

// Delete removes a clip.
func (r *ClipsRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM clips WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrClipNotFound
	}
	return nil
}

// DeleteExpiredReady removes up to batchSize ready clips with expires_at
// in the past, returning their output_paths so the caller can unlink files.
func (r *ClipsRepo) DeleteExpiredReady(ctx context.Context, batchSize int) ([]string, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, COALESCE(output_path, '') FROM clips
		WHERE state = 'ready' AND expires_at IS NOT NULL AND expires_at < ?
		ORDER BY expires_at LIMIT ?
	`, Now(), batchSize)
	if err != nil {
		return nil, err
	}
	var ids []string
	var paths []string
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		if path != "" {
			paths = append(paths, path)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM clips WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

const clipSelect = `
SELECT id, COALESCE(event_id, ''), camera_id, start_time, end_time,
       pre_roll_seconds, post_roll_seconds,
       state, format, COALESCE(output_path, ''), COALESCE(size_bytes, 0),
       COALESCE(checksum, ''),
       COALESCE(created_by, ''), created_at,
       COALESCE(ready_at, ''), COALESCE(expires_at, '')
FROM clips
`

func scanClip(row rowScanner) (*Clip, error) {
	var c Clip
	var startTime, endTime, createdAt, readyAt, expiresAt string
	if err := row.Scan(
		&c.ID, &c.EventID, &c.CameraID, &startTime, &endTime,
		&c.PreRollSeconds, &c.PostRollSeconds,
		&c.State, &c.Format, &c.OutputPath, &c.SizeBytes,
		&c.Checksum, &c.CreatedBy, &createdAt,
		&readyAt, &expiresAt,
	); err != nil {
		return nil, err
	}
	t, err := ParseTime(startTime)
	if err != nil {
		return nil, err
	}
	c.StartTime = t
	t, err = ParseTime(endTime)
	if err != nil {
		return nil, err
	}
	c.EndTime = t
	t, err = ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	if readyAt != "" {
		if t, err := ParseTime(readyAt); err == nil {
			c.ReadyAt = t
		}
	}
	if expiresAt != "" {
		if t, err := ParseTime(expiresAt); err == nil {
			c.ExpiresAt = t
		}
	}
	return &c, nil
}
