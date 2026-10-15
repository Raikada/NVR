// Package notifications builds canonical webhook/email payloads from
// canonical events, signs webhooks with HMAC-SHA-256, and renders the
// HTML email template. The dispatcher runs an outbox worker pool over
// notification_outbox rows.
package notifications

import (
	"encoding/json"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

// PayloadSchema is the canonical schema string included in every payload.
const PayloadSchema = "raikada.event.v1"

// Payload is the JSON shape POSTed to webhook targets and rendered into
// email bodies. It is also stored verbatim in notification_outbox.
type Payload struct {
	Schema       string       `json:"schema"`
	DeliveryID   string       `json:"delivery_id"`
	Site         SitePayload  `json:"site"`
	Event        EventPayload `json:"event"`
	SnapshotURL  string       `json:"snapshot_url"`
	ThumbnailURL string       `json:"thumbnail_url"`
	ClipURL      string       `json:"clip_url,omitempty"`
}

// SitePayload identifies the recorder.
type SitePayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// EventPayload is the per-event slice of the payload.
type EventPayload struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	TypeDisplayName string          `json:"type_display_name"`
	Camera          CameraPayload   `json:"camera"`
	Source          string          `json:"source"`
	Severity        string          `json:"severity,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at"`
	ReceivedAt      time.Time       `json:"received_at"`
	Region          json.RawMessage `json:"region,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

// CameraPayload identifies the camera that produced the event.
type CameraPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

// Build renders a Payload from an event + the site/camera/type lookups
// the caller supplies. URLs are filled in by the dispatcher (it knows
// the recorder's external URL); this function leaves them empty.
func Build(deliveryID string, site SitePayload, ev *events.Event, cam *store.Camera, evType *store.EventType) *Payload {
	return &Payload{
		Schema:     PayloadSchema,
		DeliveryID: deliveryID,
		Site:       site,
		Event: EventPayload{
			ID:              ev.ID,
			Type:            ev.TypeID,
			TypeDisplayName: evType.DisplayName,
			Camera: CameraPayload{
				ID: cam.ID, Name: cam.Name, DisplayName: cam.DisplayName,
			},
			Source: ev.Source, Severity: ev.Severity,
			OccurredAt: ev.OccurredAt, ReceivedAt: ev.ReceivedAt,
			Region: ev.RegionJSON, Payload: ev.PayloadJSON,
		},
	}
}
