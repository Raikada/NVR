// Package conf: RecordingPolicyConfig — persistence shape for the
// canonical RecordingPolicy entity per ADR 0009 §D8.
//
// The canonical shape lives in internal/defs/recording_policy.go. This
// file mirrors it inside the conf package so mediamtx.yml can persist
// policies through the same Conf.Validate() / Parent.APIConfigSet
// machinery that already serializes conf.Path mutations.
//
// The defs↔conf import cycle (defs imports conf) means defs cannot
// declare conf-mirrored types. The two types stay in sync via the
// conversion helpers in internal/defs/recording_policy_translate.go.
//
// YAML tags use camelCase to match the rest of conf.Path's
// convention. Wire-surface tags (snake_case canonical) live on
// defs.RecordingPolicy.
package conf

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DefaultRecordingPolicyID is the deterministic UUID of the seeded
// "Default" RecordingPolicy that every recorder ships with. The UI
// relies on its presence; new cameras without an explicit
// recording_policy_id attach to it. Stable across restarts so the
// canonical-ID is a fixed handle the UI can reference.
const DefaultRecordingPolicyID = "00000000-0000-0000-0000-000000000001"

// NewDefaultRecordingPolicyConfig returns the canonical Default policy
// seeded at boot when conf.RecordingPolicies is empty (or missing the
// Default UUID). Settings: continuous mode, fmp4 container, 14d
// retention, 1m–10m segment range.
//
// CreatedAt / UpdatedAt are intentionally left as the zero time.Time.
// Two reasons: (1) deepClone() in conf.go cannot copy unexported
// time.Time fields and would zero them anyway on every Clone; (2) a
// zero timestamp is a clean sentinel for "the seeded Default has not
// been edited" — operator edits stamp wall-clock UpdatedAt via the
// /v1/recording-policies PATCH handler.
//
// Idempotent: callers test for presence by ID before invoking.
func NewDefaultRecordingPolicyConfig() *RecordingPolicyConfig {
	return &RecordingPolicyConfig{
		Name:               "Default",
		Mode:               "continuous",
		RetentionDuration:  Duration(14 * 24 * time.Hour),
		MinSegmentDuration: Duration(1 * time.Minute),
		MaxSegmentDuration: Duration(10 * time.Minute),
		Container:          "fmp4",
		Enabled:            true,
		PartDuration:       Duration(1 * time.Second),
		MaxPartSize:        50 * 1024 * 1024,
	}
}

// RecordingPolicyConfig is the persistence shape of one RecordingPolicy
// as it lives in mediamtx.yml. Fields mirror defs.RecordingPolicy
// exactly; YAML tags are camelCase per the recorder's existing
// convention.
type RecordingPolicyConfig struct {
	Name string `json:"name"`

	Mode     string                            `json:"mode"`
	Schedule *RecordingPolicyConfigSchedule    `json:"schedule,omitempty"`

	RetentionDuration  Duration `json:"retentionDuration"`
	MinSegmentDuration Duration `json:"minSegmentDuration"`
	MaxSegmentDuration Duration `json:"maxSegmentDuration"`

	Container string `json:"container"`

	PreEventBuffer  *Duration `json:"preEventBuffer,omitempty"`
	PostEventBuffer *Duration `json:"postEventBuffer,omitempty"`

	Enabled bool `json:"enabled"`

	PartDuration       Duration `json:"partDuration"`
	MaxPartSize        int64    `json:"maxPartSize"`
	RecordPathTemplate *string  `json:"recordPathTemplate,omitempty"`

	TenantID  string    `json:"tenantId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// RecordingPolicyConfigScheduleWindow mirrors defs.RecordingPolicyScheduleWindow.
type RecordingPolicyConfigScheduleWindow struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// RecordingPolicyConfigSchedule mirrors defs.RecordingPolicySchedule.
type RecordingPolicyConfigSchedule struct {
	Timezone string                                `json:"timezone"`
	Windows  []RecordingPolicyConfigScheduleWindow `json:"windows"`
}

// validate checks one RecordingPolicyConfig entry. Called by
// Conf.Validate() per-key as part of the recordingPolicies: top-level
// map walk. Catches the obvious shape errors (missing name, invalid
// mode, mode-schedule mismatch, sub-second segment durations).
func (rp *RecordingPolicyConfig) validate(id string) error {
	if id == "" {
		return fmt.Errorf("recording policy has empty id key")
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("recording policy id '%s' is not a valid UUID: %w", id, err)
	}
	if rp.Name == "" {
		return fmt.Errorf("recording policy '%s' has empty name", id)
	}
	switch rp.Mode {
	case "continuous", "motion", "schedule", "event_triggered", "off":
		// ok
	default:
		return fmt.Errorf("recording policy '%s' has invalid mode '%s'", id, rp.Mode)
	}
	if rp.Mode == "schedule" && rp.Schedule == nil {
		return fmt.Errorf("recording policy '%s' has mode=schedule but no schedule block", id)
	}
	switch rp.Container {
	case "", "fmp4", "mpegts":
		// "" is acceptable on synthesis; a missing container falls back
		// to fmp4 at the conversion boundary.
	default:
		return fmt.Errorf("recording policy '%s' has invalid container '%s'", id, rp.Container)
	}
	if rp.MinSegmentDuration > 0 && rp.MaxSegmentDuration > 0 &&
		rp.MinSegmentDuration > rp.MaxSegmentDuration {
		return fmt.Errorf(
			"recording policy '%s' has minSegmentDuration > maxSegmentDuration",
			id)
	}
	return nil
}
