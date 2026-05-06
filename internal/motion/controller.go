// Motion controller: consumes camera.motion_detected events, drives
// motion-mode recording state per camera, and emits
// recording.motion_started / recording.motion_ended events.
//
// Per ADR 0009 §D2 the canonical Event entity is the event vocabulary;
// motion is one consumer of that vocabulary, not a parallel system.
// The controller's contract:
//
//   - On each camera.motion_detected event, look up the camera's
//     MotionConfig (from the supplied resolver) and the camera's
//     RecordingPolicy.mode (from the recording-policy resolver). If
//     motion_config.enabled is false, ignore. If
//     RecordingPolicy.mode != "motion", ignore. Otherwise, mark the
//     camera as motion-active and (re)start a per-camera cooldown
//     timer.
//   - First motion event for a camera in a quiet window: emit
//     recording.motion_started.
//   - Subsequent motion events while still active: extend the cooldown
//     window; do NOT re-emit motion_started.
//   - Cooldown timer expires without further motion: emit
//     recording.motion_ended.
//
// The controller does NOT call into the recording pipeline directly.
// Per AGENTS.md §7 ("Do not touch the media pipeline unless asked")
// motion-mode recording is implemented as "always on with motion-event
// annotations" in v1: the underlying RecordingPolicy already handles
// disk writes for motion mode (mode != off => Record=true via
// ApplyPolicyToPath); the controller adds the canonical Event stream
// markers that downstream consumers (clip extraction, MS rollups, UI
// timelines) use to identify motion-active intervals. True on-demand
// pipeline start / stop is a separate engineering swing.

package motion

