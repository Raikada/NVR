// Package api: slice 4-B camerasync.Applier + AuditEmitter
// implementations per ADR 0016 D2.
//
// The recorder's API surface is the single owner of conf.Path mutation
// state (a.mutex serializes pushes and reloads). The poll-direction
// apply layer (internal/camerasync) reaches in via these adapters so
// push-direction and poll-direction mutations share one lock and one
// audit chain.
//
// camerasync.Apply takes the mutex via its sync.Locker parameter —
// the API hands it &a.cameraApplyLock, which wraps a.mutex's write
// half. While Apply holds the lock, every Applier method on this file
// can read a.Conf and call APIConfigSet without re-acquiring.
package api //nolint:revive

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/camerasync"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
)

// CameraApplyLock returns a sync.Locker that takes a.mutex's write
// lock. camerasync.Apply uses it to serialize against the
// push-direction handlers (which call a.mutex.Lock() directly). The
// returned Locker may be passed to camerasync.PollerOptions.Mu.
//
// One Locker instance per *API is fine — the underlying lock is the
// API's RWMutex, not new state. We keep it as a method rather than a
// per-API-construction value so test setups don't need the wiring
// step.
func (a *API) CameraApplyLock() sync.Locker {
	return rwMutexWriteLocker{rw: &a.mutex}
}

// rwMutexWriteLocker adapts a *sync.RWMutex to sync.Locker, taking
// the write lock on Lock(). Avoids requiring camerasync to know about
// the recorder's RWMutex shape.
type rwMutexWriteLocker struct {
	rw *sync.RWMutex
}

func (l rwMutexWriteLocker) Lock()   { l.rw.Lock() }
func (l rwMutexWriteLocker) Unlock() { l.rw.Unlock() }

// CameraApplier returns a camerasync.Applier backed by this API's
// conf.Path store. The returned Applier expects to be invoked under
// a.mutex (acquired by camerasync.Apply via the Locker above) — the
// methods perform unlocked reads of a.Conf accordingly.
func (a *API) CameraApplier() camerasync.Applier {
	return &cameraApplier{a: a}
}

// CameraAuditEmitter returns a camerasync.AuditEmitter backed by this
// API's audit chain. Mirrors the push-direction emit pattern in
// camera_lockdown_gate.go + audit_emit.go.
func (a *API) CameraAuditEmitter() camerasync.AuditEmitter {
	return &cameraAuditEmitter{a: a}
}

// cameraApplier is the non-exported impl behind CameraApplier(). All
// methods assume a.mutex is held by the caller (camerasync.Apply).
type cameraApplier struct {
	a *API
}

// CurrentCameras projects the recorder's current path table into the
// camerasync-facing LocalCamera shape. Unlocked read of a.Conf — caller
// holds a.mutex.
func (c *cameraApplier) CurrentCameras() map[string]camerasync.LocalCamera {
	out := map[string]camerasync.LocalCamera{}
	if c.a.Conf == nil {
		return out
	}
	for name, p := range c.a.Conf.Paths {
		if p == nil {
			continue
		}
		id := cameraIDFromPathName(name)
		if id == "" {
			continue
		}
		out[id] = camerasync.LocalCamera{
			ID:      id,
			Name:    name,
			Version: c.a.cameraAppliedVersionLocked(id),
		}
	}
	return out
}

