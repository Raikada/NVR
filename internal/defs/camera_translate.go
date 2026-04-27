package defs

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/mediamtx/internal/conf"
)

// sourceTypeFromConfSource derives the canonical Camera source_type
// (one of the 12 values per ADR 0009) from a conf.Path's Source string.
//
// MediaMTX uses a free-form Source string for static sources (URL with
// scheme indicating protocol) plus three magic literals: "publisher",
// "redirect", "rpiCamera". This function maps every supported value to
// a canonical CameraSourceType.
func sourceTypeFromConfSource(src string) CameraSourceType {
	switch {
	case src == "publisher":
		return CameraSourceTypePublish
	case src == "redirect":
		return CameraSourceTypeRedirect
	case src == "rpiCamera":
		return CameraSourceTypeRPiCamera
	case strings.HasPrefix(src, "rtsps://") ||
		strings.HasPrefix(src, "rtsps+http://") ||
		strings.HasPrefix(src, "rtsps+ws://"):
		return CameraSourceTypeRTSPS
	case strings.HasPrefix(src, "rtsp://") ||
		strings.HasPrefix(src, "rtsp+http://") ||
		strings.HasPrefix(src, "rtsp+ws://"):
		return CameraSourceTypeRTSP
	case strings.HasPrefix(src, "rtmps://"):
		return CameraSourceTypeRTMPS
	case strings.HasPrefix(src, "rtmp://"):
		return CameraSourceTypeRTMP
	case strings.HasPrefix(src, "srt://"):
		return CameraSourceTypeSRT
	case strings.HasPrefix(src, "wheps://") ||
		strings.HasPrefix(src, "whep://"):
		return CameraSourceTypeWHEP
	case strings.HasPrefix(src, "https://") ||
		strings.HasPrefix(src, "http://"):
		// MediaMTX uses http(s):// only for HLS pulls.
		return CameraSourceTypeHLS
	case strings.HasPrefix(src, "udp+rtp://") ||
		strings.HasPrefix(src, "unix+rtp://"):
		return CameraSourceTypeRTP
	case strings.HasPrefix(src, "udp://") ||
		strings.HasPrefix(src, "udp+mpegts://") ||
		strings.HasPrefix(src, "unix+mpegts://"):
		// MPEG-TS-over-UDP carries no canonical source_type of its own; it is
		// surfaced as "file" today since it's a local-pipe-style ingest.
		// TODO Phase 2: confirm this mapping with the orchestrator; ADR 0009's
		// 12-value enum does not enumerate "mpegts" explicitly.
		return CameraSourceTypeFile
	default:
		// Fall back to "file" for any unrecognized source string. The
		// current set above is exhaustive over MediaMTX's path.go validate()
		// switch, so this branch should be unreachable in practice.
		return CameraSourceTypeFile
	}
}

// confSourceFromSourceType is the reverse mapping: pick the conf.Path
// Source string-or-magic-literal for a canonical Camera source_type.
//
// For URL-shaped source_types (rtsp, rtsps, rtmp, rtmps, srt, whep, hls,
// rtp, file) the caller's Camera.SourceURL is the source; this helper is
// used only for the magic-literal source types (publish, redirect,
// rpi_camera) where the conf.Path Source is not a URL.
func confSourceFromSourceType(st CameraSourceType, sourceURL string) string {
	switch st {
	case CameraSourceTypePublish:
		return "publisher"
	case CameraSourceTypeRedirect:
		return "redirect"
	case CameraSourceTypeRPiCamera:
		return "rpiCamera"
	default:
		return sourceURL
	}
}

// redactCredentials strips userinfo from a URL string, returning
// (redactedURL, hadCredentials). Mirrors the API-layer redactSourceURL
// behavior so the canonical Camera.SourceURL never carries embedded
// secrets per the D4 fix; the userinfo bit (if any) becomes the basis
// for a non-nil credentials_ref.
func redactCredentials(s string) (string, bool) {
	if s == "" || !strings.Contains(s, "@") {
		return s, false
	}
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s, false
	}
	u.User = nil
	return u.String(), true
}

