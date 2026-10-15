// Wave 4 motion-controller wiring. Connects internal/motion's
// Controller to the recorder's existing canonical-event pipeline:
//
//   - The motion controller subscribes to EventStore via Subscribe;
//     every camera.motion_detected event routed through the store
//     fans out to HandleMotionEvent.
//   - The MotionConfig + RecordingPolicy resolvers read from the
//     live a.Conf under a.mutex.
//   - recording.motion_started / motion_ended emissions flow back
//     into the same EventStore so /v1/events surfaces the lifecycle
//     end-to-end.
//
// One controller per recorder process. Constructed lazily by
// core.go's createResources hook; package-level reference lives here
// so tests can substitute and so a future API surface that wants to
// query controller state (active-camera count, etc.) doesn't have to
// thread a reference through.

package api //nolint:revive

import (
	"sync"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/motion"
)

var (
	motionMu         sync.RWMutex
	motionController *motion.Controller
)

// SetMotionController stores a process-wide reference to the active
// motion controller. core.go calls this once during startup; tests
// may replace it.
func SetMotionController(c *motion.Controller) {
	motionMu.Lock()
	defer motionMu.Unlock()
	motionController = c
}

// GetMotionController returns the active controller. Returns nil
// when nothing has been wired yet (tests, pre-core-init paths).
func GetMotionController() *motion.Controller {
	motionMu.RLock()
	defer motionMu.RUnlock()
	return motionController
}

// MotionConfigResolverForAPI builds a resolver that reads from a's
// live Conf.MotionConfigs map. Wraps the lookup under a.mutex.RLock
// so the resolver is safe to invoke from off-thread (the controller's
// HandleMotionEvent fires from the event subscriber, not from a
// gin handler).
func MotionConfigResolverForAPI(a *API) motion.MotionConfigResolver {
	return func(cameraID string) (motion.MotionConfig, bool) {
		a.mutex.RLock()
		defer a.mutex.RUnlock()
		if a.Conf == nil || a.Conf.MotionConfigs == nil {
			return motion.MotionConfig{}, false
		}
		mc, ok := a.Conf.MotionConfigs[cameraID]
		if !ok || mc == nil {
			return motion.MotionConfig{}, false
		}
		return motion.FromConfigConfig(mc), true
	}
}

// PolicyModeResolverForAPI builds a resolver that walks a's live
// Conf.Paths to find the camera's path, reads its RecordingPolicyID,
// then looks up the policy's mode in Conf.RecordingPolicies.
//
// Returns "" + false when the camera has no path, no policy id, or
// the policy id resolves to a missing entry. The motion controller
// treats those cases as "ignore the event."
func PolicyModeResolverForAPI(a *API) motion.PolicyModeResolver {
	return func(cameraID string) (defs.RecordingPolicyMode, bool) {
		a.mutex.RLock()
		defer a.mutex.RUnlock()
		if a.Conf == nil {
			return "", false
		}
		pathName, ok := pathNameFromCameraID(a.Conf.Paths, cameraID)
		if !ok {
			return "", false
		}
		path := a.Conf.Paths[pathName]
		if path == nil {
			return "", false
		}
		policyID := path.RecordingPolicyID
		if policyID == "" {
			return "", false
		}
		if a.Conf.RecordingPolicies == nil {
			return "", false
		}
		policy, ok := a.Conf.RecordingPolicies[policyID]
		if !ok || policy == nil {
			return "", false
		}
		return defs.RecordingPolicyMode(policy.Mode), true
	}
}

// TenantIDFnForAPI returns a TenantIDFn that reads from a's live
// Conf.TenantID under the lock. Mirrors a.tenantID() but is exposed
// because motion lives in a separate package.
func TenantIDFnForAPI(a *API) motion.TenantIDFn {
	return func() string {
		a.mutex.RLock()
		defer a.mutex.RUnlock()
		if a.Conf == nil {
			return ""
		}
		return ""
	}
}

// WireMotionControllerForAPI constructs a controller wired to a's
// Conf and registers it as a subscriber on the package-wide
// EventStore. Idempotent — calling twice replaces the previous
// controller. Returns the constructed controller so the caller can
// own its Close lifecycle.
func WireMotionControllerForAPI(a *API, log logger.Writer) *motion.Controller {
	c := motion.NewController(
		log,
		DefaultEventStore(),
		MotionConfigResolverForAPI(a),
		PolicyModeResolverForAPI(a),
		TenantIDFnForAPI(a),
	)
	DefaultEventStore().Subscribe(c.HandleMotionEvent)
	SetMotionController(c)
	return c
}
