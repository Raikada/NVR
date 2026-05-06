// Local CV-based motion detector — SCAFFOLD ONLY in v1.
//
// TODO: future slice — full local motion detection.
//
// In v1 the recorder relies on ONVIF for motion signals: the camera's
// own analytics rule fires events, the recorder consumes them via
// internal/onvif's PullPoint manager, and they translate to canonical
// camera.motion_detected Events through the table in
// internal/onvif/event_topics.go.
//
// Cameras that don't speak ONVIF (RPi-camera, RTMP-publisher cameras,
// publisher-based ingests, generic RTSP cameras without analytics
// rules) need a fallback: the recorder samples decoded frames and
// runs a CV pipeline (background subtraction → contour detection →
// area thresholding) to produce its own motion events. That work is
// deliberately deferred:
//
//   - It touches the media pipeline (frame access from the decoder
//     output), which AGENTS.md §7 forbids without explicit request.
//   - It needs a CV dependency (gocv / OpenCV bindings) that
//     AGENTS.md §8 requires justifying with a one-paragraph rationale.
//   - The CPU profile of background subtraction on every camera is
//     non-trivial and benefits from a system-level decision (which
//     resolution to subsample, what frame interval to drop to, whether
//     to fall back to mediapipe / lighter-weight detectors).
//
// Wave 4 ships the architecture so the future slice can drop in
// implementation behind the LocalDetector interface without rewiring
// callers. motion_config.source = "local_future" is the operator-
// visible knob that signals "I want local detection when it's
// available"; the controller currently logs a warning and continues
// to consume ONVIF events, falling back gracefully when neither
// source is producing.

package motion

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// LocalDetector is the interface a future CV-based detector
// implements. The single Run method takes a context, a per-camera
// configuration, and a callback the detector invokes whenever it
// produces a synthetic motion event. The callback is the seam back
// into the controller's HandleMotionEvent pipeline so local-source
// events fold into the same canonical Event stream as ONVIF-source
// events.
//
// Implementations are responsible for:
//
//   - Pulling decoded frames from the recorder's stream pipeline.
//   - Honoring the ROI when set.
//   - Honoring the schedule when set.
//   - Throttling synthetic events so the controller's cooldown can
//     debounce them (a CV detector that fires on every frame is
//     wasteful; once per ~250ms is plenty for human-scale motion).
type LocalDetector interface {
	// Run blocks until the context is cancelled, sampling frames and
	// firing motionFn whenever motion is detected. cameraID is the
	// canonical Camera UUID; cfg is the live MotionConfig. motionFn
	// emits a synthetic camera.motion_detected event into the
	// recorder's canonical Event stream.
	Run(ctx context.Context, cameraID string, cfg MotionConfig, emit LocalMotionEmitFn) error
}

// LocalMotionEmitFn is the callback shape the LocalDetector invokes
// when it observes motion. The detector supplies a partial
// EventInput; the wrapping recorder code stamps tenant_id and
// recording_server_id, runs it through the controller, and lands it
// in the EventStore.
type LocalMotionEmitFn func(in defs.EventInput)

// NopLocalDetector is the v1 placeholder. Run blocks on the supplied
// context's Done channel and returns when the context is cancelled.
// No frames are processed; no events are emitted. Wired in as the
// default implementation so motion_config.source = "local_future"
// degrades cleanly to "no local source — see canonical-divergences
// motion_config entry for the future-slice plan."
type NopLocalDetector struct{}

// Run on NopLocalDetector waits for ctx.Done. Per the comment block
// at the top of this file, this is a deliberate stub; the recorder
// never installs a NopLocalDetector goroutine for a live camera in v1
// because the controller never enters the local_future branch (it
// logs a warning and falls back to ONVIF).
func (NopLocalDetector) Run(ctx context.Context, _ string, _ MotionConfig, _ LocalMotionEmitFn) error {
	<-ctx.Done()
	return ctx.Err()
}

// Compile-time assertion that NopLocalDetector satisfies
// LocalDetector. Future detector implementations should add a similar
// assertion next to their type so a refactor of the interface surface
// fails the build at the implementation, not at the call site.
var _ LocalDetector = NopLocalDetector{}
