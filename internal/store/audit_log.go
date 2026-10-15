package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuditEntry is a row in audit_log. Append-only.
type AuditEntry struct {
	ID            string
	OccurredAt    time.Time
	ActorUserID   string // empty when system
	ActorUsername string // denormalized snapshot
	ActorIP       string
	Action        string
	TargetKind    string
	TargetID      string
	BeforeJSON    string
	AfterJSON     string
	Details       string
}

// ListAuditFilter is the filter struct for AuditLogRepo.List.
type ListAuditFilter struct {
	ActorUserID string
	Action      string
	TargetKind  string
	TargetID    string
	From        time.Time
	To          time.Time
	Cursor      string
	Limit       int
}

// AuditLogRepo provides append-only access to audit_log.
type AuditLogRepo struct {
	db *sql.DB
}

// ErrAuditEntryNotFound is returned by GetByID when no row matches.
var ErrAuditEntryNotFound = errors.New("audit entry not found")

// Insert appends an entry. Generates ID if zero. Sets OccurredAt = Now()
// if zero.
func (r *AuditLogRepo) Insert(ctx context.Context, e *AuditEntry) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_log (
			id, occurred_at, actor_user_id, actor_username, actor_ip,
			action, target_kind, target_id, before_json, after_json, details
		)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		        ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))
	`,
		e.ID, FormatTime(e.OccurredAt),
		e.ActorUserID, e.ActorUsername, e.ActorIP,
		e.Action, e.TargetKind, e.TargetID,
		e.BeforeJSON, e.AfterJSON, e.Details,
	)
	return err
}

// GetByID fetches an entry by id.
func (r *AuditLogRepo) GetByID(ctx context.Context, id string) (*AuditEntry, error) {
	row := r.db.QueryRowContext(ctx, auditEntrySelect+` WHERE id = ?`, id)
	e, err := scanAuditEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuditEntryNotFound
	}
	return e, err
}

// List returns entries matching filter, paginated by id DESC.
func (r *AuditLogRepo) List(ctx context.Context, f ListAuditFilter) ([]*AuditEntry, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var sb strings.Builder
	sb.WriteString(auditEntrySelect)
	sb.WriteString(" WHERE 1=1")
	var args []any
	if f.ActorUserID != "" {
		sb.WriteString(" AND actor_user_id = ?")
		args = append(args, f.ActorUserID)
	}
	if f.Action != "" {
		sb.WriteString(" AND action = ?")
		args = append(args, f.Action)
	}
	if f.TargetKind != "" {
		sb.WriteString(" AND target_kind = ?")
		args = append(args, f.TargetKind)
	}
	if f.TargetID != "" {
		sb.WriteString(" AND target_id = ?")
		args = append(args, f.TargetID)
	}
	if !f.From.IsZero() {
		sb.WriteString(" AND occurred_at >= ?")
		args = append(args, FormatTime(f.From))
	}
	if !f.To.IsZero() {
		sb.WriteString(" AND occurred_at <= ?")
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
		return nil, "", err
	}
	defer rows.Close()
	var out []*AuditEntry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var nextCursor string
	if len(out) == limit && limit > 0 {
		nextCursor = out[len(out)-1].ID
	}
	return out, nextCursor, nil
}

// StreamForExport returns a channel that emits audit entries in the time
// range fromTime..toTime. The goroutine closes the channel when finished
// or when ctx is cancelled.
func (r *AuditLogRepo) StreamForExport(ctx context.Context, fromTime, toTime time.Time) (<-chan *AuditEntry, error) {
	rows, err := r.db.QueryContext(ctx, auditEntrySelect+`
		WHERE occurred_at >= ? AND occurred_at <= ?
		ORDER BY occurred_at ASC
	`, FormatTime(fromTime), FormatTime(toTime))
	if err != nil {
		return nil, err
	}
	out := make(chan *AuditEntry)
	go func() {
		defer rows.Close()
		defer close(out)
		for rows.Next() {
			e, err := scanAuditEntry(rows)
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case out <- e:
			}
		}
	}()
	return out, nil
}

// PurgeOlderThan removes every row with occurred_at < t. Returns count.
func (r *AuditLogRepo) PurgeOlderThan(ctx context.Context, t time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM audit_log WHERE occurred_at < ?`, FormatTime(t))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

const auditEntrySelect = `
SELECT id, occurred_at, COALESCE(actor_user_id, ''), COALESCE(actor_username, ''),
       COALESCE(actor_ip, ''), action, COALESCE(target_kind, ''), COALESCE(target_id, ''),
       COALESCE(before_json, ''), COALESCE(after_json, ''), COALESCE(details, '')
FROM audit_log
`

func scanAuditEntry(row rowScanner) (*AuditEntry, error) {
	var e AuditEntry
	var occurredAt string
	if err := row.Scan(&e.ID, &occurredAt, &e.ActorUserID, &e.ActorUsername,
		&e.ActorIP, &e.Action, &e.TargetKind, &e.TargetID,
		&e.BeforeJSON, &e.AfterJSON, &e.Details); err != nil {
		return nil, err
	}
	t, err := ParseTime(occurredAt)
	if err != nil {
		return nil, err
	}
	e.OccurredAt = t
	return &e, nil
}
