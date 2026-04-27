package defs

// SourceConfig is the discriminated union of source-mode-specific
// configuration on a Camera, keyed by Camera.SourceType.
//
// Phase 1 of ADR 0009 only marshals these types out. JSON unmarshaling
// across the discriminator is not implemented; Phase 2 adds a custom
// UnmarshalJSON on Camera if PATCH/POST input handlers need it.
type SourceConfig interface {
	sourceConfig()
}

// CameraTransport selects the transport for RTSP / RTSPS / RTMP /
// RTMPS / SRT pull sources.
type CameraTransport string

// RTSP-family transports.
const (
	CameraTransportTCP          CameraTransport = "tcp"
	CameraTransportUDP          CameraTransport = "udp"
	CameraTransportUDPMulticast CameraTransport = "udp_multicast"
)

// SourceConfigRTSP is source_config for source_type=rtsp / source_type=rtsps.
type SourceConfigRTSP struct {
	Transport  *CameraTransport `json:"transport,omitempty"`
	Passphrase *string          `json:"passphrase,omitempty"`
}

func (*SourceConfigRTSP) sourceConfig() {}

// SourceConfigRTMP is source_config for source_type=rtmp / source_type=rtmps.
type SourceConfigRTMP struct {
	Transport *CameraTransport `json:"transport,omitempty"`
}

func (*SourceConfigRTMP) sourceConfig() {}

// SourceConfigSRT is source_config for source_type=srt.
type SourceConfigSRT struct {
	Transport  *CameraTransport `json:"transport,omitempty"`
	Passphrase *string          `json:"passphrase,omitempty"`
}

func (*SourceConfigSRT) sourceConfig() {}

// SourceConfigWHEP is source_config for source_type=whep.
//
// The bearer token is held under a credential-store ref, not inlined.
type SourceConfigWHEP struct {
	BearerTokenRef *string `json:"bearer_token_ref,omitempty"`
}

func (*SourceConfigWHEP) sourceConfig() {}

// SourceConfigRedirect is source_config for source_type=redirect.
type SourceConfigRedirect struct {
	Target string `json:"target"`
}

func (*SourceConfigRedirect) sourceConfig() {}

// SourceConfigFile is source_config for source_type=file.
type SourceConfigFile struct {
	Path string `json:"path"`
}

func (*SourceConfigFile) sourceConfig() {}

// SourceConfigPublish is source_config for source_type=publish.
//
// Empty canonical sub-shape per ADR 0009; a publish path simply waits
// for any-protocol publish.
type SourceConfigPublish struct{}

func (*SourceConfigPublish) sourceConfig() {}

// SourceConfigRPiCamera is the canonical stub for source_type=rpi_camera.
//
// The real ~30-field RPi-camera shape lives in the recorder-localized
// escape hatch at /v1/recorder/cameras/{id}/source-config; the canonical
// Camera.source_config keeps an empty marker per ADR 0009.
type SourceConfigRPiCamera struct{}

func (*SourceConfigRPiCamera) sourceConfig() {}

// SourceConfigRTP is source_config for source_type=rtp.
//
// Empty canonical sub-shape; the SDP descriptor location is captured by
// Camera.source_url.
type SourceConfigRTP struct{}

func (*SourceConfigRTP) sourceConfig() {}

// SourceConfigHLS is source_config for source_type=hls.
//
// Empty canonical sub-shape today; HLS pulls do not carry extra knobs.
type SourceConfigHLS struct{}

func (*SourceConfigHLS) sourceConfig() {}