import (
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// MotionConfigResolver returns the per-camera MotionConfig. Nil + ok=false
// when no config exists; the controller treats that as "use the default
// config (disabled)" so motion events for un-configured cameras are
// ignored.
type MotionConfigResolver func(cameraID string) (MotionConfig, bool)

// PolicyModeResolver returns the RecordingPolicy.mode for the named
// camera, or "" + ok=false when the camera has no policy. The
// controller treats absent / non-motion mode as "ignore the event."
type PolicyModeResolver func(cameraID string) (defs.RecordingPolicyMode, bool)

// EventEmitter publishes a canonical Event. The controller uses this
// to emit recording.motion_started / recording.motion_ended; the
// concrete implementation in the recorder is api.DefaultEventStore().
// Decoupling via interface lets tests assert against a fake without
// pulling in the api package's singleton.
type EventEmitter interface {
	Publish(in defs.EventInput, recordingServerID, tenantID, siteID string) defs.Event
}

// TenantIDFn returns the recorder's bound tenant id. Threaded in
// rather than read directly so tests don't need to spin up a *conf.Conf.
type TenantIDFn func() string

// Controller is the motion-mode state machine. One instance per
// recorder process; one cooldown timer per camera. Safe for
// concurrent use.
type Controller struct {
	logger     logger.Writer
	emitter    EventEmitter
	configFn   MotionConfigResolver
	policyFn   PolicyModeResolver
	tenantFn   TenantIDFn
	now        func() time.Time
	newTimer   func(d time.Duration, fn func()) Timer

	mu     sync.Mutex
	active map[string]*activeMotion
}

// activeMotion tracks one camera's currently-active motion window.
type activeMotion struct {
	startedAt time.Time
	expiresAt time.Time
	timer     Timer
	// lastMessageID lets the test suite (and operators) correlate the
	// closing recording.motion_ended event back to whatever started
	// the motion. Best-effort; many ONVIF cameras emit no message-id.
	lastMessageID string
}

// Timer is the minimal interface the controller needs from a
// time.Timer. Lets us swap a deterministic fake into tests that
// fast-forward time instead of sleeping.
type Timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

// realTimer wraps time.AfterFunc. The Reset semantics mirror
// time.Timer's: returns true if a previous timer was stopped before
// firing, false otherwise.
type realTimer struct {
	t *time.Timer
}

func (r *realTimer) Stop() bool                  { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool  { return r.t.Reset(d) }

// NewController constructs a controller wired to the supplied
// dependencies. All callbacks are required; nil emitter or nil
// resolvers will panic on first event.
func NewController(
	log logger.Writer,
	emitter EventEmitter,
	configFn MotionConfigResolver,
	policyFn PolicyModeResolver,
	tenantFn TenantIDFn,
) *Controller {
	c := &Controller{
		logger:   log,
		emitter:  emitter,
		configFn: configFn,
		policyFn: policyFn,
		tenantFn: tenantFn,
		now:      func() time.Time { return time.Now().UTC() },
		active:   make(map[string]*activeMotion),
	}
	c.newTimer = func(d time.Duration, fn func()) Timer {
		return &realTimer{t: time.AfterFunc(d, fn)}
	}
	return c
}

// SetTimerFactory swaps the timer constructor. Tests use this to
// inject a fake-time scheduler. Must be called before any events flow.
func (c *Controller) SetTimerFactory(f func(d time.Duration, fn func()) Timer) {
	c.newTimer = f
}

// SetNowFn swaps the wall-clock source. Tests use this for
// deterministic timestamps on emitted events.
func (c *Controller) SetNowFn(f func() time.Time) {
	c.now = f
}

// HandleMotionEvent is the controller's entry point. The recorder
// wires this into the canonical Event sink (see core.go) so any event
// with kind = "camera.motion_detected" routes here. Other event kinds
// fall through unchanged.
func (c *Controller) HandleMotionEvent(ev defs.Event) {
	if ev.Kind != "camera.motion_detected" {
		return
	}
	if ev.SubjectKind != defs.EventSubjectKindCamera || ev.SubjectID == "" {
		return
	}

	cameraID := ev.SubjectID

	cfg, ok := c.configFn(cameraID)
	if !ok || !cfg.Enabled {
		c.log(logger.Debug,
			"[motion] ignoring motion event for camera %s: motion_config disabled or absent",
			cameraID)
		return
	}

	mode, _ := c.policyFn(cameraID)
	if mode != defs.RecordingPolicyModeMotion {
		c.log(logger.Debug,
			"[motion] ignoring motion event for camera %s: recording_policy.mode=%q (not motion)",
			cameraID, mode)
		return
	}

	if cfg.Source == MotionConfigSourceLocalFuture {
		// In v1 only ONVIF actually drives events. local_future is a
		// scaffold; logging once per event keeps the operator informed
		// without flooding (the typical motion burst is 5–30 events
		// per real-world motion, so a Warn is loud but not catastrophic).
		c.log(logger.Warn,
			"[motion] camera %s motion_config.source=local_future; "+
				"local CV detection is not implemented in v1 "+
				"(treating ONVIF event as fallback)",
			cameraID)
	}

	cooldown := cfg.Cooldown()
	now := c.now()
	expires := now.Add(cooldown)

	c.mu.Lock()
	defer c.mu.Unlock()

	cur, alreadyActive := c.active[cameraID]
	if alreadyActive {
		// Extend the existing window: stop and reset the timer rather
		// than constructing a new one. Per Go's time.Timer Reset
		// guidance the previous fire was already drained or has not
		// fired yet (we're holding c.mu so the timer's callback can't
		// be racing this code path past the lock acquisition above —
		// but the timer may have fired and queued a goroutine that's
		// blocked on c.mu, so we still need to handle a "timer fired
		// but cleanup not yet run" case. The fire-callback rechecks
		// expiresAt under c.mu, so resetting expiresAt + Reset is
		// safe: a stale-fire callback observes expiresAt > now and
		// re-schedules itself.)
		cur.expiresAt = expires
		if ev.CorrelationID != nil {
			cur.lastMessageID = *ev.CorrelationID
		}
		cur.timer.Reset(cooldown)
		c.log(logger.Debug, "[motion] camera %s motion extended; new expiry at %s",
			cameraID, expires.Format(time.RFC3339))
		return
	}

	// First event: open a new motion window. Emit motion_started under
	// the lock so the start-then-end ordering is monotonic.
	a := &activeMotion{
		startedAt: now,
		expiresAt: expires,
	}
	if ev.CorrelationID != nil {
		a.lastMessageID = *ev.CorrelationID
	}
	a.timer = c.newTimer(cooldown, func() { c.onCooldownExpired(cameraID) })
	c.active[cameraID] = a

	c.emitMotionStarted(cameraID, ev)
	c.log(logger.Info, "[motion] camera %s motion started; cooldown %s", cameraID, cooldown)
}

// onCooldownExpired runs when a per-camera timer fires. We re-check
// under the lock because a stale timer may fire after a Reset
// extension that should have prevented it.
func (c *Controller) onCooldownExpired(cameraID string) {
	c.mu.Lock()
	cur, ok := c.active[cameraID]
	if !ok {
		c.mu.Unlock()
		return
	}
	now := c.now()
	if cur.expiresAt.After(now) {
		// A Reset() landed between the fire and us acquiring the lock.
		// The Reset already re-armed the timer; nothing to do.
		c.mu.Unlock()
		return
	}
	delete(c.active, cameraID)
	startedAt := cur.startedAt
	lastMessageID := cur.lastMessageID
	c.mu.Unlock()

	c.emitMotionEnded(cameraID, startedAt, now, lastMessageID)
	c.log(logger.Info,
		"[motion] camera %s motion ended; duration %s",
		cameraID, now.Sub(startedAt))
}

// emitMotionStarted publishes recording.motion_started. Severity Info:
// motion is normal operational activity, not an incident.
func (c *Controller) emitMotionStarted(cameraID string, source defs.Event) {
	tenantID := ""
	if c.tenantFn != nil {
		tenantID = c.tenantFn()
	}
	attrs := map[string]string{
		"camera_id": cameraID,
	}
	if source.Attributes != nil {
		// Carry through the source ONVIF topic so consumers can see
		// which analytics rule actually fired. Don't overwrite our
		// own camera_id.
		if topic, ok := source.Attributes["onvif_topic"]; ok && topic != "" {
			attrs["onvif_topic"] = topic
		}
	}
	in := defs.EventInput{
		Kind:        "recording.motion_started",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "motion-mode recording start (motion detected)",
		Attributes:  attrs,
		OccurredAt:  c.now(),
	}
	if source.CorrelationID != nil {
		corr := *source.CorrelationID
		in.CorrelationID = &corr
	}
	c.emitter.Publish(in, source.RecordingServerID, tenantID, source.SiteID)
}

// emitMotionEnded publishes recording.motion_ended.
func (c *Controller) emitMotionEnded(cameraID string, startedAt, endedAt time.Time, lastMessageID string) {
	tenantID := ""
	if c.tenantFn != nil {
		tenantID = c.tenantFn()
	}
	attrs := map[string]string{
		"camera_id":     cameraID,
		"started_at":    startedAt.UTC().Format(time.RFC3339Nano),
		"ended_at":      endedAt.UTC().Format(time.RFC3339Nano),
		"duration_ms":   formatDurationMS(endedAt.Sub(startedAt)),
	}
	in := defs.EventInput{
		Kind:        "recording.motion_ended",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "motion-mode recording end (cooldown elapsed)",
		Attributes:  attrs,
		OccurredAt:  endedAt,
	}
	if lastMessageID != "" {
		corr := lastMessageID
		in.CorrelationID = &corr
	}
	c.emitter.Publish(in, "", tenantID, "")
}

// IsActive reports whether the camera is currently in an active
// motion window. Used by tests + UI status snippets.
func (c *Controller) IsActive(cameraID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.active[cameraID]
	return ok
}

// ActiveCount returns the number of cameras in an active motion
// window. Cheap snapshot for health surfaces.
func (c *Controller) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

// Close stops every per-camera timer. The recorder wires this into
// the package-shutdown path so a graceful shutdown does not leak
// goroutines through the AfterFunc callbacks. Idempotent; emits no
// motion_ended events (shutting-down recorder is not the right time
// to flood the event store).
func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.active {
		a.timer.Stop()
	}
	c.active = make(map[string]*activeMotion)
}

func (c *Controller) log(level logger.Level, format string, args ...any) {
	if c.logger == nil {
		return
	}
	c.logger.Log(level, format, args...)
}

// formatDurationMS renders a duration as a base-10 millisecond integer
// string. Avoids strconv import on the hot path's signature.
func formatDurationMS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	// inline base-10 itoa to avoid strconv.Itoa allocation overhead
	if ms == 0 {
		return "0"
	}
	buf := make([]byte, 0, 16)
	for ms > 0 {
		buf = append([]byte{'0' + byte(ms%10)}, buf...)
		ms /= 10
	}
	return string(buf)
}
