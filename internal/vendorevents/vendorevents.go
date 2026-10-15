// Package vendorevents supervises per-camera vendor event channels
// (SP3): camera-side detections (ONVIF PullPoint, Amcrest CGI) are
// normalized onto the seeded event_types vocabulary and funneled into
// events.Service, with camerahealth.last_event_at stamped via Touch.
//
// One channel per camera; selection order: explicit event_channel
// override → manufacturer (Amcrest) → ONVIF XAddr presence → none.
package vendorevents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

// ErrUnsupported is returned by an AdapterFactory when the camera can't
// speak that channel; the supervisor logs once and does not retry.
var ErrUnsupported = errors.New("vendorevents: unsupported camera")

// NormalizedEvent is what every adapter emits: the vendor payload
// reduced to the seeded event_types vocabulary.
type NormalizedEvent struct {
	TypeID     string
	Severity   string // info|warning; empty defaults to info
	OccurredAt time.Time
	Payload    json.RawMessage
}

// Adapter is one camera-scoped event channel. Run blocks, delivering
// events to emit until ctx cancels or the channel dies (non-nil error
// return → supervisor backoff-restarts).
type Adapter interface {
	Run(ctx context.Context, emit func(NormalizedEvent)) error
}

// ChannelCamera carries everything a factory needs to open a channel.
// Credentials is a late-bound closure so plaintext never sits in a
// struct field.
type ChannelCamera struct {
	ID           string
	Name         string
	Host         string // camera host from source_url
	OnvifXAddr   string
	EventsXAddr  string // from SP2 capability probe, may be empty
	Manufacturer string
	Channel      string // raw event_channel override ('', auto, onvif, amcrest, none)
	Credentials  func(ctx context.Context) (username, password string, err error)
}

// AdapterFactory builds an adapter for a camera, or reports it
// unsupported.
type AdapterFactory func(ctx context.Context, cam ChannelCamera) (Adapter, error)

// EventSink abstracts events.Service.
type EventSink interface {
	Insert(ctx context.Context, ev *events.Event) error
}

// Toucher stamps camera_health.last_event_at (camerahealth.Collector.Touch).
type Toucher func(cameraID string, at time.Time)

// CameraLister abstracts the camera store.
type CameraLister interface {
	List(ctx context.Context, f store.ListCamerasFilter) ([]*store.Camera, error)
}

// SelectChannel resolves which channel a camera should run.
func SelectChannel(override, manufacturer, onvifXAddr string) string {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "onvif":
		return "onvif"
	case "amcrest":
		return "amcrest"
	case "none":
		return "none"
	}
	// auto / empty
	if strings.EqualFold(strings.TrimSpace(manufacturer), "amcrest") {
		return "amcrest"
	}
	if strings.TrimSpace(onvifXAddr) != "" {
		return "onvif"
	}
	return "none"
}
