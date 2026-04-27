package defs

import (
	"time"
)

// RecordingPolicyMode is the recording activation mode of a policy.
type RecordingPolicyMode string

// Recording-policy modes per domain-model.md.
const (
	RecordingPolicyModeContinuous     RecordingPolicyMode = "continuous"
	RecordingPolicyModeMotion         RecordingPolicyMode = "motion"
	RecordingPolicyModeSchedule       RecordingPolicyMode = "schedule"
	RecordingPolicyModeEventTriggered RecordingPolicyMode = "event_triggered"
	RecordingPolicyModeOff            RecordingPolicyMode = "off"
)

// RecordingPolicyContainer is the on-disk container format for segments.
type RecordingPolicyContainer string

// Recording-policy container formats per domain-model.md.
const (
	RecordingPolicyContainerFMP4   RecordingPolicyContainer = "fmp4"
	RecordingPolicyContainerMPEGTS RecordingPolicyContainer = "mpegts"
)

// RecordingPolicyScheduleWindow is one window of a scheduled-mode policy.
type RecordingPolicyScheduleWindow struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// RecordingPolicySchedule is the schedule block of a scheduled-mode policy.
type RecordingPolicySchedule struct {
	Timezone string                          `json:"timezone"`
	Windows  []RecordingPolicyScheduleWindow `json:"windows"`
}

// RecordingPolicy is the canonical RecordingPolicy entity exposed at
// /v1/recording-policies per ADR 0009 §D2.
//
// The MS owns RecordingPolicy authoritatively; the recorder caches and
// enforces it. Multiple cameras share a policy by referencing its id.
type RecordingPolicy struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`

	Mode     RecordingPolicyMode      `json:"mode"`
	Schedule *RecordingPolicySchedule `json:"schedule,omitempty"`

	RetentionDuration  time.Duration `json:"retention_duration"`
	MinSegmentDuration time.Duration `json:"min_segment_duration"`
	MaxSegmentDuration time.Duration `json:"max_segment_duration"`

	Container RecordingPolicyContainer `json:"container"`

	PreEventBuffer  *time.Duration `json:"pre_event_buffer,omitempty"`
	PostEventBuffer *time.Duration `json:"post_event_buffer,omitempty"`

	Enabled bool `json:"enabled"`

	// Fields added by ADR 0009 §"Canonical model amendments → RecordingPolicy".
	PartDuration       time.Duration `json:"part_duration"`
	MaxPartSize        int64         `json:"max_part_size"`
	RecordPathTemplate *string       `json:"record_path_template,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
