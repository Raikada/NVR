package defs

import (
	"time"
)

// RecordingState is the lifecycle state of a Recording.
//
// Per ADR 0009: active → sealed; sealed → archived | deleted; (any) → failed.
type RecordingState string

// Recording lifecycle states.
const (
	RecordingStateActive   RecordingState = "active"
	RecordingStateSealed   RecordingState = "sealed"
	RecordingStateArchived RecordingState = "archived"
	RecordingStateFailed   RecordingState = "failed"
	RecordingStateDeleted  RecordingState = "deleted"
)

// Recording is the canonical Recording entity introduced inline by
// ADR 0009. A continuous span of recorded footage on one camera,
// composed of one or more RecordingSegments. The client/scrub primitive.
//
// The recorder owns Recording authoritatively; MS holds an index
// projection. Replaces the old MediaMTX two-field "Recording" stub.
type Recording struct {
	ID                string  `json:"id"`
	TenantID          string  `json:"tenant_id"`
	SiteID            string  `json:"site_id"`
	CameraID          string  `json:"camera_id"`
	RecordingServerID string  `json:"recording_server_id"`
	PolicyID          *string `json:"policy_id,omitempty"`

	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`

	State RecordingState `json:"state"`

	// FailureReason is required when State == failed; null otherwise.
	FailureReason *string `json:"failure_reason,omitempty"`

	Duration     time.Duration `json:"duration"`
	ByteSize     int64         `json:"byte_size"`
	SegmentCount int           `json:"segment_count"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Segments is populated on the GET /v1/recordings/{id} response per
	// ADR 0009 §D5. Omitted from list responses.
	Segments []RecordingSegment `json:"segments,omitempty"`
}
