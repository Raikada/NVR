// Tests for the motion controller. Uses a fake timer + fake event
// emitter so the test suite is deterministic without real wall-clock
// time. The fake emitter captures published events for assertion.

package motion

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

type fakeEmitter struct {
	mu     sync.Mutex
	events []defs.Event
}

func (f *fakeEmitter) Publish(in defs.EventInput, _, tenantID, _ string) defs.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	occurred := in.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	e := defs.Event{
		ID:          "test-" + in.Kind,
		TenantID:    tenantID,
		OccurredAt:  occurred,
		Kind:        in.Kind,
		Severity:    in.Severity,
		SubjectKind: in.SubjectKind,
		SubjectID:   in.SubjectID,
		Message:     in.Message,
		Attributes:  in.Attributes,
	}
	if in.CorrelationID != nil {
		c := *in.CorrelationID
		e.CorrelationID = &c
	}
	f.events = append(f.events, e)
	return e
}

func (f *fakeEmitter) snapshot() []defs.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]defs.Event, len(f.events))
	copy(out, f.events)
	return out
}

func (f *fakeEmitter) byKind(kind string) []defs.Event {
	out := []defs.Event{}
	for _, e := range f.snapshot() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// fakeTimer is a deterministic Timer implementation. The controller
// would normally call newTimer(d, fn), get a real time.AfterFunc,
// and the fn would fire on a background goroutine after d. Here we
// capture (d, fn) and let the test fire fn synchronously by calling
// Fire(); Reset just updates the captured d, and Stop is a no-op
// (the test never fires a stopped timer).
type fakeTimer struct {
	mu       sync.Mutex
	d        time.Duration
	fn       func()
	fired    bool
	stopped  bool
}

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.stopped
	t.stopped = true
	return !prev
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.d = d
	t.stopped = false
	return false
}

func (t *fakeTimer) Fire() {
	t.mu.Lock()
	if t.stopped || t.fired || t.fn == nil {
		t.mu.Unlock()
		return
	}
	t.fired = true
	fn := t.fn
	t.mu.Unlock()
	fn()
}

// fakeTimerFactory returns a factory that records every newTimer call
// in the supplied slice. Tests inspect the slice to fire timers.
func fakeTimerFactory(timers *[]*fakeTimer, mu *sync.Mutex) func(d time.Duration, fn func()) Timer {
	return func(d time.Duration, fn func()) Timer {
		t := &fakeTimer{d: d, fn: fn}
		mu.Lock()
		*timers = append(*timers, t)
		mu.Unlock()
		return t
	}
}

func motionEvent(cameraID string) defs.Event {
	return defs.Event{
		Kind:        "camera.motion_detected",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Attributes: map[string]string{
			"onvif_topic": "tns1:VideoSource/MotionAlarm",
		},
	}
}

// helper: motion config with sensible defaults + override knobs.
func cfg(enabled bool) MotionConfig {
	c := DefaultMotionConfig()
	c.Enabled = enabled
	return c
}

// TestController_MotionEventTriggersStarted: the first
// camera.motion_detected event for a configured + motion-mode camera
// emits a recording.motion_started.
func TestController_MotionEventTriggersStarted(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-1"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil,
		emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	var timers []*fakeTimer
	var mu sync.Mutex
	c.SetTimerFactory(fakeTimerFactory(&timers, &mu))

	c.HandleMotionEvent(motionEvent(cameraID))

	started := emitter.byKind("recording.motion_started")
	require.Len(t, started, 1)
	require.Equal(t, defs.EventSubjectKindCamera, started[0].SubjectKind)
	require.Equal(t, cameraID, started[0].SubjectID)
	require.Equal(t, "tenant-test", started[0].TenantID)
	require.Equal(t, defs.EventSeverityInfo, started[0].Severity)
	require.Equal(t, cameraID, started[0].Attributes["camera_id"])
	require.Equal(t, "tns1:VideoSource/MotionAlarm", started[0].Attributes["onvif_topic"])
	require.True(t, c.IsActive(cameraID))
	require.Equal(t, 1, c.ActiveCount())
	require.Len(t, timers, 1)
}

