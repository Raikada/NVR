package defs

import (
	"time"
)

// CameraSourceType identifies the source mode of a Camera.
//
// Per ADR 0009, this replaces the older `transport` field. Values cover
// every MediaMTX source mode the recorder supports today.
type CameraSourceType string

// Camera source types per ADR 0009 §"Canonical model amendments → Camera"
// (with mpegts_udp added per D9 — the OQ6 ratification).
const (
	CameraSourceTypeRTSP      CameraSourceType = "rtsp"
	CameraSourceTypeRTSPS     CameraSourceType = "rtsps"
	CameraSourceTypeRTMP      CameraSourceType = "rtmp"
	CameraSourceTypeRTMPS     CameraSourceType = "rtmps"
	CameraSourceTypeSRT       CameraSourceType = "srt"
	CameraSourceTypeRTP       CameraSourceType = "rtp"
	CameraSourceTypeMpegTSUDP CameraSourceType = "mpegts_udp"
	CameraSourceTypeWHEP      CameraSourceType = "whep"
	CameraSourceTypeHLS       CameraSourceType = "hls"
	CameraSourceTypeFile      CameraSourceType = "file"
	CameraSourceTypeRedirect  CameraSourceType = "redirect"
	CameraSourceTypePublish   CameraSourceType = "publish"
	CameraSourceTypeRPiCamera CameraSourceType = "rpi_camera"
)

// CameraOnDemand is the on-demand activation block for a Camera.
//
// When enabled, the recorder starts the camera's source only after a
// consumer connects, and tears it down after `CloseAfter` of inactivity.
type CameraOnDemand struct {
	Enabled      bool          `json:"enabled"`
	StartTimeout time.Duration `json:"start_timeout"`
	CloseAfter   time.Duration `json:"close_after"`
}

// CameraAlwaysAvailable describes file-based fallback served when the
// configured source is offline.
type CameraAlwaysAvailable struct {
	Enabled bool     `json:"enabled"`
	File    *string  `json:"file,omitempty"`
	Tracks  []string `json:"tracks,omitempty"`
}

// CameraRuntime is the recorder-reported runtime block on Camera.
//
// Per ADR 0009 §D3 this is read-only at the API; writes ignore it. It
// answers the most common UI question ("is this camera reachable right
// now?") without forcing a separate /v1/streams query.
type CameraRuntime struct {
	Online       bool       `json:"online"`
	Available    bool       `json:"available"`
	LastOnlineAt *time.Time `json:"last_online_at,omitempty"`
}

// Camera is the canonical Camera entity exposed at the recorder's
// /v1/cameras surface per ADR 0009 §D2.
//
// The MS owns Camera authoritatively; the recorder caches and reports.
// All identifiers are UUID strings (see ADR 0009 §D4).
type Camera struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	SiteID            string `json:"site_id"`
	RecordingServerID string `json:"recording_server_id"`
	Name              string `json:"name"`

	SourceType     CameraSourceType `json:"source_type"`
	SourceURL      string           `json:"source_url"`
	CredentialsRef *string          `json:"credentials_ref,omitempty"`

	// SourceConfig is a discriminated union by SourceType. See
	// camera_source_config.go for variants. Phase 1 only marshals these
	// types out; if Phase 2 needs to unmarshal across the discriminator
	// it must add a custom UnmarshalJSON.
	SourceConfig SourceConfig `json:"source_config,omitempty"`

	RecordingPolicyID *string `json:"recording_policy_id,omitempty"`

	// EventChannel selects the vendor event channel (SP3):
	// ''/'auto' auto-resolve, 'onvif', 'amcrest', 'none'. Lives on the
	// store row, not the conf path; the API overlays it on reads.
	EventChannel string `json:"event_channel,omitempty"`

	OnDemand         *CameraOnDemand        `json:"on_demand,omitempty"`
	MaxReaders       *int                   `json:"max_readers,omitempty"`
	FallbackCameraID *string                `json:"fallback_camera_id,omitempty"`
	AlwaysAvailable  *CameraAlwaysAvailable `json:"always_available,omitempty"`

	SourceFingerprint    *string `json:"source_fingerprint,omitempty"`
	UseAbsoluteTimestamp bool    `json:"use_absolute_timestamp"`
	OverridePublisher    bool    `json:"override_publisher"`

	// Runtime is recorder-reported and READ-ONLY at the API per ADR 0009 §D3.
	Runtime *CameraRuntime `json:"runtime,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
