// Package cameras owns the canonical Camera entity, an in-process bus
// that fans CRUD changes out to subscribers (path-manager bridge,
// onvif manager), and credential vault integration. Insert/Update/Delete
// flow through Service so policy + onvif teardown can be invoked
// consistently regardless of caller (API, bootstrap, sync).
package cameras

import (
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

// ChangeType is the kind of camera lifecycle event published on the bus.
type ChangeType int

const (
	// ChangeCreated means a new camera was inserted.
	ChangeCreated ChangeType = iota + 1
	// ChangeUpdated means an existing camera's row or credentials changed.
	ChangeUpdated
	// ChangeDeleted means the camera was removed.
	ChangeDeleted
)

// Change is the bus payload for one Service mutation.
type Change struct {
	Type   ChangeType
	Camera *store.Camera // nil for ChangeDeleted; ID is in CameraID
	// CameraID is set on every Change so subscribers handling delete
	// don't need the Camera struct.
	CameraID string
}

// CameraPathSpec is the materialized path-source description handed to
// the path manager bridge. SourceURL has credentials inlined; callers
// must NEVER log it.
type CameraPathSpec struct {
	Name      string
	SourceURL string
}

// HealthSnapshot is the per-camera health view returned by Service.Health.
// Mirrors store.CameraHealth but is exported via the cameras surface so
// the API layer doesn't need a direct store dep for read-only views.
type HealthSnapshot struct {
	CameraID            string
	RTSPState           string
	LastKeyframeAt      time.Time
	LastEventAt         time.Time
	LastSeenAt          time.Time
	ConsecutiveFailures int
	LastError           string
	UpdatedAt           time.Time
}
