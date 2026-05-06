// Package motion implements the recorder-side motion-detection
// subsystem (Wave 4).
//
// The package exposes a controller that consumes canonical Events with
// kind = "camera.motion_detected" (from Wave 3's ONVIF PullPoint
// translator) and drives motion-mode recording state per camera. It
// also owns the per-camera MotionConfig persistence shape that the
// /v1/recorder/cameras/{id}/motion-config escape hatch reads and
// writes.
//
// Scope and limits in v1:
//
//   - The MotionConfig is recorder-local. Per the canonical-divergences
//     entry added in Wave 4, it lives under /v1/recorder/... rather than
//     on canonical Camera; future slices may elevate it to canonical
//     Camera.motion_config when MS canonical Camera (slice 4-B) gains
//     support.
//   - Motion-mode recording is implemented as "always on with motion-
//     event annotations" rather than true on-demand pipeline start /
//     stop. The controller emits recording.motion_started /
//     recording.motion_ended events into the canonical Event stream;
//     segments record continuously per the underlying RecordingPolicy.
//     True on-demand start / stop is a separate engineering swing
//     because it touches the load-bearing media pipeline (AGENTS.md §7).
//   - ONVIF is the v1 motion source. The local_detector.go scaffold
//     defines the LocalDetector interface and ships a NopLocalDetector
//     so motion_config.source = "local_future" cleanly degrades to "no
//     local detection in v1 — see scaffold for the future slice."
package motion

import (
	"fmt"
	"strings"
	"time"
)

// MotionConfigSource identifies which motion-detection signal source
// the camera uses. ONVIF is the v1 implementation; "local_future" is
// the scaffold for a future CV-based detector.
type MotionConfigSource string

// Source values.
const (
	MotionConfigSourceONVIF       MotionConfigSource = "onvif"
	MotionConfigSourceLocalFuture MotionConfigSource = "local_future"
)

// DefaultCooldown is the default debounce window the controller waits
// after a motion event before considering the camera quiet again. A
// noisy ONVIF feed (Hikvision particularly) emits one event per frame
// of motion; the cooldown collapses bursts into a single
// recording.motion_started / recording.motion_ended pair.
const DefaultCooldown = 5 * time.Second

// DefaultSensitivity is the seed sensitivity for new MotionConfigs.
// 50 is the midpoint of the 0–100 range; the canonical mapping to
// vendor-specific ONVIF analytics-rule sensitivity is camera-specific
// and is currently surfaced only on the local_future scaffold (ONVIF
// rule sensitivity is set on the camera, not on the recorder).
const DefaultSensitivity = 50

// MotionROI is a normalized rectangle of interest. All values are in
// the [0.0, 1.0] domain; (X, Y) is the top-left corner, (W, H) the
// width / height. A nil ROI means "the whole frame" (the v1 default).
type MotionROI struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// validate checks the ROI rectangle stays inside [0, 1] x [0, 1].
func (r MotionROI) validate() error {
	if r.X < 0 || r.X > 1 || r.Y < 0 || r.Y > 1 {
		return fmt.Errorf("roi origin (%g, %g) out of [0, 1] range", r.X, r.Y)
	}
	if r.W < 0 || r.W > 1 || r.H < 0 || r.H > 1 {
		return fmt.Errorf("roi size (%g x %g) out of [0, 1] range", r.W, r.H)
	}
	if r.X+r.W > 1 || r.Y+r.H > 1 {
		return fmt.Errorf("roi extends beyond frame (origin + size > 1)")
	}
	return nil
}

// MotionScheduleWindow mirrors RecordingPolicyScheduleWindow shape so
// schedule editors in the UI can be reused. Days are lower-case
// English weekday names ("monday".."sunday"); start/end are HH:MM
// 24-hour strings.
type MotionScheduleWindow struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// MotionSchedule is the optional schedule block. When present, motion-
// triggered recording only fires while the current wall-clock falls
// inside one of the windows. When nil, motion is always armed.
type MotionSchedule struct {
	Timezone string                 `json:"timezone"`
	Windows  []MotionScheduleWindow `json:"windows"`
}

