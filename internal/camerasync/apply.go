// Package camerasync — apply layer for slice 4-B Camera canonical
// authority migration per ADR 0016 D2.
//
// The apply layer is the seam between an MS-issued desired-state
// snapshot (delivered via the recorder-poll endpoint per
// camera-canonical-push.md §4.2) and the recorder's local conf.Path
// store. The Poller (poll.go) calls Apply on each tick; the API package
// satisfies the Applier interface with mutex-guarded conf-mutation
// primitives that share state with the push-direction handlers.
//
// Apply rules per camera-canonical-push.md §5 + ADR 0016 D4:
//
//   - For each camera in the desired-state response: if it does not
//     exist locally, ADD it. If it exists with version < the MS-issued
//     version, UPDATE it. If it exists with version equal to the
//     MS-issued version, NO-OP (idempotent). If it exists with version
//     greater than the MS-issued version, this is a version regression
//     (only possible after a local break-glass per ADR 0016 D7) — the
//     MS version still wins, but the apply emits a loud audit entry
//     marking the override revert.
//
//   - For each camera that exists locally but is NOT in the desired-
//     state response: DELETE it. The MS sends only live cameras (per
//     contract §4.2 + §11) — the absence of a camera-id is the
//     tombstone signal.
//
// All apply operations are idempotent. Re-running Apply on the same
// desired-state is a no-op.
package camerasync

