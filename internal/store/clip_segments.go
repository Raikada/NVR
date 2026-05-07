package store

import (
	"context"
	"database/sql"
)

// ClipSegment is a row in clip_segments. Composite PK (clip_id, segment_path).
type ClipSegment struct {
	ClipID        string
	SegmentPath   string
	StartOffsetMS int64
	DurationMS    int64
}

// ClipSegmentsRepo provides batch insert + listing of clip-to-segment mappings.
type ClipSegmentsRepo struct {
	db *sql.DB
}

// InsertBatch inserts every segment for a clip in a single transaction.
// Atomic: a failure in any insert rolls back the whole batch.
func (r *ClipSegmentsRepo) InsertBatch(ctx context.Context, clipID string, segments []*ClipSegment) error {
	if len(segments) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range segments {
		s.ClipID = clipID
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO clip_segments (clip_id, segment_path, start_offset_ms, duration_ms)
			VALUES (?, ?, ?, ?)
		`, s.ClipID, s.SegmentPath, s.StartOffsetMS, s.DurationMS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListByClip returns every segment for a clip, ordered by start_offset_ms.
func (r *ClipSegmentsRepo) ListByClip(ctx context.Context, clipID string) ([]*ClipSegment, error) {
	rows, err := r.db.QueryContext(ctx, clipSegmentSelect+`
		WHERE clip_id = ? ORDER BY start_offset_ms ASC
	`, clipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ClipSegment
	for rows.Next() {
		s, err := scanClipSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteByClip removes every segment for a clip. Cascade also fires via
// FK on clip deletion; explicit Delete is for tests + ad-hoc cleanup.
func (r *ClipSegmentsRepo) DeleteByClip(ctx context.Context, clipID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM clip_segments WHERE clip_id = ?`, clipID)
	return err
}

const clipSegmentSelect = `
SELECT clip_id, segment_path, start_offset_ms, duration_ms
FROM clip_segments
`

func scanClipSegment(row rowScanner) (*ClipSegment, error) {
	var s ClipSegment
	if err := row.Scan(&s.ClipID, &s.SegmentPath, &s.StartOffsetMS, &s.DurationMS); err != nil {
		return nil, err
	}
	return &s, nil
}
