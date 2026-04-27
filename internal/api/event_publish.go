// Package api: helpers for publishing canonical Events from API-layer
// and pipeline-side trigger points.
//
// API-layer producers (auth decisions and config/policy applies) live
// in handlers that hold *API, and use publishEvent /
// publishEventLocked; the locked variant is for callers that already
// hold a.mutex (Go's RWMutex isn't reentrant, so calling the unlocked
// helper under a held write lock deadlocks).
//
// Pipeline-side producers (camera state changes from
// internal/core/path.go's lifecycle hooks) cannot reach an *API
// receiver without breaking layering. They use the package-level
// PublishCameraOnline / PublishCameraOffline functions, which resolve
// their target via SetPipelineEventTarget — wired once at startup by
// core.go to share the same EventStore the /v1/events surface serves
// from.
//
// Per AGENTS.md §6, helpers used by media-pipeline producers live in
// internal/api (not in the pipeline packages); pipeline code calls
// them as a single-statement attachment alongside existing logger
// calls. The defs.EventInput construction logic stays in this file.
package api //nolint:revive

import (
	"sync"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// pipelineEventTarget holds the EventStore and tenant-id resolver used
// by pipeline-side publish helpers (PublishCameraOnline, etc.). Set
// once at startup by core.go via SetPipelineEventTarget so pipeline
// callers don't have to thread an EventStore reference through their
// own initialization.
//
// If unset, the pipeline helpers fall back to defaultEventStore() with
// an empty tenant id. That keeps tests usable without explicit setup.
var (
	pipelineMu       sync.RWMutex
	pipelineStore    *EventStore
	pipelineTenantFn func() string
)

// SetPipelineEventTarget configures where pipeline-emitted Events go
// and how to resolve the recorder's tenant id at publish time. Called
// once during recorder startup, after the API and recorder config are
// initialized. Safe to call again to reconfigure (e.g., on tenant
// rebinding); replaces both fields atomically.
func SetPipelineEventTarget(store *EventStore, tenantIDFn func() string) {
	pipelineMu.Lock()
	defer pipelineMu.Unlock()
	pipelineStore = store
	pipelineTenantFn = tenantIDFn
}

// pipelineTarget returns the configured store and tenant id, or sane
// defaults (defaultEventStore() and empty tenant id) when nothing has
// been wired yet. Used internally by the pipeline publish helpers.
func pipelineTarget() (*EventStore, string) {
	pipelineMu.RLock()
	defer pipelineMu.RUnlock()
	store := pipelineStore
	if store == nil {
		store = defaultEventStore()
	}
	tenantID := ""
	if pipelineTenantFn != nil {
		tenantID = pipelineTenantFn()
	}
	return store, tenantID
}

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

// PublishCameraOnline is the pipeline-side helper for camera.online
// Events. Pipeline code (path.go) calls this at the moment a path
// transitions to online; the helper resolves the EventStore and
// tenant id from the package-level pipeline target configured at
// startup, derives the canonical Camera UUID from the path-name, and
// publishes. Severity info per the domain-model.md examples.
//
// Recording-server and site ids stay empty per the shared convention
// in api_v1_health.go::recordingServerID — they land when the MS-
// pairing client surfaces a server-scoped UUID through conf.Conf.
func PublishCameraOnline(pathName string) {
	store, tenantID := pipelineTarget()
	if store == nil {
		return
	}
	store.Publish(defs.EventInput{
		Kind:        "camera.online",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraIDFromPathName(pathName),
		Message:     "camera came online",
		Attributes: map[string]string{
			"path_name": pathName,
		},
	}, "", tenantID, "")
}

// PublishCameraOffline is the pipeline-side helper for camera.offline
// Events. Severity is warning per domain-model.md: a camera going
// offline is a noteworthy operational state, not a normal info-level
// transition. Pipeline callers should guard against emit-when-never-
// online (e.g., check pa.source != nil before calling) so a path that
// has never come online doesn't emit a spurious offline event.
func PublishCameraOffline(pathName string) {
	store, tenantID := pipelineTarget()
	if store == nil {
		return
	}
	store.Publish(defs.EventInput{
		Kind:        "camera.offline",
		Severity:    defs.EventSeverityWarning,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraIDFromPathName(pathName),
		Message:     "camera went offline",
		Attributes: map[string]string{
			"path_name": pathName,
		},
	}, "", tenantID, "")
}

// PublishStorageVolumeFull is the recordcleaner-side helper for
// storage.volume_full Events. Severity is error per
// domain-model.md: a volume crossing the capacity threshold is an
// operational incident, not just a warning. Callers (recordcleaner's
// capacity probe) are responsible for once-per-state-change emission
// — this helper does not deduplicate. SubjectID is the canonical
// volume UUID derived from the absolute mount path; the wire shape
// matches /v1/storage-volumes' volume-id derivation so consumers
// linking events to volumes can correlate by id.
func PublishStorageVolumeFull(volumeID, mountPath string) {
	store, tenantID := pipelineTarget()
	if store == nil {
		return
	}
	store.Publish(defs.EventInput{
		Kind:        "storage.volume_full",
		Severity:    defs.EventSeverityError,
		SubjectKind: defs.EventSubjectKindVolume,
		SubjectID:   volumeID,
		Message:     "storage volume crossed capacity threshold",
		Attributes: map[string]string{
			"mount_path": mountPath,
		},
	}, "", tenantID, "")
}

// PublishStorageVolumeDegraded is the recordcleaner-side helper for
// storage.volume_degraded Events. Severity is warning: the volume is
// still serving but its status (degraded or read_only) suggests
// administrative attention. The reason argument is included as an
// attribute so consumers can distinguish the underlying signal
// (capacity headroom, statfs failure, read-only mount, etc.) without
// parsing the message string.
func PublishStorageVolumeDegraded(volumeID, mountPath, reason string) {
	store, tenantID := pipelineTarget()
	if store == nil {
		return
	}
	store.Publish(defs.EventInput{
		Kind:        "storage.volume_degraded",
		Severity:    defs.EventSeverityWarning,
		SubjectKind: defs.EventSubjectKindVolume,
		SubjectID:   volumeID,
		Message:     "storage volume entered a degraded state",
		Attributes: map[string]string{
			"mount_path": mountPath,
			"reason":     reason,
		},
	}, "", tenantID, "")
}

// DefaultEventStore exposes the package-wide singleton so callers
// that don't construct an *API can publish into the same store the
// /v1/events surface serves from. core.go uses this in
// SetPipelineEventTarget; tests use it via defaultEventStore() in the
// same package.
func DefaultEventStore() *EventStore {
	return defaultEventStore()
}