// sourceConfigFromPath builds the Camera.SourceConfig discriminated
// payload from per-source-type fields on a conf.Path.
//
// Returns a typed SourceConfig variant matching st, with whatever fields
// the conf.Path can supply. Empty variants (Publish, RPiCamera, RTP, HLS)
// are still returned as concrete pointers so the caller's response shape
// is consistent.
func sourceConfigFromPath(p *conf.Path, st CameraSourceType) SourceConfig {
	switch st {
	case CameraSourceTypeRTSP, CameraSourceTypeRTSPS:
		out := &SourceConfigRTSP{}
		if t := transportFromConf(p.RTSPTransport); t != nil {
			out.Transport = t
		}
		if p.SRTReadPassphrase != "" {
			pp := p.SRTReadPassphrase
			out.Passphrase = &pp
		}
		return out

	case CameraSourceTypeRTMP, CameraSourceTypeRTMPS:
		return &SourceConfigRTMP{}

	case CameraSourceTypeSRT:
		out := &SourceConfigSRT{}
		if p.SRTReadPassphrase != "" {
			pp := p.SRTReadPassphrase
			out.Passphrase = &pp
		}
		return out

	case CameraSourceTypeWHEP:
		out := &SourceConfigWHEP{}
		if p.WHEPBearerToken != "" {
			// Per ADR 0009, the bearer token is held under a credential-store
			// ref, not inlined. Until Phase 2 wires up a credential store,
			// we just mark presence with an opaque ref string.
			ref := "inline"
			out.BearerTokenRef = &ref
		}
		return out

	case CameraSourceTypeRedirect:
		return &SourceConfigRedirect{Target: p.SourceRedirect}

	case CameraSourceTypeFile:
		// "file" canonical source_type maps to a path-on-disk source. The
		// conf.Path doesn't expose a dedicated field for this today (the
		// always_available fallback is separate); leave Path empty until
		// Phase 2 clarifies.
		return &SourceConfigFile{Path: ""}

	case CameraSourceTypePublish:
		return &SourceConfigPublish{}

	case CameraSourceTypeRPiCamera:
		// Per ADR 0009, the canonical RPi-camera shape is empty; the real
		// ~30-field shape lives in the recorder-localized escape hatch.
		return &SourceConfigRPiCamera{}

	case CameraSourceTypeRTP:
		return &SourceConfigRTP{}

	case CameraSourceTypeHLS:
		return &SourceConfigHLS{}
	}
	return nil
}

// transportFromConf maps conf.RTSPTransport to a canonical CameraTransport.
// Returns nil when the conf value is "automatic" (no specific transport).
func transportFromConf(rt conf.RTSPTransport) *CameraTransport {
	if rt.Protocol == nil {
		return nil
	}
	var v CameraTransport
	switch *rt.Protocol {
	case gortsplib.ProtocolUDP:
		v = CameraTransportUDP
	case gortsplib.ProtocolUDPMulticast:
		v = CameraTransportUDPMulticast
	default:
		v = CameraTransportTCP
	}
	return &v
}

// alwaysAvailableTracksToCanonical projects conf.AlwaysAvailableTrack
// items to a slim string slice for the canonical CameraAlwaysAvailable
// shape. The canonical type tracks codec strings only; the recorder's
// ~4-field per-track conf shape stays in conf.Path.
func alwaysAvailableTracksToCanonical(tracks []conf.AlwaysAvailableTrack) []string {
	if len(tracks) == 0 {
		return nil
	}
	out := make([]string, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, string(t.Codec))
	}
	return out
}