import (
	"context"
	"fmt"
	"sync"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// DesiredState is the recorder-side projection of the MS's poll
// response (camera-canonical-push.md §4.2). Decoded from JSON by the
// Poller before being handed to Apply.
type DesiredState struct {
	RecordingServerID      string             `json:"recording_server_id"`
	VersionSet             int64              `json:"version_set"`
	AppliedVersionRecorded int64              `json:"applied_version_recorded"`
	Cameras                []DesiredStateItem `json:"cameras"`
}

// DesiredStateItem is one entry in the response's cameras array. The
// inner Camera carries the canonical wire shape (snake_case, all
// recorder-facing fields), and Version is the MS's monotonic counter
// per ADR 0016 D4. The recorder's existing /v1/cameras decoder accepts
// the inner Camera as-is.
//
// The wire shape decoded here is JSON-equivalent to a
// management/internal/cameras.Camera record: defs.Camera fields plus a
// `version`. We mirror the MS shape here rather than embedding
// defs.Camera so the decode side can ignore MS-only fields like
// `sync_state` without polluting the canonical recorder type.
type DesiredStateItem struct {
	defs.Camera
	Version int64 `json:"version"`
}

// AppliedOutcome describes the per-camera apply result.
type AppliedOutcome string

// AppliedOutcome values.
const (
	OutcomeAdded             AppliedOutcome = "added"
	OutcomeUpdated           AppliedOutcome = "updated"
	OutcomeUnchanged         AppliedOutcome = "unchanged"
	OutcomeDeleted           AppliedOutcome = "deleted"
	OutcomeVersionRegression AppliedOutcome = "version_regression"
	OutcomeFailed            AppliedOutcome = "failed"
)

// AppliedItem is one row in an AppliedReport.
type AppliedItem struct {
	CameraID     string
	CameraName   string
	Outcome      AppliedOutcome
	FromVersion  int64
	ToVersion    int64
	Err          error
}

// AppliedReport summarizes one Apply call. Returned to the Poller
// for logging and metrics; tests assert on its contents.
type AppliedReport struct {
	Items []AppliedItem
}

// Counts returns a small breakdown for log emission. Stable iteration
// order means the log line is reproducible across runs.
func (r AppliedReport) Counts() map[AppliedOutcome]int {
	out := make(map[AppliedOutcome]int)
	for _, it := range r.Items {
		out[it.Outcome]++
	}
	return out
}

// AuditEmitter is the thin seam camerasync uses for emitting
// per-camera audit entries. The API package satisfies it via its
// existing audit-emit helpers.
type AuditEmitter interface {
	EmitCameraConfigApplied(cameraID, verb, source string, version int64, attrs map[string]string)
	EmitCameraLocalOverrideReverted(cameraID string, priorLocalVersion, restoredMSVersion int64)
}

// Applier applies an MS-issued desired-state snapshot to the
// recorder's local conf.Path store. Implementations are mutex-guarded
// internally; the camerasync package treats Apply as a single-shot
// idempotent call.
//
// Implementations must:
//   - Translate each DesiredStateItem.Camera into the recorder's
//     internal conf.Path format (via defs.PathFromCamera +
//     ApplyPolicyToPath).
//   - Persist the new path set via the same conf.AddPath /
//     ReplacePath / RemovePath primitives the push-direction handlers
//     use, so push-direction and poll-direction apply share the same
//     mutation seam.
//   - Emit camera.config.applied audit entries via the supplied
//     AuditEmitter — actor_kind = service_account, source = "poll",
//     attributes per camera-canonical-push.md §9.2.
//   - Be idempotent: re-applying the same desired-state is a no-op
//     (Outcome == OutcomeUnchanged).
type Applier interface {
	// CurrentCameras returns the recorder's locally-cached camera set
	// keyed by canonical id. Each entry carries the locally-applied
	// version (zero before slice 4-B activation; populated thereafter).
	CurrentCameras() map[string]LocalCamera

	// AddCamera persists a new camera derived from item. Returns the
	// applied version (== item.Version on success).
	AddCamera(ctx context.Context, item DesiredStateItem) error

	// UpdateCamera applies the patch shape for an existing camera.
	UpdateCamera(ctx context.Context, item DesiredStateItem) error

	// DeleteCamera removes a camera by canonical id.
	DeleteCamera(ctx context.Context, cameraID string) error

	// EngageLockdownIfNeeded flips the recorder's canonical_source to
	// `ms` when the apply has applied at least one MS-issued change
	// AND the lockdown isn't already engaged. Idempotent.
	EngageLockdownIfNeeded() error
}

// LocalCamera is the recorder-side projection of a cached camera.
// Populated by Applier.CurrentCameras() so the apply diff can compute
// add/update/delete without leaking conf.Path through the camerasync
// package boundary.
type LocalCamera struct {
	ID      string
	Name    string
	Version int64
}

// Apply diffs the desired-state snapshot against the Applier's current
// cache and applies the necessary mutations. Returns a per-camera
// report for logging.
//
// Apply is mutex-guarded by mu so push-direction and poll-direction
// applies cannot race; the caller (Poller) supplies the mutex via the
// constructor so push-direction handlers can use the same lock. The
// caller passes a sync.Locker — usually a *sync.Mutex from a fake
// applier or a *sync.RWMutex used as a write-lock from the recorder's
// API surface.
func Apply(
	ctx context.Context,
	app Applier,
	emit AuditEmitter,
	mu sync.Locker,
	desired DesiredState,
) AppliedReport {
	mu.Lock()
	defer mu.Unlock()

	report := AppliedReport{}
	current := app.CurrentCameras()
	seen := make(map[string]struct{}, len(desired.Cameras))
	appliedAtLeastOne := false

	for _, item := range desired.Cameras {
		if item.ID == "" {
			continue
		}
		seen[item.ID] = struct{}{}

		local, exists := current[item.ID]
		switch {
		case !exists:
			if err := app.AddCamera(ctx, item); err != nil {
				report.Items = append(report.Items, AppliedItem{
					CameraID:   item.ID,
					CameraName: item.Name,
					Outcome:    OutcomeFailed,
					ToVersion:  item.Version,
					Err:        err,
				})
				continue
			}
			emit.EmitCameraConfigApplied(item.ID, "create", "poll", item.Version, map[string]string{
				"camera_id": item.ID,
				"verb":      "create",
				"source":    "poll",
			})
			report.Items = append(report.Items, AppliedItem{
				CameraID:   item.ID,
				CameraName: item.Name,
				Outcome:    OutcomeAdded,
				ToVersion:  item.Version,
			})
			appliedAtLeastOne = true

		case local.Version == item.Version:
			report.Items = append(report.Items, AppliedItem{
				CameraID:    item.ID,
				CameraName:  item.Name,
				Outcome:     OutcomeUnchanged,
				FromVersion: local.Version,
				ToVersion:   item.Version,
			})

		case local.Version < item.Version:
			if err := app.UpdateCamera(ctx, item); err != nil {
				report.Items = append(report.Items, AppliedItem{
					CameraID:    item.ID,
					CameraName:  item.Name,
					Outcome:     OutcomeFailed,
					FromVersion: local.Version,
					ToVersion:   item.Version,
					Err:         err,
				})
				continue
			}
			emit.EmitCameraConfigApplied(item.ID, "update", "poll", item.Version, map[string]string{
				"camera_id": item.ID,
				"verb":      "update",
				"source":    "poll",
			})
			report.Items = append(report.Items, AppliedItem{
				CameraID:    item.ID,
				CameraName:  item.Name,
				Outcome:     OutcomeUpdated,
				FromVersion: local.Version,
				ToVersion:   item.Version,
			})
			appliedAtLeastOne = true

		default: // local.Version > item.Version
			// Version regression: only possible after a local break-
			// glass mutation per ADR 0016 D7. The MS-canonical version
			// still wins; we apply the MS shape and emit a loud audit
			// entry marking the local-override revert.
			if err := app.UpdateCamera(ctx, item); err != nil {
				report.Items = append(report.Items, AppliedItem{
					CameraID:    item.ID,
					CameraName:  item.Name,
					Outcome:     OutcomeFailed,
					FromVersion: local.Version,
					ToVersion:   item.Version,
					Err:         fmt.Errorf("apply ms version %d over local %d: %w",
						item.Version, local.Version, err),
				})
				continue
			}
			emit.EmitCameraLocalOverrideReverted(item.ID, local.Version, item.Version)
			emit.EmitCameraConfigApplied(item.ID, "update", "poll", item.Version, map[string]string{
				"camera_id":           item.ID,
				"verb":                "update",
				"source":              "poll",
				"prior_local_version": fmt.Sprintf("%d", local.Version),
				"restored_ms_version": fmt.Sprintf("%d", item.Version),
			})
			report.Items = append(report.Items, AppliedItem{
				CameraID:    item.ID,
				CameraName:  item.Name,
				Outcome:     OutcomeVersionRegression,
				FromVersion: local.Version,
				ToVersion:   item.Version,
			})
			appliedAtLeastOne = true
		}
	}

	// Tombstone pass: any camera in the local cache that's missing
	// from the response is deleted. The MS sends only live cameras
	// per camera-canonical-push.md §11.
	for id, local := range current {
		if _, present := seen[id]; present {
			continue
		}
		if err := app.DeleteCamera(ctx, id); err != nil {
			report.Items = append(report.Items, AppliedItem{
				CameraID:    id,
				CameraName:  local.Name,
				Outcome:     OutcomeFailed,
				FromVersion: local.Version,
				Err:         err,
			})
			continue
		}
		emit.EmitCameraConfigApplied(id, "delete", "poll", 0, map[string]string{
			"camera_id":     id,
			"verb":          "delete",
			"source":        "poll",
			"prior_version": fmt.Sprintf("%d", local.Version),
		})
		report.Items = append(report.Items, AppliedItem{
			CameraID:    id,
			CameraName:  local.Name,
			Outcome:     OutcomeDeleted,
			FromVersion: local.Version,
		})
		appliedAtLeastOne = true
	}

	if appliedAtLeastOne {
		_ = app.EngageLockdownIfNeeded()
	}
	return report
}
