// Package policysync — apply layer for slice 4-C RecordingPolicy
// canonical-authority migration per ADR 0017 D2.
//
// The apply layer is the seam between an MS-issued desired-state
// snapshot (delivered via the recorder-poll endpoint per
// recording-policy-canonical-push.md §4.2) and the recorder's local
// conf.RecordingPolicies store. The Poller (poll.go) calls Apply on
// each tick; the API package satisfies the Applier interface with
// mutex-guarded conf-mutation primitives that share state with the
// push-direction handlers.
//
// Apply rules per recording-policy-canonical-push.md §5 + ADR 0017
// D4:
//
//   - For each policy in the desired-state response: if it does not
//     exist locally, ADD it. If it exists with version < the MS-issued
//     version, UPDATE it. If it exists with version equal to the
//     MS-issued version, NO-OP. If it exists with version greater than
//     the MS-issued version, version regression (only possible after a
//     local break-glass per ADR 0017 D7) — the MS version still wins
//     and the apply emits a loud audit entry marking the override
//     revert.
//
//   - For each policy that exists locally but is NOT in the desired-
//     state response: DELETE it. The MS sends only live policies;
//     absence is the tombstone signal.
//
// All apply operations are idempotent. Re-running Apply on the same
// desired-state is a no-op.
package policysync

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// PolicyShape mirrors the MS canonical RecordingPolicy wire shape.
// Held here as a local struct so policysync doesn't need to import
// management/internal/policies — the wire shape is what's
// authoritative.
//
// The recorder's defs.RecordingPolicy carries the same field names
// with the same JSON tags; we hold a separate struct here so the
// apply layer can carry the version field without round-tripping
// through defs (which doesn't have it).
type PolicyShape struct {
	ID                 string          `json:"id"`
	TenantID           string          `json:"tenant_id"`
	RecordingServerID  string          `json:"recording_server_id,omitempty"`
	Name               string          `json:"name"`
	Mode               string          `json:"mode"`
	Schedule           json.RawMessage `json:"schedule,omitempty"`
	RetentionDuration  time.Duration   `json:"retention_duration"`
	MinSegmentDuration time.Duration   `json:"min_segment_duration"`
	MaxSegmentDuration time.Duration   `json:"max_segment_duration"`
	Container          string          `json:"container"`
	PreEventBuffer     *time.Duration  `json:"pre_event_buffer,omitempty"`
	PostEventBuffer    *time.Duration  `json:"post_event_buffer,omitempty"`
	Enabled            bool            `json:"enabled"`
	PartDuration       time.Duration   `json:"part_duration"`
	MaxPartSize        int64           `json:"max_part_size"`
	RecordPathTemplate *string         `json:"record_path_template,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// DesiredState is the recorder-side projection of the MS's poll
// response (recording-policy-canonical-push.md §4.2). Decoded from
// JSON by the Poller before being handed to Apply.
type DesiredState struct {
	RecordingServerID      string             `json:"recording_server_id"`
	VersionSet             int64              `json:"version_set"`
	AppliedVersionRecorded int64              `json:"applied_version_recorded"`
	RecordingPolicies      []DesiredStateItem `json:"recording_policies"`
}

// DesiredStateItem is one entry in the response's recording_policies
// array.
type DesiredStateItem struct {
	PolicyShape
	Version int64 `json:"version"`
}

// AppliedOutcome describes the per-policy apply result.
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
	PolicyID    string
	PolicyName  string
	Outcome     AppliedOutcome
	FromVersion int64
	ToVersion   int64
	Err         error
}

// AppliedReport summarizes one Apply call. Returned to the Poller for
// logging and metrics; tests assert on its contents.
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

// AuditEmitter is the thin seam policysync uses for emitting
// per-policy audit entries. The API package satisfies it via its
// existing audit-emit helpers.
type AuditEmitter interface {
	EmitPolicyConfigApplied(policyID, verb, source string, version int64, attrs map[string]string)
	EmitPolicyLocalOverrideReverted(policyID string, priorLocalVersion, restoredMSVersion int64)
}

// Applier applies an MS-issued desired-state snapshot to the
// recorder's local conf.RecordingPolicies store.
//
// Implementations must:
//   - Translate each DesiredStateItem.PolicyShape into the recorder's
//     internal conf.RecordingPolicyConfig format.
//   - Persist the new policy set via APIConfigSet so push-direction
//     and poll-direction apply share the same mutation seam.
//   - Emit recording_policy.config.applied audit entries via the
//     supplied AuditEmitter — actor_kind = service_account, source =
//     "poll", attributes per recording-policy-canonical-push.md §9.2.
//   - Be idempotent: re-applying the same desired-state is a no-op
//     (Outcome == OutcomeUnchanged).
type Applier interface {
	// CurrentPolicies returns the recorder's locally-cached policy
	// set keyed by canonical id. Each entry carries the
	// locally-applied version (zero before slice 4-C activation;
	// populated thereafter).
	CurrentPolicies() map[string]LocalPolicy

	// AddPolicy persists a new policy derived from item.
	AddPolicy(ctx context.Context, item DesiredStateItem) error

	// UpdatePolicy applies a full-replacement update for an existing
	// policy.
	UpdatePolicy(ctx context.Context, item DesiredStateItem) error

	// DeletePolicy removes a policy by canonical id.
	DeletePolicy(ctx context.Context, policyID string) error

	// EngageLockdownIfNeeded flips the recorder's
	// policy_canonical_source to `ms` when the apply has applied at
	// least one MS-issued change AND the lockdown isn't already
	// engaged. Idempotent.
	EngageLockdownIfNeeded() error
}

// LocalPolicy is the recorder-side projection of a cached policy.
// Populated by Applier.CurrentPolicies() so the apply diff can compute
// add/update/delete without leaking conf.RecordingPolicyConfig through
// the policysync package boundary.
type LocalPolicy struct {
	ID      string
	Name    string
	Version int64
}

// Apply diffs the desired-state snapshot against the Applier's current
// cache and applies the necessary mutations. Returns a per-policy
// report for logging.
//
// Apply is mutex-guarded by mu so push-direction and poll-direction
// applies cannot race; the caller (Poller) supplies the mutex via the
// constructor so push-direction handlers can use the same lock.
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
	current := app.CurrentPolicies()
	seen := make(map[string]struct{}, len(desired.RecordingPolicies))
	appliedAtLeastOne := false

	for _, item := range desired.RecordingPolicies {
		if item.ID == "" {
			continue
		}
		seen[item.ID] = struct{}{}

		local, exists := current[item.ID]
		switch {
		case !exists:
			if err := app.AddPolicy(ctx, item); err != nil {
				report.Items = append(report.Items, AppliedItem{
					PolicyID:   item.ID,
					PolicyName: item.Name,
					Outcome:    OutcomeFailed,
					ToVersion:  item.Version,
					Err:        err,
				})
				continue
			}
			emit.EmitPolicyConfigApplied(item.ID, "create", "poll", item.Version, map[string]string{
				"policy_id": item.ID,
				"verb":      "create",
				"source":    "poll",
			})
			report.Items = append(report.Items, AppliedItem{
				PolicyID:   item.ID,
				PolicyName: item.Name,
				Outcome:    OutcomeAdded,
				ToVersion:  item.Version,
			})
			appliedAtLeastOne = true

		case local.Version == item.Version:
			report.Items = append(report.Items, AppliedItem{
				PolicyID:    item.ID,
				PolicyName:  item.Name,
				Outcome:     OutcomeUnchanged,
				FromVersion: local.Version,
				ToVersion:   item.Version,
			})

		case local.Version < item.Version:
			if err := app.UpdatePolicy(ctx, item); err != nil {
				report.Items = append(report.Items, AppliedItem{
					PolicyID:    item.ID,
					PolicyName:  item.Name,
					Outcome:     OutcomeFailed,
					FromVersion: local.Version,
					ToVersion:   item.Version,
					Err:         err,
				})
				continue
			}
			emit.EmitPolicyConfigApplied(item.ID, "update", "poll", item.Version, map[string]string{
				"policy_id": item.ID,
				"verb":      "update",
				"source":    "poll",
			})
			report.Items = append(report.Items, AppliedItem{
				PolicyID:    item.ID,
				PolicyName:  item.Name,
				Outcome:     OutcomeUpdated,
				FromVersion: local.Version,
				ToVersion:   item.Version,
			})
			appliedAtLeastOne = true

		default: // local.Version > item.Version
			// Version regression: only possible after a local break-
			// glass mutation per ADR 0017 D7. The MS-canonical version
			// still wins; we apply the MS shape and emit a loud audit
			// entry marking the local-override revert.
			if err := app.UpdatePolicy(ctx, item); err != nil {
				report.Items = append(report.Items, AppliedItem{
					PolicyID:    item.ID,
					PolicyName:  item.Name,
					Outcome:     OutcomeFailed,
					FromVersion: local.Version,
					ToVersion:   item.Version,
					Err: fmt.Errorf("apply ms version %d over local %d: %w",
						item.Version, local.Version, err),
				})
				continue
			}
			emit.EmitPolicyLocalOverrideReverted(item.ID, local.Version, item.Version)
			emit.EmitPolicyConfigApplied(item.ID, "update", "poll", item.Version, map[string]string{
				"policy_id":           item.ID,
				"verb":                "update",
				"source":              "poll",
				"prior_local_version": fmt.Sprintf("%d", local.Version),
				"restored_ms_version": fmt.Sprintf("%d", item.Version),
			})
			report.Items = append(report.Items, AppliedItem{
				PolicyID:    item.ID,
				PolicyName:  item.Name,
				Outcome:     OutcomeVersionRegression,
				FromVersion: local.Version,
				ToVersion:   item.Version,
			})
			appliedAtLeastOne = true
		}
	}

	// Tombstone pass: any policy in the local cache that's missing
	// from the response is deleted. The MS sends only live policies
	// per recording-policy-canonical-push.md §4.2 + §11.
	for id, local := range current {
		if _, present := seen[id]; present {
			continue
		}
		if err := app.DeletePolicy(ctx, id); err != nil {
			report.Items = append(report.Items, AppliedItem{
				PolicyID:    id,
				PolicyName:  local.Name,
				Outcome:     OutcomeFailed,
				FromVersion: local.Version,
				Err:         err,
			})
			continue
		}
		emit.EmitPolicyConfigApplied(id, "delete", "poll", 0, map[string]string{
			"policy_id":     id,
			"verb":          "delete",
			"source":        "poll",
			"prior_version": fmt.Sprintf("%d", local.Version),
		})
		report.Items = append(report.Items, AppliedItem{
			PolicyID:    id,
			PolicyName:  local.Name,
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
