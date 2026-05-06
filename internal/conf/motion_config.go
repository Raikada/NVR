// Package conf: MotionConfigConfig — persistence shape for the
// per-camera motion-detection configuration introduced in Wave 4.
//
// Per the Wave 4 canonical-divergences entry, motion_config is a
// recorder-local extension: the canonical Camera entity does not
// carry these fields today. The shape lives in conf so the recorder
// can persist it through the same Conf.Validate() / APIConfigSet
// machinery that already serializes RecordingPolicy + RecordingVolume
// overrides.
//
// Wire surface (snake_case, defs.MotionConfig-shaped) lives in
// internal/motion/config.go. This file mirrors the shape with the
// camelCase JSON tags the rest of conf uses, plus a translator
// helper between the two shapes (see motion_config_translate.go).
//
// Map key: canonical Camera UUID (the recorder uses
// cameraIDFromPathName to derive a deterministic id from a path
// name; see internal/api/api_v1_camera_id.go). Stable across
// restarts so the persisted config still applies after a reboot.
package conf

import (
	"fmt"

	"github.com/google/uuid"
)

// MotionConfigConfig is the persistence shape of one camera's
// motion-detection configuration. Mirrors motion.MotionConfig
// field-for-field; the conf-package mirror exists to break a
// hypothetical defs↔conf import cycle (motion lives in internal/motion,
// not internal/defs, but the same convention applies).
type MotionConfigConfig struct {
	Enabled     bool                       `json:"enabled"`
	Source      string                     `json:"source"`
	Sensitivity int                        `json:"sensitivity"`
	ROI         *MotionConfigROI           `json:"roi,omitempty"`
	Schedule    *MotionConfigSchedule      `json:"schedule,omitempty"`
	CooldownMS  int                        `json:"cooldownMs"`
}

// MotionConfigROI mirrors motion.MotionROI.
type MotionConfigROI struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// MotionConfigScheduleWindow mirrors motion.MotionScheduleWindow.
type MotionConfigScheduleWindow struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// MotionConfigSchedule mirrors motion.MotionSchedule.
type MotionConfigSchedule struct {
	Timezone string                       `json:"timezone"`
	Windows  []MotionConfigScheduleWindow `json:"windows"`
}

// validate is invoked by Conf.Validate() per-key. It catches the
// cheap shape errors; deeper invariants (ROI inside frame, schedule
// clock-time format) live in motion.MotionConfig.Validate() and
// fire from the API PATCH handler.
func (mc *MotionConfigConfig) validate(cameraID string) error {
	if cameraID == "" {
		return fmt.Errorf("motion config has empty camera-id key")
	}
	if _, err := uuid.Parse(cameraID); err != nil {
		return fmt.Errorf("motion config camera id '%s' is not a valid UUID: %w", cameraID, err)
	}
	switch mc.Source {
	case "", "onvif", "local_future":
		// "" accepted; the API layer normalizes to "onvif".
	default:
		return fmt.Errorf("motion config '%s' has invalid source '%s'", cameraID, mc.Source)
	}
	if mc.Sensitivity < 0 || mc.Sensitivity > 100 {
		return fmt.Errorf("motion config '%s' has sensitivity %d outside [0, 100]",
			cameraID, mc.Sensitivity)
	}
	if mc.CooldownMS < 0 {
		return fmt.Errorf("motion config '%s' has negative cooldownMs %d",
			cameraID, mc.CooldownMS)
	}
	return nil
}
