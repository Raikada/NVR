package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RecordingPolicy is a row in recording_policies. The 'policy_default'
// row is seeded by migration 0010 and protected from deletion.
type RecordingPolicy struct {
	ID                        string
	Name                      string
	Mode                      string // continuous|motion|scheduled|off
	RetentionDurationSeconds  int64
	Container                 string // fmp4|mpegts
	MinSegmentDurationSeconds int64
	MaxSegmentDurationSeconds int64
	PartDurationMS            int64
	MaxPartSizeBytes          int64
	PreEventSeconds           int
	PostEventSeconds          int
	Enabled                   bool
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// RecordingPoliciesRepo provides CRUD over recording_policies.
type RecordingPoliciesRepo struct {
	db *sql.DB
}

// ErrRecordingPolicyExists is returned by Insert when the name is taken.
var ErrRecordingPolicyExists = errors.New("recording policy already exists")

// ErrRecordingPolicyNotFound is returned by per-id mutators on miss.
var ErrRecordingPolicyNotFound = errors.New("recording policy not found")

// ErrPolicyIsDefault is returned by Delete when called on policy_default.
var ErrPolicyIsDefault = errors.New("policy_default cannot be deleted")

// Insert adds a new recording policy.
func (r *RecordingPoliciesRepo) Insert(ctx context.Context, p *RecordingPolicy) error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO recording_policies (
			id, name, mode, retention_duration_seconds, container,
			min_segment_duration_seconds, max_segment_duration_seconds,
			part_duration_ms, max_part_size_bytes,
			pre_event_seconds, post_event_seconds, enabled,
			created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		p.ID, p.Name, p.Mode, p.RetentionDurationSeconds, p.Container,
		p.MinSegmentDurationSeconds, p.MaxSegmentDurationSeconds,
		p.PartDurationMS, p.MaxPartSizeBytes,
		p.PreEventSeconds, p.PostEventSeconds, boolToInt(p.Enabled),
		FormatTime(p.CreatedAt), FormatTime(p.UpdatedAt),
	)
	if err != nil && isConstraintErr(err) {
		return ErrRecordingPolicyExists
	}
	return err
}

// GetByID fetches a policy by id.
func (r *RecordingPoliciesRepo) GetByID(ctx context.Context, id string) (*RecordingPolicy, error) {
	row := r.db.QueryRowContext(ctx, recordingPolicySelect+` WHERE id = ?`, id)
	return scanRecordingPolicy(row)
}

// GetByName fetches a policy by name.
func (r *RecordingPoliciesRepo) GetByName(ctx context.Context, name string) (*RecordingPolicy, error) {
	row := r.db.QueryRowContext(ctx, recordingPolicySelect+` WHERE name = ?`, name)
	return scanRecordingPolicy(row)
}

// List returns every policy, ordered by name.
func (r *RecordingPoliciesRepo) List(ctx context.Context) ([]*RecordingPolicy, error) {
	rows, err := r.db.QueryContext(ctx, recordingPolicySelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RecordingPolicy
	for rows.Next() {
		p, err := scanRecordingPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Update writes the full row. Sets UpdatedAt = Now().
func (r *RecordingPoliciesRepo) Update(ctx context.Context, p *RecordingPolicy) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE recording_policies SET
			name = ?, mode = ?, retention_duration_seconds = ?, container = ?,
			min_segment_duration_seconds = ?, max_segment_duration_seconds = ?,
			part_duration_ms = ?, max_part_size_bytes = ?,
			pre_event_seconds = ?, post_event_seconds = ?, enabled = ?,
			updated_at = ?
		WHERE id = ?
	`,
		p.Name, p.Mode, p.RetentionDurationSeconds, p.Container,
		p.MinSegmentDurationSeconds, p.MaxSegmentDurationSeconds,
		p.PartDurationMS, p.MaxPartSizeBytes,
		p.PreEventSeconds, p.PostEventSeconds, boolToInt(p.Enabled),
		Now(),
		p.ID,
	)
	if err != nil && isConstraintErr(err) {
		return ErrRecordingPolicyExists
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRecordingPolicyNotFound
	}
	return nil
}

// Delete removes the policy. Refuses to delete 'policy_default'.
func (r *RecordingPoliciesRepo) Delete(ctx context.Context, id string) error {
	if id == "policy_default" {
		return ErrPolicyIsDefault
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM recording_policies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRecordingPolicyNotFound
	}
	return nil
}

const recordingPolicySelect = `
SELECT id, name, mode, retention_duration_seconds, container,
       min_segment_duration_seconds, max_segment_duration_seconds,
       part_duration_ms, max_part_size_bytes,
       pre_event_seconds, post_event_seconds, enabled,
       created_at, updated_at
FROM recording_policies
`

func scanRecordingPolicy(row rowScanner) (*RecordingPolicy, error) {
	var p RecordingPolicy
	var enabled int
	var createdAt, updatedAt string
	if err := row.Scan(
		&p.ID, &p.Name, &p.Mode, &p.RetentionDurationSeconds, &p.Container,
		&p.MinSegmentDurationSeconds, &p.MaxSegmentDurationSeconds,
		&p.PartDurationMS, &p.MaxPartSizeBytes,
		&p.PreEventSeconds, &p.PostEventSeconds, &enabled,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = t
	t, err = ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	p.UpdatedAt = t
	return &p, nil
}
