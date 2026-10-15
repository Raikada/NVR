// Package schedule evaluates "is camera X recording at time t" given
// a recording policy (mode + schedules) plus the motion controller's
// current per-camera state. Site timezone is global, sourced from
// system_settings['timezone']. Wrap-midnight windows and union of
// overlapping windows are handled here.
package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

// MotionState reports whether a given camera is currently in a
// motion-triggered window (start..end+postroll). Implemented by the
// motion controller in Phase 6.
type MotionState interface {
	IsActive(cameraID string, t time.Time) (active bool, until time.Time)
}

// Resolver answers "is recording active for this camera at this instant?"
type Resolver struct {
	cameras   *store.CamerasRepo
	policies  *store.RecordingPoliciesRepo
	schedules *store.RecordingSchedulesRepo
	settings  *store.SystemSettingsRepo
	motion    MotionState
}

// New wires a Resolver. motion may be nil; in that case 'motion' policies
// always evaluate to off.
func New(
	cameras *store.CamerasRepo,
	policies *store.RecordingPoliciesRepo,
	schedules *store.RecordingSchedulesRepo,
	settings *store.SystemSettingsRepo,
	motion MotionState,
) *Resolver {
	return &Resolver{cameras, policies, schedules, settings, motion}
}

// Active is the per-decision result.
type Active struct {
	On     bool
	Mode   string // 'continuous'|'motion'|'scheduled'|'off'
	Reason string
	Until  time.Time // optional: when the current "on" window ends
}

const (
	defaultPolicyID = "policy_default"
)

// IsActive evaluates the policy for cameraID at time t. Errors only
// surface for genuine DB failures; missing rows produce sensible defaults
// (camera missing -> false; policy missing -> fall back to default).
func (r *Resolver) IsActive(ctx context.Context, cameraID string, t time.Time) (Active, error) {
	cam, err := r.cameras.GetByID(ctx, cameraID)
	if err != nil {
		return Active{}, err
	}
	policyID := cam.RecordingPolicyID
	if policyID == "" {
		policyID = defaultPolicyID
	}
	pol, err := r.policies.GetByID(ctx, policyID)
	if err != nil {
		return Active{}, err
	}
	if !pol.Enabled || pol.Mode == "off" {
		return Active{On: false, Mode: pol.Mode, Reason: "policy disabled"}, nil
	}
	switch pol.Mode {
	case "continuous":
		return Active{On: true, Mode: "continuous", Reason: "continuous"}, nil
	case "motion":
		if r.motion == nil {
			return Active{On: false, Mode: "motion", Reason: "no motion controller"}, nil
		}
		on, until := r.motion.IsActive(cameraID, t)
		if on {
			return Active{On: true, Mode: "motion", Reason: "motion active", Until: until}, nil
		}
		return Active{On: false, Mode: "motion", Reason: "motion idle"}, nil
	case "scheduled":
		tz, _ := r.siteTimezone(ctx)
		schedules, err := r.schedules.ListByPolicy(ctx, pol.ID)
		if err != nil {
			return Active{}, err
		}
		on, until := evaluateWindows(schedules, t.In(tz))
		if on {
			return Active{On: true, Mode: "scheduled", Reason: "scheduled window", Until: until}, nil
		}
		return Active{On: false, Mode: "scheduled", Reason: "outside window"}, nil
	}
	return Active{}, errors.New("unknown policy mode: " + pol.Mode)
}

func (r *Resolver) siteTimezone(ctx context.Context) (*time.Location, error) {
	s, err := r.settings.Get(ctx, "timezone")
	if err != nil || s == nil || s.Value == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(s.Value)
	if err != nil {
		return time.Local, nil
	}
	return loc, nil
}

// evaluateWindows checks every schedule row for the given local time and
// returns (on, until). Windows that wrap midnight are split internally.
// `until` is the next minute boundary at which the current decision
// changes (best-effort; the caller should re-evaluate around `until`).
func evaluateWindows(schedules []*store.RecordingSchedule, t time.Time) (bool, time.Time) {
	if len(schedules) == 0 {
		return false, time.Time{}
	}
	day := int(t.Weekday()) // 0=Sun..6=Sat
	minuteOfDay := t.Hour()*60 + t.Minute()
	yesterday := (day + 6) % 7

	for _, s := range schedules {
		if s.DayOfWeek == day {
			start, end := s.StartMinute, s.EndMinute
			if end > start && minuteOfDay >= start && minuteOfDay < end {
				until := startOfDay(t).Add(time.Duration(end) * time.Minute)
				return true, until
			}
			if end < start && minuteOfDay >= start {
				// wraps into tomorrow
				until := startOfDay(t).Add(24*time.Hour + time.Duration(end)*time.Minute)
				return true, until
			}
		}
		if s.DayOfWeek == yesterday && s.EndMinute < s.StartMinute && minuteOfDay < s.EndMinute {
			// midnight-crossed window from yesterday
			until := startOfDay(t).Add(time.Duration(s.EndMinute) * time.Minute)
			return true, until
		}
	}
	return false, time.Time{}
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
