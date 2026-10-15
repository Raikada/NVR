// Package onvif: composite capability probe (SP2). One call that runs
// GetDeviceInformation + GetCapabilities + GetProfiles + stream/snapshot
// URI resolution and aggregates the result for persistence into
// camera_capabilities.
package onvif

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ErrUnauthorized marks a probe rejected by the camera's auth layer, so
// the API can map it to a 400 "camera rejected credentials" instead of
// a generic 500.
var ErrUnauthorized = errors.New("camera rejected credentials")

// CapabilityReport aggregates one full capability probe.
type CapabilityReport struct {
	Device        DeviceInformation `json:"device"`
	Profiles      []MediaProfile    `json:"profiles"`
	SelectedToken string            `json:"selected_profile_token"`
	StreamURI     string            `json:"stream_uri"`
	SnapshotURI   string            `json:"snapshot_uri"`

	HasAudio   bool `json:"has_audio"`
	HasPTZ     bool `json:"has_ptz"`
	HasMotion  bool `json:"has_motion"`
	HasIO      bool `json:"has_io"`
	HasImaging bool `json:"has_imaging"`

	EventsXAddr string `json:"events_xaddr,omitempty"`
}

// ProbeCapabilities runs the full probe against a camera's device
// service XAddr with the supplied credentials.
func ProbeCapabilities(ctx context.Context, xaddr, username, password string) (*CapabilityReport, error) {
	dc := &DeviceClient{XAddr: xaddr, Username: username, Password: password}

	device, err := dc.GetDeviceInformation(ctx)
	if err != nil {
		return nil, classifyProbeError("device information", err)
	}

	caps, err := dc.GetCapabilities(ctx)
	if err != nil {
		return nil, classifyProbeError("capabilities", err)
	}
	if caps.MediaXAddr == "" {
		return nil, fmt.Errorf("camera reports no media service")
	}

	// Cameras report the XAddr of the interface THEY think they're on
	// (LAN IP behind NAT, stale DHCP address after a move). We always
	// talk to the host we successfully probed, keeping only the
	// service's path.
	mediaXAddr := rebaseXAddr(caps.MediaXAddr, xaddr)

	mc := &MediaClient{XAddr: mediaXAddr, Username: username, Password: password}
	profiles, err := mc.GetProfiles(ctx)
	if err != nil {
		return nil, classifyProbeError("profiles", err)
	}
	selected := selectProfile(profiles)
	if selected == nil {
		return nil, fmt.Errorf("camera reports no media profiles")
	}

	streamURI, err := mc.GetStreamURI(ctx, selected.Token)
	if err != nil {
		return nil, classifyProbeError("stream uri", err)
	}
	// Snapshot URI is best-effort: some cameras gate it behind separate
	// capability flags. An error leaves it empty rather than failing
	// the probe (SP4 has a frame-grab fallback anyway).
	snapshotURI, err := mc.GetSnapshotURI(ctx, selected.Token)
	if err != nil {
		snapshotURI = ""
	}

	return &CapabilityReport{
		Device:        device,
		Profiles:      profiles,
		SelectedToken: selected.Token,
		StreamURI:     streamURI,
		SnapshotURI:   snapshotURI,
		HasAudio:      selected.HasAudio,
		HasPTZ:        caps.HasPTZ,
		HasMotion:     caps.EventsXAddr != "",
		HasIO:         caps.HasIO,
		HasImaging:    caps.HasImaging,
		EventsXAddr:   caps.EventsXAddr,
	}, nil
}

// selectProfile picks the recording profile: largest pixel area first,
// audio presence breaking ties. Returns nil for an empty slice.
func selectProfile(profiles []MediaProfile) *MediaProfile {
	if len(profiles) == 0 {
		return nil
	}
	sorted := make([]MediaProfile, len(profiles))
	copy(sorted, profiles)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai, aj := sorted[i].Width*sorted[i].Height, sorted[j].Width*sorted[j].Height
		if ai != aj {
			return ai > aj
		}
		return sorted[i].HasAudio && !sorted[j].HasAudio
	})
	return &sorted[0]
}

// rebaseXAddr keeps serviceXAddr's path but swaps scheme+host for those
// of probedXAddr. Unparseable inputs fall back to serviceXAddr.
func rebaseXAddr(serviceXAddr, probedXAddr string) string {
	svc, err1 := url.Parse(serviceXAddr)
	probed, err2 := url.Parse(probedXAddr)
	if err1 != nil || err2 != nil || probed.Host == "" {
		return serviceXAddr
	}
	svc.Scheme = probed.Scheme
	svc.Host = probed.Host
	return svc.String()
}

// classifyProbeError wraps transport errors, mapping HTTP auth
// rejections onto ErrUnauthorized.
func classifyProbeError(stage string, err error) error {
	if strings.Contains(err.Error(), "authentication required") {
		return fmt.Errorf("%s: %w", stage, ErrUnauthorized)
	}
	return fmt.Errorf("%s: %w", stage, err)
}