// CameraFromPath translates a conf.Path into a canonical Camera per
// ADR 0009 §"Canonical model amendments → Camera".
//
// Mapping notes:
//   - conf.Path.Name is not stored on Camera (Camera is UUID-keyed); the
//     caller maintains a path-name → camera-UUID side-table and supplies
//     cameraID here.
//   - Source URL userinfo (if present) is redacted before serialization
//     per the D4 fix; CredentialsRef is set non-nil to mark presence.
//   - Recording-policy fields are NOT extracted here; the synthesis helper
//     produces a separate RecordingPolicy and the caller stamps the
//     resulting policy id onto the Camera via recordingPolicyID.
//   - RPi-camera and shell-hooks fields stay in conf.Path; the escape-hatch
//     handlers (Phase 2E) reach into conf.Path directly for those.
//   - pathToCameraID is consulted to map a deprecated Fallback path-name
//     reference to a canonical camera UUID; if the fallback path is not
//     present in the map the field is left nil.
func CameraFromPath(
	p *conf.Path,
	cameraID string,
	tenantID string,
	recordingPolicyID *string,
	pathToCameraID map[string]string,
	runtime *CameraRuntime,
) Camera {
	st := sourceTypeFromConfSource(p.Source)

	// Source URL + credentials_ref split.
	var sourceURL string
	var credentialsRef *string
	switch st {
	case CameraSourceTypePublish, CameraSourceTypeRedirect, CameraSourceTypeRPiCamera:
		// No URL for these source types; SourceURL stays empty.
	default:
		redacted, hadCreds := redactCredentials(p.Source)
		sourceURL = redacted
		if hadCreds {
			// credentials_ref is opaque pre-Phase-2; the literal "inline"
			// marks "userinfo was present in the on-disk URL but we redacted
			// it for the response." Phase 2 may swap this for a real
			// credential-store key.
			ref := "inline"
			credentialsRef = &ref
		}
	}

	out := Camera{
		ID:                cameraID,
		TenantID:          tenantID,
		Name:              p.Name,
		SourceType:        st,
		SourceURL:         sourceURL,
		CredentialsRef:    credentialsRef,
		SourceConfig:      sourceConfigFromPath(p, st),
		RecordingPolicyID: recordingPolicyID,

		UseAbsoluteTimestamp: p.UseAbsoluteTimestamp,
		OverridePublisher:    p.OverridePublisher,
		Runtime:              runtime,
	}

	// Source fingerprint (TLS pin).
	if p.SourceFingerprint != "" {
		fp := p.SourceFingerprint
		out.SourceFingerprint = &fp
	}

	// On-demand block — only emitted when SourceOnDemand is set; the
	// timeouts have non-zero defaults regardless and would falsely imply
	// configuration if surfaced unconditionally.
	if p.SourceOnDemand {
		out.OnDemand = &CameraOnDemand{
			Enabled:      true,
			StartTimeout: time.Duration(p.SourceOnDemandStartTimeout),
			CloseAfter:   time.Duration(p.SourceOnDemandCloseAfter),
		}
	}

	// Max readers: 0 means "no cap" in conf.Path; surface as nil at canonical.
	if p.MaxReaders > 0 {
		mr := p.MaxReaders
		out.MaxReaders = &mr
	}

	// Fallback (deprecated in conf, but still translated). The conf.Path
	// Fallback is a path-name string; we resolve it to a camera UUID via
	// the caller-supplied map. Unknown path-names produce nil.
	if p.Fallback != nil && *p.Fallback != "" {
		// Strip any leading slash that conf.Path stores for path-name
		// fallbacks ("/cameraX" → "cameraX"). URL-style fallbacks (scheme://)
		// can't be canonical-mapped — leave nil if the lookup misses.
		key := strings.TrimPrefix(*p.Fallback, "/")
		if id, ok := pathToCameraID[key]; ok {
			out.FallbackCameraID = &id
		}
	}

	// Always-available block.
	if p.AlwaysAvailable {
		aa := &CameraAlwaysAvailable{Enabled: true}
		if p.AlwaysAvailableFile != "" {
			f := p.AlwaysAvailableFile
			aa.File = &f
		}
		aa.Tracks = alwaysAvailableTracksToCanonical(p.AlwaysAvailableTracks)
		out.AlwaysAvailable = aa
	}

	return out
}

