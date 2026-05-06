// Translation between conf.MotionConfigConfig (persistence shape, in
// the conf package) and motion.MotionConfig (runtime / API shape, in
// this package).
//
// The two shapes mirror each other one-for-one; the split exists for
// the same reason recording-policy split — defs/conf can't both
// import each other, and even though motion lives in internal/motion
// rather than internal/defs, the same packaging discipline applies:
// the conf shape uses camelCase JSON tags (matching the rest of
// conf.Path's MediaMTX-lineage convention) while the wire surface
// uses snake_case.

package motion

import (
	"github.com/bluenviron/mediamtx/internal/conf"
)

// FromConfigConfig converts a conf.MotionConfigConfig into the
// runtime MotionConfig shape. Nil input returns DefaultMotionConfig().
func FromConfigConfig(c *conf.MotionConfigConfig) MotionConfig {
	if c == nil {
		return DefaultMotionConfig()
	}
	mc := MotionConfig{
		Enabled:     c.Enabled,
		Source:      MotionConfigSource(c.Source),
		Sensitivity: c.Sensitivity,
		CooldownMS:  c.CooldownMS,
	}
	if c.ROI != nil {
		mc.ROI = &MotionROI{X: c.ROI.X, Y: c.ROI.Y, W: c.ROI.W, H: c.ROI.H}
	}
	if c.Schedule != nil {
		s := &MotionSchedule{Timezone: c.Schedule.Timezone}
		for _, w := range c.Schedule.Windows {
			s.Windows = append(s.Windows, MotionScheduleWindow{
				Days:  append([]string{}, w.Days...),
				Start: w.Start,
				End:   w.End,
			})
		}
		mc.Schedule = s
	}
	mc.Normalize()
	return mc
}

// ToConfigConfig converts a runtime MotionConfig into a
// conf.MotionConfigConfig for persistence.
func ToConfigConfig(mc MotionConfig) *conf.MotionConfigConfig {
	c := &conf.MotionConfigConfig{
		Enabled:     mc.Enabled,
		Source:      string(mc.Source),
		Sensitivity: mc.Sensitivity,
		CooldownMS:  mc.CooldownMS,
	}
	if mc.ROI != nil {
		c.ROI = &conf.MotionConfigROI{X: mc.ROI.X, Y: mc.ROI.Y, W: mc.ROI.W, H: mc.ROI.H}
	}
	if mc.Schedule != nil {
		s := &conf.MotionConfigSchedule{Timezone: mc.Schedule.Timezone}
		for _, w := range mc.Schedule.Windows {
			s.Windows = append(s.Windows, conf.MotionConfigScheduleWindow{
				Days:  append([]string{}, w.Days...),
				Start: w.Start,
				End:   w.End,
			})
		}
		c.Schedule = s
	}
	return c
}
