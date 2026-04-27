// Package api: helpers for publishing canonical Events from API-layer
// trigger points.
//
// The /v1/events ring buffer (event_store.go) was added in Phase 2D
// with a "producers will be wired in Phase-2-followup" notice. This
// file is the followup wiring for the API-layer trigger points: auth
// decisions and config/policy applies. Pipeline-adjacent producers
// (camera state changes from internal/core/path_manager.go) call into
// the same EventStore singleton via a thin helper that lives here so
// the pipeline package never imports defs.EventInput construction
// logic — pipeline code only ever invokes Publish.
//
// Per AGENTS.md §6, helpers used by media-pipeline producers live in
// internal/api (not in the pipeline packages); pipeline code calls
// them as a single-statement attachment alongside existing logger
// calls.
package api //nolint:revive

import (
	"github.com/bluenviron/mediamtx/internal/defs"
)

// publishEvent is the thin wrapper around EventStore.Publish for
// callers that do NOT hold a.mutex (e.g., the auth middleware). It
// acquires the read lock through a.tenantID() and uses the package-
// wide default EventStore. recordingServerID and siteID are passed
// empty for now — neither is surfaced through conf.Conf yet (see
// api_v1_health.go's recordingServerID note).
//
// Use publishEventLocked from handlers that already hold a.mutex
// (write or read). Go's sync.RWMutex is not reentrant; calling
// publishEvent from under a held write lock deadlocks the moment the
// scheduler hands the goroutine off, since a.tenantID() tries to
// reacquire the read lock.
func (a *API) publishEvent(in defs.EventInput) {
	tenantID := ""
	if a != nil {
		tenantID = a.tenantID()
	}
	defaultEventStore().Publish(in, "", tenantID, "")
}

// publishEventLocked is publishEvent for callers that already hold
// a.mutex (write or read). Reads a.Conf.TenantID directly without
// re-acquiring the mutex, matching the pattern used elsewhere in
// internal/api/ for in-handler convenience reads.
func (a *API) publishEventLocked(in defs.EventInput) {
	tenantID := ""
	if a != nil && a.Conf != nil {
		tenantID = a.Conf.TenantID
	}
	defaultEventStore().Publish(in, "", tenantID, "")
}

// PublishCameraOnline is the API-side helper for camera.online events
// emitted by the path manager. Lives in internal/api so pipeline
// packages don't construct defs.EventInput themselves; the caller
// passes the path/camera id and any reason string.
//
// The function is deliberately unattached to a *API receiver so
// pipeline-side callers (which only have an *EventStore reference)
// can invoke it without hauling the API surface across the
// import boundary. Recording-server and site ids stay empty per the
// shared convention in api_v1_health.go::recordingServerID.
func PublishCameraOnline(store *EventStore, tenantID, cameraID, pathName string) {
	if store == nil {
		return
	}
	store.Publish(defs.EventInput{
		Kind:        "camera.online",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "camera came online",
		Attributes: map[string]string{
			"path_name": pathName,
		},
	}, "", tenantID, "")
}

// PublishCameraOffline is the API-side helper for camera.offline
// events emitted by the path manager. Severity is warning per
// domain-model.md: a camera going offline is a noteworthy operational
// state, not a normal info-level transition.
func PublishCameraOffline(store *EventStore, tenantID, cameraID, pathName string) {
	if store == nil {
		return
	}
	store.Publish(defs.EventInput{
		Kind:        "camera.offline",
		Severity:    defs.EventSeverityWarning,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "camera went offline",
		Attributes: map[string]string{
			"path_name": pathName,
		},
	}, "", tenantID, "")
}

// DefaultEventStore exposes the package-wide singleton to pipeline
// callers that don't have a per-API store reference. The orchestrator
// (core.go) pulls this once at startup and threads it into the path
// manager via the existing dependency-injection seam. Returning the
// same singleton both the API handlers and the pipeline producers
// publish into keeps /v1/events coherent across both producer paths.
func DefaultEventStore() *EventStore {
	return defaultEventStore()
}