// TestController_CooldownExpiresEmitsEnded: after the cooldown timer
// fires, the controller emits recording.motion_ended and clears the
// per-camera state.
func TestController_CooldownExpiresEmitsEnded(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-2"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	// Deterministic clock: each call advances by 1ms. The handler
	// captures startedAt at first event; cooldown-fire reads the
	// expiresAt assertion under the lock, then we advance time past
	// expiresAt before the timer fires.
	var nowMu sync.Mutex
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	c.SetNowFn(nowFn)
	defer c.Close()

	var timers []*fakeTimer
	var mu sync.Mutex
	c.SetTimerFactory(fakeTimerFactory(&timers, &mu))

	c.HandleMotionEvent(motionEvent(cameraID))
	require.True(t, c.IsActive(cameraID))

	// Advance past the cooldown so the controller's onCooldownExpired
	// re-check sees expiresAt <= now.
	advance(DefaultCooldown + 100*time.Millisecond)

	require.Len(t, timers, 1)
	timers[0].Fire()

	ended := emitter.byKind("recording.motion_ended")
	require.Len(t, ended, 1)
	require.Equal(t, cameraID, ended[0].SubjectID)
	require.Equal(t, cameraID, ended[0].Attributes["camera_id"])
	require.Equal(t, defs.EventSeverityInfo, ended[0].Severity)

	require.False(t, c.IsActive(cameraID))
	require.Equal(t, 0, c.ActiveCount())
}

// TestController_SubsequentEventsExtendNotRestart: while motion is
// active, additional events extend the cooldown but DO NOT emit a new
// motion_started.
func TestController_SubsequentEventsExtendNotRestart(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-3"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	var timers []*fakeTimer
	var mu sync.Mutex
	c.SetTimerFactory(fakeTimerFactory(&timers, &mu))

	for i := 0; i < 5; i++ {
		c.HandleMotionEvent(motionEvent(cameraID))
	}

	started := emitter.byKind("recording.motion_started")
	require.Len(t, started, 1, "expected a single motion_started even though 5 events fired")
	ended := emitter.byKind("recording.motion_ended")
	require.Len(t, ended, 0, "expected no motion_ended yet — camera still active")
	require.True(t, c.IsActive(cameraID))
}

// TestController_ModeNotMotionIgnored: events for a camera whose
// RecordingPolicy.mode is not "motion" never produce motion_started.
func TestController_ModeNotMotionIgnored(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-4"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	// Continuous, not motion.
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeContinuous}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	c.HandleMotionEvent(motionEvent(cameraID))

	require.Empty(t, emitter.byKind("recording.motion_started"))
	require.Empty(t, emitter.byKind("recording.motion_ended"))
	require.False(t, c.IsActive(cameraID))
}

// TestController_DisabledConfigIgnored: motion_config.enabled = false
// gates out the camera even when the policy is motion mode.
func TestController_DisabledConfigIgnored(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-5"

	configs := map[string]MotionConfig{cameraID: cfg(false)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	c.HandleMotionEvent(motionEvent(cameraID))

	require.Empty(t, emitter.byKind("recording.motion_started"))
	require.False(t, c.IsActive(cameraID))
}

// TestController_AbsentConfigIgnored: a camera without an explicit
// MotionConfig is treated as disabled.
func TestController_AbsentConfigIgnored(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-6"

	configs := map[string]MotionConfig{} // empty
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	c.HandleMotionEvent(motionEvent(cameraID))

	require.Empty(t, emitter.byKind("recording.motion_started"))
}

// TestController_NonMotionEventsIgnored: events with a different kind
// fall through. The controller is registered as a sink for *all*
// canonical events; only motion-detected ones should drive it.
func TestController_NonMotionEventsIgnored(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-7"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	for _, kind := range []string{
		"camera.online", "camera.offline", "camera.tamper_detected",
		"recording.motion_started", "recording.motion_ended",
	} {
		c.HandleMotionEvent(defs.Event{
			Kind:        kind,
			SubjectKind: defs.EventSubjectKindCamera,
			SubjectID:   cameraID,
		})
	}
	require.Empty(t, emitter.snapshot())
}

