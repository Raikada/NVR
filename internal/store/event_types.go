package store

import (
	"context"
	"database/sql"
	"errors"
)

// EventType is a row in event_types. Operator-extensible registry; the
// well-known seeded types are protected from deletion (vendor != 'custom').
type EventType struct {
	ID          string
	DisplayName string
	Vendor      string // onvif|amcrest|hikvision|reolink|internal|custom
	Description string
	// CaptureSnapshot: capture a JPEG when an event of this type
	// arrives (SP4). Defaults on; connectivity events opt out.
	CaptureSnapshot bool
}

// EventTypesRepo provides CRUD over event_types.
type EventTypesRepo struct {
	db *sql.DB
}

// ErrEventTypeExists is returned by Insert when the id is taken.
var ErrEventTypeExists = errors.New("event type already exists")

// ErrEventTypeNotFound is returned by per-id mutators on miss.
var ErrEventTypeNotFound = errors.New("event type not found")

// ErrEventTypeProtected is returned by Delete when the type is a seeded
// well-known (vendor != 'custom').
var ErrEventTypeProtected = errors.New("event type is protected (built-in)")

// Insert adds a new event type.
func (r *EventTypesRepo) Insert(ctx context.Context, e *EventType) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO event_types (id, display_name, vendor, description, capture_snapshot)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)
	`, e.ID, e.DisplayName, e.Vendor, e.Description, boolToInt(e.CaptureSnapshot))
	if err != nil && isConstraintErr(err) {
		return ErrEventTypeExists
	}
	return err
}

// GetByID fetches an event type by id.
func (r *EventTypesRepo) GetByID(ctx context.Context, id string) (*EventType, error) {
	row := r.db.QueryRowContext(ctx, eventTypeSelect+` WHERE id = ?`, id)
	return scanEventType(row)
}

// List returns every event type, ordered by display_name.
func (r *EventTypesRepo) List(ctx context.Context) ([]*EventType, error) {
	rows, err := r.db.QueryContext(ctx, eventTypeSelect+` ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EventType
	for rows.Next() {
		e, err := scanEventType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Update changes only DisplayName + Description; vendor is immutable.
func (r *EventTypesRepo) Update(ctx context.Context, e *EventType) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE event_types SET display_name = ?, description = NULLIF(?, ''),
			capture_snapshot = ?
		WHERE id = ?
	`, e.DisplayName, e.Description, boolToInt(e.CaptureSnapshot), e.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEventTypeNotFound
	}
	return nil
}

// Delete removes the event type. Refuses if vendor != 'custom'.
func (r *EventTypesRepo) Delete(ctx context.Context, id string) error {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEventTypeNotFound
		}
		return err
	}
	if existing.Vendor != "custom" {
		return ErrEventTypeProtected
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM event_types WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEventTypeNotFound
	}
	return nil
}

const eventTypeSelect = `
SELECT id, display_name, COALESCE(vendor, ''), COALESCE(description, ''),
       capture_snapshot
FROM event_types
`

func scanEventType(row rowScanner) (*EventType, error) {
	var e EventType
	var capture int
	if err := row.Scan(&e.ID, &e.DisplayName, &e.Vendor, &e.Description, &capture); err != nil {
		return nil, err
	}
	e.CaptureSnapshot = capture != 0
	return &e, nil
}
