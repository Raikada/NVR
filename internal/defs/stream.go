package defs

import (
	"time"
)

// StreamProtocol identifies the wire protocol of an active Stream.
//
// Per ADR 0009 §"Stream amendments" the canonical enum is extended with
// rtsps and rtmps to track TLS variants.
type StreamProtocol string

// Stream protocols.
const (
	StreamProtocolRTSP   StreamProtocol = "rtsp"
	StreamProtocolRTSPS  StreamProtocol = "rtsps"
	StreamProtocolRTMP   StreamProtocol = "rtmp"
	StreamProtocolRTMPS  StreamProtocol = "rtmps"
	StreamProtocolSRT    StreamProtocol = "srt"
	StreamProtocolWebRTC StreamProtocol = "webrtc"
	StreamProtocolHLS    StreamProtocol = "hls"
)

// StreamDirection indicates whether a Stream publishes media to the
// recorder or reads media from it. New per ADR 0009.
type StreamDirection string

// Stream directions.
const (
	StreamDirectionPublish StreamDirection = "publish"
	StreamDirectionRead    StreamDirection = "read"
)

// StreamTrackKind classifies a track within a Stream.
type StreamTrackKind string

// Stream track kinds.
const (
	StreamTrackKindVideo    StreamTrackKind = "video"
	StreamTrackKindAudio    StreamTrackKind = "audio"
	StreamTrackKindMetadata StreamTrackKind = "metadata"
)

// StreamState is the canonical Stream lifecycle state per
// platform/docs/domain-model.md §Stream. Protocol-specific states (e.g.,
// RTMP idle/read/publish) live on Stream.protocol_specific, not here.
type StreamState string

// Stream states.
const (
	StreamStateConnecting StreamState = "connecting"
	StreamStateActive     StreamState = "active"
	StreamStateStalled    StreamState = "stalled"
	StreamStateEnded      StreamState = "ended"
	StreamStateErrored    StreamState = "errored"
)

// StreamTrack describes one track on an active Stream.
//
// The flat fields (Width, Height, FPS, BitrateKbps) are a denormalized
// summary of the discriminated CodecProps detail per ADR 0009.
type StreamTrack struct {
	Kind        StreamTrackKind `json:"kind"`
	Codec       string          `json:"codec"`
	Width       *int            `json:"width,omitempty"`
	Height      *int            `json:"height,omitempty"`
	FPS         *float64        `json:"fps,omitempty"`
	BitrateKbps *int            `json:"bitrate_kbps,omitempty"`

	// CodecProps is the discriminated codec-specific payload. Per ADR 0009
	// the variants are the nine sub-shapes already defined as
	// APIPathTrackCodecProps in api_path_track_codec_props.go; this field
	// reuses that interface rather than duplicating the variant types.
	CodecProps APIPathTrackCodecProps `json:"codec_props,omitempty"`
}

// Stream is the canonical Stream entity exposed at /v1/streams per
// ADR 0009 §D2.
//
// The recorder is authoritative for active runtime sessions; this entity
// folds in the per-protocol-session content from MediaMTX's
// rtsp/rtsps/rtmp/rtmps/srt/webrtc/hls session and connection endpoints.
type Stream struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	CameraID          string `json:"camera_id"`
	RecordingServerID string `json:"recording_server_id"`

	Protocol  StreamProtocol  `json:"protocol"`
	Direction StreamDirection `json:"direction"`

	State StreamState `json:"state"`

	// RemoteAddr is PII; handlers redact at the API boundary per the
	// data-classification rules.
	RemoteAddr string `json:"remote_addr"`

	UserID *string `json:"user_id,omitempty"`

	// QueryString is Sensitive; handlers redact `token=`, `password=`,
	// `key=`, `secret=` keys at the API boundary.
	QueryString *string `json:"query_string,omitempty"`

	BytesInbound            int64 `json:"bytes_inbound"`
	BytesOutbound           int64 `json:"bytes_outbound"`
	FramesDiscardedOutbound int   `json:"frames_discarded_outbound"`

	Tracks []StreamTrack `json:"tracks,omitempty"`

	// ProtocolSpecific is a discriminated union by Protocol. See
	// stream_protocol_specific.go. Phase 1 only marshals out; Phase 2
	// adds custom UnmarshalJSON if input handlers need to discriminate.
	ProtocolSpecific ProtocolSpecific `json:"protocol_specific,omitempty"`

	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}