// TestController_MultipleCamerasIndependent: independent cooldown
// timers per camera; one camera ending doesn't end the other.
func TestController_MultipleCamerasIndependent(t *testing.T) {
	emitter := &fakeEmitter{}
	camA := "cam-A"
	camB := "cam-B"

	configs := map[string]MotionConfig{camA: cfg(true), camB: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{
		camA: defs.RecordingPolicyModeMotion,
		camB: defs.RecordingPolicyModeMotion,
	}

	var nowMu sync.Mutex
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	c.SetNowFn(nowFn)
	defer c.Close()

	var timers []*fakeTimer
	var mu sync.Mutex
	c.SetTimerFactory(fakeTimerFactory(&timers, &mu))

	c.HandleMotionEvent(motionEvent(camA))
	c.HandleMotionEvent(motionEvent(camB))

	require.True(t, c.IsActive(camA))
	require.True(t, c.IsActive(camB))
	require.Equal(t, 2, c.ActiveCount())
	require.Len(t, timers, 2)

	// Fire only camA's timer (which is timers[0] by registration order).
	advance(DefaultCooldown + 50*time.Millisecond)
	timers[0].Fire()

	require.False(t, c.IsActive(camA))
	require.True(t, c.IsActive(camB))
}

// TestController_StaleTimerFireAfterReset: a timer that fires after a
// Reset has extended its expiry should no-op (no spurious motion_ended).
func TestController_StaleTimerFireAfterReset(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-8"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	var nowMu sync.Mutex
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	c.SetNowFn(nowFn)
	defer c.Close()

	var timers []*fakeTimer
	var mu sync.Mutex
	c.SetTimerFactory(fakeTimerFactory(&timers, &mu))

	c.HandleMotionEvent(motionEvent(cameraID))
	// Second event extends the window before any cooldown expiry.
	advance(1 * time.Second)
	c.HandleMotionEvent(motionEvent(cameraID))

	// Advance to a point that's past the *original* expiry but before
	// the *extended* expiry.
	advance(DefaultCooldown - 2*time.Second)

	// Now fire the timer (simulating a stale fire that happens to land
	// here despite Reset). The controller's onCooldownExpired re-checks
	// expiresAt > now and should skip emitting motion_ended.
	require.Len(t, timers, 1)
	timers[0].Fire()

	require.True(t, c.IsActive(cameraID),
		"camera should still be active because the extended window hasn't elapsed")
	require.Empty(t, emitter.byKind("recording.motion_ended"),
		"stale timer fire under an active extended window must not emit motion_ended")
}

// TestController_LocalFutureSourceLogsButFiresThroughONVIF: when
// motion_config.source = local_future, the controller still consumes
// ONVIF-emitted motion_detected events (with a Warn log) so the
// system degrades gracefully if local-CV isn't yet implemented.
func TestController_LocalFutureSourceLogsButFiresThroughONVIF(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-9"

	c0 := cfg(true)
	c0.Source = MotionConfigSourceLocalFuture
	configs := map[string]MotionConfig{cameraID: c0}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)
	defer c.Close()

	c.HandleMotionEvent(motionEvent(cameraID))
	require.Len(t, emitter.byKind("recording.motion_started"), 1,
		"local_future source must still process incoming motion events as fallback")
}

// TestController_CloseStopsTimers: Close cancels every per-camera
// timer and clears the active map.
func TestController_CloseStopsTimers(t *testing.T) {
	emitter := &fakeEmitter{}
	cameraID := "cam-10"

	configs := map[string]MotionConfig{cameraID: cfg(true)}
	policies := map[string]defs.RecordingPolicyMode{cameraID: defs.RecordingPolicyModeMotion}

	c := NewController(
		nil, emitter,
		func(id string) (MotionConfig, bool) { v, ok := configs[id]; return v, ok },
		func(id string) (defs.RecordingPolicyMode, bool) { v, ok := policies[id]; return v, ok },
		func() string { return "tenant-test" },
	)

	var timers []*fakeTimer
	var mu sync.Mutex
	c.SetTimerFactory(fakeTimerFactory(&timers, &mu))

	c.HandleMotionEvent(motionEvent(cameraID))
	require.True(t, c.IsActive(cameraID))

	c.Close()
	require.False(t, c.IsActive(cameraID))
	require.Equal(t, 0, c.ActiveCount())
}
