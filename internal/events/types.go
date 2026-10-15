// Package events owns the canonical CameraEvent shape, the in-process
// bus that fans new events out to subscribers (notification dispatcher,
// snapshot fetcher, clip linker, SSE stream, cloud_outbox enqueuer), and
// the service that persists, materializes expires_at, and exposes
// list/ack/delete-expired operations to the API and retention sweepers.
package events

import (
	"encoding/json"
	"time"
)

// Event is the canonical in-memory shape used across the event pipeline.
// It mirrors the store.Event row but uses json.RawMessage for the JSON
// blobs to avoid reparse on the bus path.
type Event struct {
	ID             string
	CameraID       string
	TypeID         string
	Source         string
	OccurredAt     time.Time
	ReceivedAt     time.Time
	Severity       string
	PayloadJSON    json.RawMessage
	RegionJSON     json.RawMessage
	AcknowledgedAt time.Time
	AcknowledgedBy string
	ExpiresAt      time.Time
}

// ListFilter is the typed filter accepted by Service.List, mapped 1:1
// onto store.ListEventsFilter inside the service.
type ListFilter struct {
	CameraIDs []string
	TypeIDs   []string
	Sources   []string
	From, To  time.Time
	MinSev    string
	OnlyUnack bool
	Cursor    string
	Limit     int
}
