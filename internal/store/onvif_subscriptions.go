package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// OnvifSubscription is a persisted PullPoint subscription. The shape
// mirrors internal/onvif.SubscriptionRecord plus the fields the manager
// needs to resume the subscription after a restart (subscription_url,
// username, password).
type OnvifSubscription struct {
	ID              string
	CameraID        string
	XAddr           string
	Username        string
	Password        string
	SubscriptionURL string
	TerminationTime time.Time
	CreatedAt       time.Time
	State           string
	LastError       string
	LastEventAt     time.Time // zero if no events seen
	EventCount      int
}

// OnvifSubscriptionsRepo provides CRUD over onvif_subscriptions.
type OnvifSubscriptionsRepo struct {
	db *sql.DB
}

// ErrOnvifSubscriptionNotFound is returned when no row matches the
// supplied id.
var ErrOnvifSubscriptionNotFound = errors.New("onvif subscription not found")

// Insert persists a new subscription row.
func (r *OnvifSubscriptionsRepo) Insert(ctx context.Context, s *OnvifSubscription) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	state := s.State
	if state == "" {
		state = "active"
	}
	var lastEventAt any
	if !s.LastEventAt.IsZero() {
		lastEventAt = FormatTime(s.LastEventAt)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO onvif_subscriptions
		    (id, camera_id, xaddr, username, password,
		     subscription_url, termination_time, created_at,
		     state, last_error, last_event_at, event_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		s.ID, s.CameraID, s.XAddr, s.Username, s.Password,
		s.SubscriptionURL, FormatTime(s.TerminationTime), FormatTime(s.CreatedAt),
		state, s.LastError, lastEventAt, s.EventCount,
	)
	return err
}

// UpdateState writes the per-pull mutable fields back to the row.
// Idempotent; safe to call from the manager's run-loop on every event /
// renew / error tick.
func (r *OnvifSubscriptionsRepo) UpdateState(ctx context.Context, s *OnvifSubscription) error {
	var lastEventAt any
	if !s.LastEventAt.IsZero() {
		lastEventAt = FormatTime(s.LastEventAt)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE onvif_subscriptions
		SET subscription_url = ?, termination_time = ?, state = ?,
		    last_error = ?, last_event_at = ?, event_count = ?
		WHERE id = ?
	`,
		s.SubscriptionURL, FormatTime(s.TerminationTime), s.State,
		s.LastError, lastEventAt, s.EventCount,
		s.ID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrOnvifSubscriptionNotFound
	}
	return nil
}

// Delete removes the row. Returns ErrOnvifSubscriptionNotFound if no
// such id.
func (r *OnvifSubscriptionsRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM onvif_subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrOnvifSubscriptionNotFound
	}
	return nil
}

// DeleteTerminated removes every row in state != 'active'. Called by
// the manager after a rehydrate pass to keep the table bounded.
func (r *OnvifSubscriptionsRepo) DeleteTerminated(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM onvif_subscriptions WHERE state != 'active'`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ListActive returns every active subscription, oldest first. The
// rehydrate path uses this on recorder startup.
func (r *OnvifSubscriptionsRepo) ListActive(ctx context.Context) ([]*OnvifSubscription, error) {
	rows, err := r.db.QueryContext(ctx, onvifSubscriptionSelect+`
		WHERE state = 'active'
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OnvifSubscription
	for rows.Next() {
		s, err := scanOnvifSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAll returns every row, including terminated/failed ones. Used by
// tests and by future audit/troubleshooting surfaces; not used by the
// manager itself.
func (r *OnvifSubscriptionsRepo) ListAll(ctx context.Context) ([]*OnvifSubscription, error) {
	rows, err := r.db.QueryContext(ctx, onvifSubscriptionSelect+`
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OnvifSubscription
	for rows.Next() {
		s, err := scanOnvifSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const onvifSubscriptionSelect = `
SELECT id, camera_id, xaddr, username, password,
       subscription_url, termination_time, created_at,
       state, last_error, COALESCE(last_event_at, ''), event_count
FROM onvif_subscriptions
`

func scanOnvifSubscription(row rowScanner) (*OnvifSubscription, error) {
	var s OnvifSubscription
	var terminationTime, createdAt, lastEventAt string
	if err := row.Scan(&s.ID, &s.CameraID, &s.XAddr, &s.Username, &s.Password,
		&s.SubscriptionURL, &terminationTime, &createdAt,
		&s.State, &s.LastError, &lastEventAt, &s.EventCount); err != nil {
		return nil, err
	}
	t, err := ParseTime(terminationTime)
	if err == nil {
		s.TerminationTime = t
	}
	t, err = ParseTime(createdAt)
	if err == nil {
		s.CreatedAt = t
	}
	if lastEventAt != "" {
		if t, err := ParseTime(lastEventAt); err == nil {
			s.LastEventAt = t
		}
	}
	return &s, nil
}
