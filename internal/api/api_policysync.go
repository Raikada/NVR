// Package api: slice 4-C policysync.Applier + AuditEmitter
// implementations per ADR 0017 D2.
//
// Mirrors api_camerasync.go shape: the API surface owns conf
// mutation; policysync reaches in through these adapters so push-
// direction and poll-direction mutations share one lock and one
// audit chain.
//
// policysync.Apply takes the API's mutex via its sync.Locker
// parameter — the api hands it CameraApplyLock() (the same RWMutex
// write-locker reused for both entity classes; conf mutation is
// already serialized by it).
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/policysync"
)

// PolicyApplier returns a policysync.Applier backed by this API's
// conf.RecordingPolicies store. Methods assume a.mutex is held by
// the caller (policysync.Apply).
func (a *API) PolicyApplier() policysync.Applier {
	return &policyApplier{a: a}
}

// PolicyAuditEmitter returns a policysync.AuditEmitter backed by
// this API's audit chain.
func (a *API) PolicyAuditEmitter() policysync.AuditEmitter {
	return &policyAuditEmitter{a: a}
}

// policyApplier is the non-exported impl behind PolicyApplier(). All
// methods assume a.mutex is held by the caller (policysync.Apply).
type policyApplier struct {
	a *API
}

// CurrentPolicies projects the recorder's current RecordingPolicy
// table into the policysync-facing LocalPolicy shape. Unlocked read
// of a.Conf — caller holds a.mutex.
func (p *policyApplier) CurrentPolicies() map[string]policysync.LocalPolicy {
	out := map[string]policysync.LocalPolicy{}
	if p.a.Conf == nil || p.a.Conf.RecordingPolicies == nil {
		return out
	}
	for id, cfg := range p.a.Conf.RecordingPolicies {
		if cfg == nil {
			continue
		}
		// Recorder doesn't carry a per-policy version on its conf
		// (the wire shape gets versioned by the MS at slice-4-C
		// activation; the recorder's local-applied-version map
		// records what we've ACK'd). Mirror cameras.
		out[id] = policysync.LocalPolicy{
			ID:      id,
			Name:    cfg.Name,
			Version: p.a.policyAppliedVersionLocked(id),
		}
	}
	return out
}

// AddPolicy inserts a new policy derived from the desired-state item.
// Mirrors the conf-mutation sequence of onV1RecordingPoliciesPost
// without the HTTP layer.
func (p *policyApplier) AddPolicy(_ context.Context, item policysync.DesiredStateItem) error {
	a := p.a
	if a.Conf == nil {
		return errors.New("policysync apply: nil conf")
	}
	if item.ID == "" {
		return errors.New("policysync apply: empty policy id")
	}

	policy := desiredStateItemToDefsPolicy(item)
	policy.TenantID = a.Conf.TenantID
	now := time.Now().UTC()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now

	newConf := a.Conf.Clone()
	if newConf.RecordingPolicies == nil {
		newConf.RecordingPolicies = make(map[string]*conf.RecordingPolicyConfig)
	}
	if _, exists := newConf.RecordingPolicies[policy.ID]; exists {
		// Already present (defensive): treat as no-op.
		return nil
	}
	newConf.RecordingPolicies[policy.ID] = defs.RecordingPolicyToConfig(policy)
	if err := newConf.Validate(nil); err != nil {
		return fmt.Errorf("policysync apply: Validate: %w", err)
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "policy.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		SubjectID:   policy.ID,
		Message:     "recording policy created (poll-applied)",
		Attributes: map[string]string{
			"policy_id": policy.ID,
			"verb":      "create",
			"source":    "poll",
		},
	})
	a.policySetAppliedVersionLocked(policy.ID, item.Version)
	return nil
}