// AddCamera inserts a new camera derived from the desired-state item.
// Mirrors the path-construction sequence in onV1CamerasPost without
// the HTTP layer.
func (c *cameraApplier) AddCamera(_ context.Context, item camerasync.DesiredStateItem) error {
	a := c.a
	if a.Conf == nil {
		return errors.New("camerasync apply: nil conf")
	}
	if item.Name == "" {
		return errors.New("camerasync apply: empty camera name")
	}

	cam := item.Camera
	cam.TenantID = a.Conf.TenantID
	now := time.Now().UTC()
	cam.CreatedAt = now
	cam.UpdatedAt = now
	cam.Runtime = nil
	// Recorder-side id space matches the MS shape (deterministic UUIDv5
	// off the path-name); preserve whatever the MS sent. If the MS
	// supplied a different id space the cameraIDFromPathName lookup
	// would miss on subsequent polls — for slice 4-B the MS uses the
	// same derivation per management/internal/cameras/cameraid.go.
	if cam.ID == "" {
		cam.ID = cameraIDFromPathName(cam.Name)
	}

	// Resolve recording-policy linkage. The MS may send a pointer or
	// nil; default to the seeded Default policy when absent (matching
	// the push-direction handler's behavior).
	policyID := conf.DefaultRecordingPolicyID
	if cam.RecordingPolicyID != nil && *cam.RecordingPolicyID != "" {
		policyID = *cam.RecordingPolicyID
	}
	policyCfg, ok := a.Conf.RecordingPolicies[policyID]
	if !ok || policyCfg == nil {
		return fmt.Errorf("camerasync apply: unknown recording_policy_id %q", policyID)
	}
	cam.RecordingPolicyID = &policyID

	newConf := a.Conf.Clone()
	if _, exists := newConf.OptionalPaths[cam.Name]; exists {
		// The MS expects an additive create; if the recorder already
		// has the path, treat as a no-op rather than a hard error.
		// (UpdateCamera would be the right call but the diff already
		// chose AddCamera based on cache miss.)
		return nil
	}

	p, err := defs.PathFromCamera(cam)
	if err != nil {
		return fmt.Errorf("camerasync apply: PathFromCamera: %w", err)
	}
	defs.ApplyPolicyToPath(p, defs.RecordingPolicyFromConfig(policyID, policyCfg))
	p.RecordingPolicyID = policyID

	op, err := optionalPathFromConfPath(p)
	if err != nil {
		return fmt.Errorf("camerasync apply: OptionalPath: %w", err)
	}
	if err := newConf.AddPath(cam.Name, op); err != nil {
		return fmt.Errorf("camerasync apply: AddPath: %w", err)
	}
	if err := newConf.Validate(nil); err != nil {
		return fmt.Errorf("camerasync apply: Validate: %w", err)
	}
	if storedPath, ok := newConf.Paths[cam.Name]; ok {
		storedPath.ID = cam.ID
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cam.ID,
		Message:     "camera created (poll-applied)",
		Attributes: map[string]string{
			"camera_id": cam.ID,
			"verb":      "create",
			"source":    "poll",
		},
	})
	a.cameraSetAppliedVersionLocked(cam.ID, item.Version)
	return nil
}

// UpdateCamera applies a full-camera update by replacing the path.
// Equivalent to the push-direction PUT semantics rather than PATCH:
// the desired-state response is authoritative, every field rides through.
func (c *cameraApplier) UpdateCamera(_ context.Context, item camerasync.DesiredStateItem) error {
	a := c.a
	if a.Conf == nil {
		return errors.New("camerasync apply: nil conf")
	}

	cam := item.Camera
	if cam.ID == "" {
		return errors.New("camerasync apply: update without camera id")
	}

	name, ok := pathNameFromCameraID(a.Conf.Paths, cam.ID)
	if !ok {
		// Cache and conf disagreed about whether the camera was
		// present. Promote to add.
		return c.AddCamera(context.Background(), item)
	}
	existingPath := a.Conf.Paths[name]
	if existingPath == nil {
		return c.AddCamera(context.Background(), item)
	}

	cam.TenantID = a.Conf.TenantID
	cam.Name = name
	cam.UpdatedAt = time.Now().UTC()
	cam.Runtime = nil

	// Resolve recording-policy linkage.
	resolvedPolicyID := existingPath.RecordingPolicyID
	if cam.RecordingPolicyID != nil && *cam.RecordingPolicyID != "" {
		resolvedPolicyID = *cam.RecordingPolicyID
	}
	if resolvedPolicyID == "" {
		resolvedPolicyID = conf.DefaultRecordingPolicyID
	}
	policyCfg, pok := a.Conf.RecordingPolicies[resolvedPolicyID]
	if !pok || policyCfg == nil {
		return fmt.Errorf("camerasync apply: unknown recording_policy_id %q", resolvedPolicyID)
	}
	cam.RecordingPolicyID = &resolvedPolicyID

	newConf := a.Conf.Clone()
	newPath, err := defs.PathFromCamera(cam)
	if err != nil {
		return fmt.Errorf("camerasync apply: PathFromCamera: %w", err)
	}
	defs.ApplyPolicyToPath(newPath, defs.RecordingPolicyFromConfig(resolvedPolicyID, policyCfg))
	newPath.RecordingPolicyID = resolvedPolicyID

	op, err := optionalPathFromConfPath(newPath)
	if err != nil {
		return fmt.Errorf("camerasync apply: OptionalPath: %w", err)
	}
	if err := newConf.ReplacePath(name, op); err != nil {
		return fmt.Errorf("camerasync apply: ReplacePath: %w", err)
	}
	if err := newConf.Validate(nil); err != nil {
		return fmt.Errorf("camerasync apply: Validate: %w", err)
	}
	if storedPath, ok := newConf.Paths[name]; ok {
		storedPath.ID = cam.ID
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cam.ID,
		Message:     "camera updated (poll-applied)",
		Attributes: map[string]string{
			"camera_id": cam.ID,
			"verb":      "update",
			"source":    "poll",
		},
	})
	a.cameraSetAppliedVersionLocked(cam.ID, item.Version)
	return nil
}