// PathFromCamera is the reverse translator: it produces a conf.Path
// suitable for the recorder to persist from a canonical Camera.
//
// Asymmetries with CameraFromPath:
//   - CredentialsRef is opaque pre-Phase-2; this function cannot reconstruct
//     a credentialed source URL from (SourceURL, CredentialsRef) alone.
//     Callers that need to write a source URL with embedded userinfo must
//     supply the credentials separately on the same write path.
//   - Recording-related fields (Record, RecordPath, RecordPartDuration, etc.)
//     are NOT set here — they come from RecordingPolicy via ApplyPolicyToPath.
//   - RPi-camera and shell-hooks fields stay zero-valued (escape-hatch
//     handlers set them via separate paths).
func PathFromCamera(c Camera) (*conf.Path, error) {
	p := &conf.Path{}

	// Path name (the conf-side primary key) round-trips from Camera.Name.
	p.Name = c.Name

	// Source string. For URL-shaped types, use SourceURL as-is; for the
	// magic-literal types, use the canonical literal.
	p.Source = confSourceFromSourceType(c.SourceType, c.SourceURL)

	// Source fingerprint pin.
	if c.SourceFingerprint != nil {
		p.SourceFingerprint = *c.SourceFingerprint
	}

	// On-demand block.
	if c.OnDemand != nil && c.OnDemand.Enabled {
		p.SourceOnDemand = true
		p.SourceOnDemandStartTimeout = conf.Duration(c.OnDemand.StartTimeout)
		p.SourceOnDemandCloseAfter = conf.Duration(c.OnDemand.CloseAfter)
	}

	// Max readers.
	if c.MaxReaders != nil {
		p.MaxReaders = *c.MaxReaders
	}

	// Fallback — Camera carries fallback_camera_id (canonical UUID); the
	// recorder needs a path-name back. Phase 2 will resolve UUID → path-name
	// at the handler boundary; this layer leaves Fallback nil and lets the
	// caller stamp it.
	// (intentionally not set — see comment above)

	// Always-available block.
	if c.AlwaysAvailable != nil && c.AlwaysAvailable.Enabled {
		p.AlwaysAvailable = true
		if c.AlwaysAvailable.File != nil {
			p.AlwaysAvailableFile = *c.AlwaysAvailable.File
		}
		// Track codec strings round-trip into conf.AlwaysAvailableTrack
		// stubs; sample-rate / channel-count detail can't be reconstructed
		// without input from elsewhere. Phase 2 wires this through if
		// callers need it.
		if len(c.AlwaysAvailable.Tracks) > 0 {
			tracks := make([]conf.AlwaysAvailableTrack, 0, len(c.AlwaysAvailable.Tracks))
			for _, codec := range c.AlwaysAvailable.Tracks {
				tracks = append(tracks, conf.AlwaysAvailableTrack{
					Codec: conf.AlwaysAvailableTrackCodec(codec),
				})
			}
			p.AlwaysAvailableTracks = tracks
		}
	}

	p.UseAbsoluteTimestamp = c.UseAbsoluteTimestamp
	p.OverridePublisher = c.OverridePublisher

	// SourceConfig variants → per-source-type fields on conf.Path.
	if err := applySourceConfigToPath(p, c.SourceType, c.SourceConfig); err != nil {
		return nil, err
	}

	return p, nil
}

// applySourceConfigToPath stamps per-source-type fields on conf.Path
// from the canonical SourceConfig variant.
func applySourceConfigToPath(p *conf.Path, st CameraSourceType, sc SourceConfig) error {
	if sc == nil {
		return nil
	}
	switch st {
	case CameraSourceTypeRTSP, CameraSourceTypeRTSPS:
		v, ok := sc.(*SourceConfigRTSP)
		if !ok {
			return fmt.Errorf("source_config type mismatch: source_type=%s but config is %T", st, sc)
		}
		if v.Transport != nil {
			p.RTSPTransport = transportToConf(*v.Transport)
		}
		// NOTE: SourceConfigRTSP carries a generic Passphrase; conf.Path's
		// dedicated read-side passphrase field is SRTReadPassphrase. Mapping
		// it onto an RTSP source would be incorrect; leave it for callers
		// that need source-specific credential handling.

	case CameraSourceTypeRTMP, CameraSourceTypeRTMPS:
		// SourceConfigRTMP carries Transport but conf.Path has no
		// per-RTMP transport setting today; nothing to apply.

	case CameraSourceTypeSRT:
		v, ok := sc.(*SourceConfigSRT)
		if !ok {
			return fmt.Errorf("source_config type mismatch: source_type=%s but config is %T", st, sc)
		}
		if v.Passphrase != nil {
			p.SRTReadPassphrase = *v.Passphrase
		}

	case CameraSourceTypeWHEP:
		// Bearer token reconstruction is asymmetric (see PathFromCamera
		// comment); callers supply WHEPBearerToken separately on writes.

	case CameraSourceTypeRedirect:
		v, ok := sc.(*SourceConfigRedirect)
		if !ok {
			return fmt.Errorf("source_config type mismatch: source_type=%s but config is %T", st, sc)
		}
		p.SourceRedirect = v.Target

	case CameraSourceTypeFile, CameraSourceTypePublish,
		CameraSourceTypeRPiCamera, CameraSourceTypeRTP, CameraSourceTypeHLS:
		// Nothing to apply; the relevant data either lives in SourceURL
		// (file/rtp/hls) or in escape-hatch resources (rpi_camera) or has
		// no per-source-type knobs (publish).
	}
	return nil
}

// transportToConf maps a canonical CameraTransport back to conf.RTSPTransport.
func transportToConf(t CameraTransport) conf.RTSPTransport {
	var p gortsplib.Protocol
	switch t {
	case CameraTransportUDP:
		p = gortsplib.ProtocolUDP
	case CameraTransportUDPMulticast:
		p = gortsplib.ProtocolUDPMulticast
	default:
		p = gortsplib.ProtocolTCP
	}
	return conf.RTSPTransport{Protocol: &p}
}