// UpdatePolicy applies a full-replacement update for an existing
// policy. Mirrors the PATCH path's conf-mutation sequence.
func (p *policyApplier) UpdatePolicy(_ context.Context, item policysync.DesiredStateItem) error {
	a := p.a
	if a.Conf == nil {
		return errors.New("policysync apply: nil conf")
	}
	if item.ID == "" {
		return errors.New("policysync apply: update without policy id")
	}
	existing := a.Conf.RecordingPolicies[item.ID]
	if existing == nil {
		return p.AddPolicy(context.Background(), item)
	}

	policy := desiredStateItemToDefsPolicy(item)
	policy.TenantID = a.Conf.TenantID
	policy.UpdatedAt = time.Now().UTC()
	// Preserve created_at from the existing entry — Validate's clone
	// path normalises zero-valued time fields anyway, but keeping the
	// original makes the audit trail honest.
	if policy.CreatedAt.IsZero() {
		// existing.Name is the only field on conf.RecordingPolicyConfig
		// we can pull a created_at from — but conf.RecordingPolicyConfig
		// doesn't carry timestamps. Use the policy's pre-existing
		// canonical view instead.
		if priorPolicy := defs.RecordingPolicyFromConfig(item.ID, existing); !priorPolicy.CreatedAt.IsZero() {
			policy.CreatedAt = priorPolicy.CreatedAt
		} else {
			policy.CreatedAt = time.Now().UTC()
		}
	}

	newConf := a.Conf.Clone()
	newConf.RecordingPolicies[policy.ID] = defs.RecordingPolicyToConfig(policy)

	// Re-apply the patched policy to every path that references it
	// (mirrors the PATCH handler's per-path stamp).
	for name, path := range newConf.Paths {
		if path == nil || path.RecordingPolicyID != policy.ID {
			continue
		}
		defs.ApplyPolicyToPath(path, policy)
		op, opErr := optionalPathFromConfPath(path)
		if opErr != nil {
			return fmt.Errorf("policysync apply: re-encode path %q: %w", name, opErr)
		}
		newConf.OptionalPaths[name] = op
	}

	if err := newConf.Validate(nil); err != nil {
		return fmt.Errorf("policysync apply: Validate: %w", err)
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "policy.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		SubjectID:   policy.ID,
		Message:     "recording policy updated (poll-applied)",
		Attributes: map[string]string{
			"policy_id": policy.ID,
			"verb":      "update",
			"source":    "poll",
		},
	})
	a.policySetAppliedVersionLocked(policy.ID, item.Version)
	return nil
}

// DeletePolicy removes a policy by id. Used by the tombstone pass.
func (p *policyApplier) DeletePolicy(_ context.Context, policyID string) error {
	a := p.a
	if a.Conf == nil {
		return errors.New("policysync apply: nil conf")
	}
	if _, exists := a.Conf.RecordingPolicies[policyID]; !exists {
		return nil
	}

	// Reject deletion if any path still references this policy. Same
	// rule the push-direction DELETE handler enforces. The MS-side
	// service ensures push ordering (D9) but the recorder still
	// guards against inconsistent deletes.
	for _, path := range a.Conf.Paths {
		if path != nil && path.RecordingPolicyID == policyID {
			// Cameras still reference this policy. Don't delete; let
			// the next poll re-evaluate after the MS reconciles.
			return fmt.Errorf("policysync apply: policy %s still referenced by camera %s", policyID, path.Name)
		}
	}

	newConf := a.Conf.Clone()
	delete(newConf.RecordingPolicies, policyID)
	if err := newConf.Validate(nil); err != nil {
		return fmt.Errorf("policysync apply: Validate: %w", err)
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "policy.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		SubjectID:   policyID,
		Message:     "recording policy deleted (poll-applied)",
		Attributes: map[string]string{
			"policy_id": policyID,
			"verb":      "delete",
			"source":    "poll",
		},
	})
	a.policyClearAppliedVersionLocked(policyID)
	return nil
}

// EngageLockdownIfNeeded flips the recorder's policy_canonical_source
// to ms after at least one MS-issued change has applied. Idempotent.
func (p *policyApplier) EngageLockdownIfNeeded() error {
	a := p.a
	if a.Identity == nil {
		return nil
	}
	if a.Identity.PolicyCanonicalSource() == "ms" {
		return nil
	}
	return a.Identity.SetPolicyCanonicalSource("ms")
}

