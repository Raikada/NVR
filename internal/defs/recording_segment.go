package defs

import (
	"time"
)

// RecordingSegmentContainer is the on-disk container format of a segment.
type RecordingSegmentContainer string

// Recording-segment container formats per domain-model.md.
const (
	RecordingSegmentContainerFMP4   RecordingSegmentContainer = "fmp4"
	RecordingSegmentContainerMPEGTS RecordingSegmentContainer = "mpegts"
)

// RecordingSegmentState is the lifecycle state of a RecordingSegment.
type RecordingSegmentState string

// Recording-segment lifecycle states per domain-model.md.
const (
	RecordingSegmentStateRecording RecordingSegmentState = "recording"
	RecordingSegmentStateSealed    RecordingSegmentState = "sealed"
	RecordingSegmentStateArchived  RecordingSegmentState = "archived"
	RecordingSegmentStateDeleted   RecordingSegmentState = "deleted"
)

// RecordingSegmentTrack describes one track on a RecordingSegment.
type RecordingSegmentTrack struct {
	Kind  StreamTrackKind `json:"kind"`
	Codec string          `json:"codec"`
}

// RecordingSegment is the canonical RecordingSegment entity, the
// storage-level unit of recorded footage (one file on disk, immutable
// once sealed).
//
// Per ADR 0009 §"Canonical model amendments → RecordingSegment" the
// `RecordingID` field links each segment to its parent Recording.
type RecordingSegment struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	SiteID            string `json:"site_id"`
	RecordingServerID string `json:"recording_server_id"`
	CameraID          string `json:"camera_id"`
	VolumeID          string `json:"volume_id"`
	PolicyID          string `json:"policy_id"`

	// RecordingID was added by ADR 0009; required.
	RecordingID string `json:"recording_id"`

	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Duration  time.Duration `json:"duration"`

	Container RecordingSegmentContainer `json:"container"`
	Path      string                    `json:"path"`
	SizeBytes int64                     `json:"size_bytes"`

	Tracks []RecordingSegmentTrack `json:"tracks,omitempty"`

	Checksum *string `json:"checksum,omitempty"`

	State RecordingSegmentState `json:"state"`

	CreatedAt time.Time `json:"created_at"`
}
