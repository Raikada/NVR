package defs

import (
	"time"
)

// ClipState is the lifecycle state of a Clip per domain-model.md.
type ClipState string

// Clip lifecycle states.
const (
	ClipStateRequested ClipState = "requested"
	ClipStatePreparing ClipState = "preparing"
	ClipStateReady     ClipState = "ready"
	ClipStateFailed    ClipState = "failed"
	ClipStateExpired   ClipState = "expired"
	ClipStateDeleted   ClipState = "deleted"
)

// ClipContainer is the container format of the stitched clip-export
// artifact. `mp4` is the canonical default for a stitched single-file
// export per domain-model.md.
type ClipContainer string

// Clip container formats.
const (
	ClipContainerMP4    ClipContainer = "mp4"
	ClipContainerFMP4   ClipContainer = "fmp4"
	ClipContainerMPEGTS ClipContainer = "mpegts"
)

// Clip is the canonical Clip entity per
// `platform/docs/domain-model.md`. A user-defined, persistent reference
// to a time range of recorded media. The recorder is authoritative for
// preparing the export artifact (stitching the source segments,
// writing the export file, computing checksums, serving the download)
// and for retaining the source segments while the clip is active.
//
// Pre-MS note: `RequestedBy` is the empty string in this build because
// there is no on-recorder User authoritative source. Once the
// Management Server lands, this field carries the canonical user id of
// the requester. See platform/docs/adr/0009 closure notes.
type Clip struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	SiteID            string `json:"site_id"`
	RecordingServerID string `json:"recording_server_id"`
	CameraID          string `json:"camera_id"`

	// EventID links an event-derived clip back to its event (SP4).
	// Empty for ad-hoc range clips.
	EventID string `json:"event_id,omitempty"`

	RequestedBy string `json:"requested_by"`

	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`

	RangeStartedAt time.Time     `json:"range_started_at"`
	RangeEndedAt   time.Time     `json:"range_ended_at"`
	Duration       time.Duration `json:"duration"`

	RequestedAt time.Time  `json:"requested_at"`
	PreparedAt  *time.Time `json:"prepared_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`

	State         ClipState `json:"state"`
	FailureReason *string   `json:"failure_reason,omitempty"`

	Container ClipContainer `json:"container"`
	SizeBytes *int64        `json:"size_bytes,omitempty"`
	Checksum  *string       `json:"checksum,omitempty"`

	SourceSegmentIDs []string `json:"source_segment_ids"`

	// ExportPath and DownloadTokenRef are recorder-internal per
	// domain-model.md — they MUST NOT appear in client-facing
	// responses. Marked `json:"-"` so an accidental ctx.JSON(clip)
	// from a handler still scrubs them.
	ExportPath       string `json:"-"`
	DownloadTokenRef string `json:"-"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