// desiredStateItemToDefsPolicy translates the policysync wire shape
// into the recorder's internal defs.RecordingPolicy. Field mapping
// is 1:1 — both shapes mirror the canonical RecordingPolicy from
// domain-model.md.
func desiredStateItemToDefsPolicy(item policysync.DesiredStateItem) defs.RecordingPolicy {
	policy := defs.RecordingPolicy{
		ID:                 item.ID,
		TenantID:           item.TenantID,
		Name:               item.Name,
		Mode:               defs.RecordingPolicyMode(item.Mode),
		RetentionDuration:  item.RetentionDuration,
		MinSegmentDuration: item.MinSegmentDuration,
		MaxSegmentDuration: item.MaxSegmentDuration,
		Container:          defs.RecordingPolicyContainer(item.Container),
		PreEventBuffer:     item.PreEventBuffer,
		PostEventBuffer:    item.PostEventBuffer,
		Enabled:            item.Enabled,
		PartDuration:       item.PartDuration,
		MaxPartSize:        item.MaxPartSize,
		RecordPathTemplate: item.RecordPathTemplate,
		CreatedAt:          item.CreatedAt,
		UpdatedAt:          item.UpdatedAt,
	}
	// Schedule is opaque JSON in the wire shape; the recorder's
	// defs.RecordingPolicy parses a structured Schedule. For 4-C we
	// only need the byte-for-byte round-trip on the canonical
	// surface, but for the conf store we have to parse. The pushed
	// shape from the MS already contains the parsed schedule
	// (encoded as JSON); we let json.Unmarshal handle the bridge in
	// the rare case the MS sends a populated schedule. For 4-C v1
	// we leave this as a no-op when item.Schedule is empty (mode =
	// continuous policies, which is the common case).
	if len(item.Schedule) > 0 {
		// Best-effort: attempt to decode the schedule JSON into
		// defs.RecordingPolicySchedule. Failure here doesn't block
		// the apply — the rest of the policy still reconciles.
		var sched defs.RecordingPolicySchedule
		if err := json.Unmarshal(item.Schedule, &sched); err == nil {
			policy.Schedule = &sched
		}
	}
	return policy
}

// policyAppliedVersionLocked / policySetAppliedVersionLocked /
// policyClearAppliedVersionLocked mirror the camera-version
// bookkeeping. Caller holds a.mutex.
func (a *API) policyAppliedVersionLocked(policyID string) int64 {
	if a.policyAppliedVersions == nil {
		return 0
	}
	return a.policyAppliedVersions[policyID]
}

func (a *API) policySetAppliedVersionLocked(policyID string, version int64) {
	if policyID == "" {
		return
	}
	if a.policyAppliedVersions == nil {
		a.policyAppliedVersions = make(map[string]int64)
	}
	a.policyAppliedVersions[policyID] = version
}

func (a *API) policyClearAppliedVersionLocked(policyID string) {
	if a.policyAppliedVersions == nil {
		return
	}
	delete(a.policyAppliedVersions, policyID)
}

// policyAuditEmitter implements policysync.AuditEmitter by writing
// to the API's audit chain via emitAuditLocked.
type policyAuditEmitter struct {
	a *API
}

// EmitPolicyConfigApplied records a recording_policy.config.applied
// audit entry per recording-policy-canonical-push.md §9.2.
func (e *policyAuditEmitter) EmitPolicyConfigApplied(policyID, verb, source string, version int64, attrs map[string]string) {
	merged := map[string]string{
		"policy_id": policyID,
		"verb":      verb,
		"source":    source,
		"version":   fmt.Sprintf("%d", version),
	}
	for k, v := range attrs {
		merged[k] = v
	}
	e.a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		ActorID:      "ms-service-recording-policy-push",
		Action:       "recording_policy.config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_policy",
		ResourceID:   policyID,
		Attributes:   merged,
	})
}

// EmitPolicyLocalOverrideReverted records the loud
// recording_policy.local_override_reverted audit entry per
// recording-policy-canonical-push.md §9.2 + ADR 0017 D7. Caller
// holds the API's mutex.
func (e *policyAuditEmitter) EmitPolicyLocalOverrideReverted(policyID string, prior, restored int64) {
	e.a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindSystem,
		Action:       "recording_policy.local_override_reverted",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_policy",
		ResourceID:   policyID,
		Attributes: map[string]string{
			"policy_id":           policyID,
			"prior_local_version": fmt.Sprintf("%d", prior),
			"restored_ms_version": fmt.Sprintf("%d", restored),
			"severity":            string(defs.EventSeverityWarning),
		},
	})
}

