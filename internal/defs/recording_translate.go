package defs

import (
	"time"
)

// RecordingFromSegments synthesizes a parent Recording row from a
// contiguous run of segments belonging to one logical recording.
//
// Used at first-startup migration (per ADR 0009 D8 closure): the recorder
// scans existing segments per camera, groups them into contiguous runs,
// and synthesizes one Recording per run. The caller is responsible for
// supplying segments in time order and limiting the slice to a single
// recording's worth.
//
// Mapping notes:
//   - tenant_id, site_id, camera_id, recording_server_id are taken from the
//     first segment (segments belonging to one recording must agree).
//   - started_at = first segment's started_at; ended_at = last segment's
//     ended_at.
//   - duration / byte_size sum across segments; segment_count = len(segments).
//   - state defaults to "sealed" — synthesized Recordings represent
//     historical, no-longer-active runs by definition. Callers that
//     synthesize a Recording for an in-progress segment set state =
//     "active" themselves after this call.
//   - policy_id is taken from the first segment if present; nil otherwise.
func RecordingFromSegments(segments []RecordingSegment, recordingID string) Recording {
	if len(segments) == 0 {
		return Recording{
			ID:    recordingID,
			State: RecordingStateSealed,
		}
	}
	first := segments[0]
	last := segments[len(segments)-1]

	var totalDuration time.Duration
	var totalBytes int64
	for _, seg := range segments {
		totalDuration += seg.Duration
		totalBytes += seg.SizeBytes
	}

	now := time.Now().UTC()
	endedAt := last.EndedAt
	out := Recording{
		ID:                recordingID,
		TenantID:          first.TenantID,
		SiteID:            first.SiteID,
		CameraID:          first.CameraID,
		RecordingServerID: first.RecordingServerID,
		StartedAt:         first.StartedAt,
		EndedAt:           &endedAt,
		State:             RecordingStateSealed,
		Duration:          totalDuration,
		ByteSize:          totalBytes,
		SegmentCount:      len(segments),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if first.PolicyID != "" {
		pid := first.PolicyID
		out.PolicyID = &pid
	}
	return out
}

// BackfillRecordingIDOnSegment is a trivial setter; included for symmetry
// with the migration-flow helpers and so callers don't need to know the
// canonical field name. Used during first-startup migration to stamp the
// synthesized recording_id onto each segment in the run.
func BackfillRecordingIDOnSegment(seg *RecordingSegment, recordingID string) {
	if seg == nil {
		return
	}
	seg.RecordingID = recordingID
}

// RecordstoreSegmentInput is the minimal shape Phase 1B can produce a
// canonical RecordingSegment from. The recorder's existing recordstore
// subsystem produces only (path, start) per the D8 stub — Phase 2C is
// expected to thread richer info through; this struct names exactly what
// the translator wants when that wiring is done.
//
// Fields with zero values are surfaced as zero on the canonical side; the
// migration is best-effort and Phase 2C may revisit.
type RecordstoreSegmentInput struct {
	// Path is the on-disk file path. Required.
	Path string

	// StartedAt is the segment's footage start time. Required.
	StartedAt time.Time

	// EndedAt is the segment's footage end time. May be zero if the
	// recordstore can't determine it (segment is still active or the
	// file's metadata is absent).
	EndedAt time.Time

	// SizeBytes is the on-disk size. May be zero if not known.
	SizeBytes int64

	// Container indicates fmp4 / mpegts. May be empty if not known;
	// caller defaults to fmp4.
	Container RecordingSegmentContainer

	// State indicates the lifecycle state. May be empty; caller defaults
	// to "sealed" for already-on-disk segments.
	State RecordingSegmentState

	// Optional checksum if the recordstore computed one.
	Checksum *string

	// Tracks discovered by the recordstore. Optional.
	Tracks []RecordingSegmentTrack
}

// SegmentFromRecordstoreFile produces a canonical RecordingSegment from
// a recordstore-supplied input. Fields the recordstore can't supply (or
// that callers carry separately, like ids and tenancy) come in as named
// arguments.
//
// TODO Phase 2C: the recordstore subsystem today exposes only (path,
// start) per canonical-divergence D8. Either it gains a richer return
// shape (in which case RecordstoreSegmentInput grows to match) or the
// migration accepts the gaps and fills them in elsewhere (e.g.,
// EndedAt = next segment's StartedAt by convention). This translator
// is intentionally conservative; do not modify the recordstore subsystem
// from this layer per AGENTS.md rule 6.
func SegmentFromRecordstoreFile(
	in RecordstoreSegmentInput,
	segmentID string,
	tenantID string,
	siteID string,
	cameraID string,
	recordingServerID string,
	volumeID string,
	policyID string,
	recordingID string,
) RecordingSegment {
	container := in.Container
	if container == "" {
		container = RecordingSegmentContainerFMP4
	}
	state := in.State
	if state == "" {
		state = RecordingSegmentStateSealed
	}
	var duration time.Duration
	if !in.EndedAt.IsZero() && !in.StartedAt.IsZero() {
		duration = in.EndedAt.Sub(in.StartedAt)
	}
	now := time.Now().UTC()
	return RecordingSegment{
		ID:                segmentID,
		TenantID:          tenantID,
		SiteID:            siteID,
		RecordingServerID: recordingServerID,
		CameraID:          cameraID,
		VolumeID:          volumeID,
		PolicyID:          policyID,
		RecordingID:       recordingID,
		StartedAt:         in.StartedAt,
		EndedAt:           in.EndedAt,
		Duration:          duration,
		Container:         container,
		Path:              in.Path,
		SizeBytes:         in.SizeBytes,
		Tracks:            in.Tracks,
		Checksum:          in.Checksum,
		State:             state,
		CreatedAt:         now,
	}
}