// DeleteCamera removes a camera by id. Used by the tombstone pass.
func (c *cameraApplier) DeleteCamera(_ context.Context, cameraID string) error {
	a := c.a
	if a.Conf == nil {
		return errors.New("camerasync apply: nil conf")
	}
	name, ok := pathNameFromCameraID(a.Conf.Paths, cameraID)
	if !ok {
		// Already gone.
		return nil
	}

	newConf := a.Conf.Clone()
	if err := newConf.RemovePath(name); err != nil {
		return fmt.Errorf("camerasync apply: RemovePath: %w", err)
	}
	if err := newConf.Validate(nil); err != nil {
		return fmt.Errorf("camerasync apply: Validate: %w", err)
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "camera deleted (poll-applied)",
		Attributes: map[string]string{
			"camera_id": cameraID,
			"verb":      "delete",
			"source":    "poll",
		},
	})
	a.cameraClearAppliedVersionLocked(cameraID)
	return nil
}

// EngageLockdownIfNeeded flips the recorder's canonical_source to ms
// after at least one MS-issued change has applied. Idempotent.
// Mirrors engageLockdownIfMSSourced (camera_lockdown_gate.go) without
// requiring a per-request *gin.Context.
func (c *cameraApplier) EngageLockdownIfNeeded() error {
	a := c.a
	if a.Identity == nil {
		return nil
	}
	if err := a.Identity.SetCanonicalSource(
		a.Identity.CanonicalSource(),
	); err != nil {
		// Reading current to no-op-write is just a safety net; the
		// real flip happens via camerasync.EngageLockdown.
		_ = err
	}
	// Use the package-level helper so push-direction and poll-direction
	// engage paths share one mechanism.
	return engageCameraLockdown(a)
}

// engageCameraLockdown flips the identity's canonical_source to ms.
// Defined as a package function so the per-request handler in
// camera_lockdown_gate.go can call it too without going through the
// camerasync.Applier interface.
func engageCameraLockdown(a *API) error {
	if a.Identity == nil {
		return nil
	}
	if a.Identity.CanonicalSource() == "ms" {
		return nil
	}
	return a.Identity.SetCanonicalSource("ms")
}

// cameraAppliedVersionLocked returns the MS-issued version most
// recently applied for cameraID, or zero if none. Caller must hold
// a.mutex (read or write).
func (a *API) cameraAppliedVersionLocked(cameraID string) int64 {
	if a.cameraAppliedVersions == nil {
		return 0
	}
	return a.cameraAppliedVersions[cameraID]
}

// cameraSetAppliedVersionLocked records the MS-issued version applied
// for cameraID. Caller must hold a.mutex (write).
func (a *API) cameraSetAppliedVersionLocked(cameraID string, version int64) {
	if cameraID == "" {
		return
	}
	if a.cameraAppliedVersions == nil {
		a.cameraAppliedVersions = make(map[string]int64)
	}
	a.cameraAppliedVersions[cameraID] = version
}

// cameraClearAppliedVersionLocked drops the version record for a
// deleted camera. Caller must hold a.mutex (write).
func (a *API) cameraClearAppliedVersionLocked(cameraID string) {
	if a.cameraAppliedVersions == nil {
		return
	}
	delete(a.cameraAppliedVersions, cameraID)
}

// cameraAuditEmitter implements camerasync.AuditEmitter by writing to
// the API's audit chain via emitAuditLocked.
type cameraAuditEmitter struct {
	a *API
}

// EmitCameraConfigApplied records a camera.config.applied audit entry
// per camera-canonical-push.md §9.2. Caller (camerasync.Apply) holds
// the API's mutex.
func (e *cameraAuditEmitter) EmitCameraConfigApplied(cameraID, verb, source string, version int64, attrs map[string]string) {
	merged := map[string]string{
		"camera_id": cameraID,
		"verb":      verb,
		"source":    source,
		"version":   fmt.Sprintf("%d", version),
	}
	for k, v := range attrs {
		merged[k] = v
	}
	e.a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		ActorID:      "ms-service-camera-push",
		Action:       "camera.config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   cameraID,
		Attributes:   merged,
	})
}

// EmitCameraLocalOverrideReverted records the loud
// camera.local_override_reverted audit entry per camera-canonical-
// push.md §9.2 + ADR 0016 D7. Caller holds the API's mutex.
func (e *cameraAuditEmitter) EmitCameraLocalOverrideReverted(cameraID string, prior, restored int64) {
	e.a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindSystem,
		Action:       "camera.local_override_reverted",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   cameraID,
		Attributes: map[string]string{
			"camera_id":             cameraID,
			"prior_local_version":   fmt.Sprintf("%d", prior),
			"restored_ms_version":   fmt.Sprintf("%d", restored),
			"severity":              string(defs.EventSeverityWarning),
		},
	})
}