// MotionConfig is the per-camera motion-detection configuration
// persisted on the recorder. Mirrored to the wire surface at
// /v1/recorder/cameras/{id}/motion-config. Because motion_config is
// recorder-local in v1, the canonical Camera entity does NOT carry
// these fields — see canonical-divergences.md for the migration path.
type MotionConfig struct {
	// Enabled gates the controller. When false the controller ignores
	// motion events for this camera entirely (no events emitted, no
	// recording-mode handling).
	Enabled bool `json:"enabled"`

	// Source is which signal source to consume. v1 only really wires
	// ONVIF; LocalFuture is a clearly-documented stub.
	Source MotionConfigSource `json:"source"`

	// Sensitivity is a 0–100 hint surfaced through the MS / cloud UI
	// for cameras whose vendor-specific ONVIF rule supports it. The
	// recorder doesn't push this value back to the camera in v1; the
	// vendor-side rule is configured on the camera itself. Held here
	// for surface coherence.
	Sensitivity int `json:"sensitivity"`

	// ROI is an optional normalized region-of-interest. When set, the
	// LocalFuture detector (when one ships) restricts processing to
	// that rectangle. ONVIF event filtering happens vendor-side so the
	// ROI is purely advisory for the ONVIF source today.
	ROI *MotionROI `json:"roi,omitempty"`

	// Schedule restricts motion-mode arming to the listed windows.
	Schedule *MotionSchedule `json:"schedule,omitempty"`

	// CooldownMS is the milliseconds the controller waits after a
	// motion event before considering the camera quiet (and emitting
	// recording.motion_ended). Multiple events within the cooldown
	// extend the active window rather than starting fresh.
	CooldownMS int `json:"cooldown_ms"`
}

// DefaultMotionConfig returns a fresh MotionConfig with v1 defaults.
// Used by the API GET handler when no operator-set config exists for
// the camera yet.
func DefaultMotionConfig() MotionConfig {
	return MotionConfig{
		Enabled:     false,
		Source:      MotionConfigSourceONVIF,
		Sensitivity: DefaultSensitivity,
		CooldownMS:  int(DefaultCooldown / time.Millisecond),
	}
}

// Validate checks the config's invariants. Called from the API PATCH
// handler before persisting.
func (mc *MotionConfig) Validate() error {
	switch mc.Source {
	case "", MotionConfigSourceONVIF, MotionConfigSourceLocalFuture:
		// OK; "" is treated as ONVIF on the way in.
	default:
		return fmt.Errorf("invalid motion source %q: expected %q or %q",
			mc.Source, MotionConfigSourceONVIF, MotionConfigSourceLocalFuture)
	}
	if mc.Sensitivity < 0 || mc.Sensitivity > 100 {
		return fmt.Errorf("sensitivity %d out of [0, 100] range", mc.Sensitivity)
	}
	if mc.CooldownMS < 0 {
		return fmt.Errorf("cooldown_ms %d must be non-negative", mc.CooldownMS)
	}
	if mc.ROI != nil {
		if err := mc.ROI.validate(); err != nil {
			return fmt.Errorf("roi: %w", err)
		}
	}
	if mc.Schedule != nil {
		for i, w := range mc.Schedule.Windows {
			if !validClockTime(w.Start) {
				return fmt.Errorf("schedule window %d: invalid start %q (HH:MM expected)", i, w.Start)
			}
			if !validClockTime(w.End) {
				return fmt.Errorf("schedule window %d: invalid end %q (HH:MM expected)", i, w.End)
			}
			for _, d := range w.Days {
				if !validWeekday(d) {
					return fmt.Errorf("schedule window %d: invalid day %q", i, d)
				}
			}
		}
	}
	return nil
}

// Cooldown returns the cooldown as a Duration, applying the default
// when CooldownMS is zero (so a freshly-defaulted MotionConfig still
// has a sensible debounce window).
func (mc *MotionConfig) Cooldown() time.Duration {
	if mc.CooldownMS <= 0 {
		return DefaultCooldown
	}
	return time.Duration(mc.CooldownMS) * time.Millisecond
}

// Normalize fills empty / zero-value fields with their v1 defaults.
// Used after PATCH-merge so unset fields don't leak through as zero
// values that would invalidate the config (e.g., "" source).
func (mc *MotionConfig) Normalize() {
	if mc.Source == "" {
		mc.Source = MotionConfigSourceONVIF
	}
	if mc.Sensitivity == 0 {
		mc.Sensitivity = DefaultSensitivity
	}
	if mc.CooldownMS == 0 {
		mc.CooldownMS = int(DefaultCooldown / time.Millisecond)
	}
}

// validClockTime checks "HH:MM" form. Lax — accepts H:MM as well, in
// keeping with the existing schedule parsing in conf.RecordingPolicyConfig.
func validClockTime(s string) bool {
	if s == "" {
		return false
	}
	colon := strings.Index(s, ":")
	if colon < 1 || colon > 2 || len(s) < colon+3 {
		return false
	}
	hh, mm := s[:colon], s[colon+1:]
	if len(mm) != 2 {
		return false
	}
	for _, r := range hh {
		if r < '0' || r > '9' {
			return false
		}
	}
	for _, r := range mm {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validWeekday(d string) bool {
	switch strings.ToLower(d) {
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
		return true
	}
	return false
}
