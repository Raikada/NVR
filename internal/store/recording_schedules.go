package store

import (
	"context"
	"database/sql"
	"errors"
)

// RecordingSchedule is a row in recording_schedules. Site-local time
// windows; the timezone setting comes from system_settings['timezone'].
type RecordingSchedule struct {
	ID          string
	PolicyID    string
	DayOfWeek   int // 0=Sun..6=Sat
	StartMinute int // 0..1439 site-local
	EndMinute   int // exclusive; if < start, wraps midnight
}

// RecordingSchedulesRepo provides per-policy schedule list management.
type RecordingSchedulesRepo struct {
	db *sql.DB
}

// ErrRecordingScheduleNotFound is returned by mutators on miss.
var ErrRecordingScheduleNotFound = errors.New("recording schedule not found")

// ListByPolicy returns all schedules for a policy, sorted by day_of_week
// then start_minute (deterministic).
func (r *RecordingSchedulesRepo) ListByPolicy(ctx context.Context, policyID string) ([]*RecordingSchedule, error) {
	rows, err := r.db.QueryContext(ctx, recordingScheduleSelect+`
		WHERE policy_id = ?
		ORDER BY day_of_week ASC, start_minute ASC
	`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RecordingSchedule
	for rows.Next() {
		sch, err := scanRecordingSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sch)
	}
	return out, rows.Err()
}

// ReplaceAllForPolicy deletes every existing schedule for the policy and
// inserts the new set in a single transaction. Atomic: a failure in any
// insert rolls back the delete.
func (r *RecordingSchedulesRepo) ReplaceAllForPolicy(ctx context.Context, policyID string, schedules []*RecordingSchedule) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM recording_schedules WHERE policy_id = ?`, policyID); err != nil {
		return err
	}
	for _, s := range schedules {
		s.PolicyID = policyID
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO recording_schedules (id, policy_id, day_of_week, start_minute, end_minute)
			VALUES (?, ?, ?, ?, ?)
		`, s.ID, s.PolicyID, s.DayOfWeek, s.StartMinute, s.EndMinute); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteByPolicy removes every schedule for a policy. Helper used during
// policy deletion (cascade also fires via FK).
func (r *RecordingSchedulesRepo) DeleteByPolicy(ctx context.Context, policyID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM recording_schedules WHERE policy_id = ?`, policyID)
	return err
}

const recordingScheduleSelect = `
SELECT id, policy_id, day_of_week, start_minute, end_minute
FROM recording_schedules
`

func scanRecordingSchedule(row rowScanner) (*RecordingSchedule, error) {
	var s RecordingSchedule
	if err := row.Scan(&s.ID, &s.PolicyID, &s.DayOfWeek, &s.StartMinute, &s.EndMinute); err != nil {
		return nil, err
	}
	return &s, nil
}
