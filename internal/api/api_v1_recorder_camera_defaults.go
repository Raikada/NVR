package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/gin-gonic/gin"
)

// recordingPolicyFields enumerates the conf.Path fields that map to a
// canonical RecordingPolicy. Per ADR 0009 §D6, these are explicitly
// excluded from the camera-defaults escape hatch: shared recording
// behavior is controlled via /v1/recording-policies, not via
// camera-defaults. The escape hatch survives only as a recorder-local
// convenience for non-policy defaults (RTSP transport, on-demand
// defaults, RPi-camera defaults, etc.).
//
// Keys are the JSON tag names used on conf.Path.
var recordingPolicyFields = map[string]struct{}{
	"record":                {},
	"recordPath":            {},
	"recordFormat":          {},
	"recordPartDuration":    {},
	"recordMaxPartSize":     {},
	"recordSegmentDuration": {},
	"recordDeleteAfter":     {},
}

// stripPolicyFields removes the recording-policy fields from a
// JSON object representation of a Path / OptionalPath. Used on the GET
// response to hide them and on the PATCH body to reject them. Returns
// the keys that were present so PATCH can surface a 400.
func stripPolicyFields(raw map[string]any) []string {
	var hits []string
	for k := range recordingPolicyFields {
		if _, ok := raw[k]; ok {
			hits = append(hits, k)
			delete(raw, k)
		}
	}
	return hits
}

// onV1RecorderCameraDefaultsGet serves /v1/recorder/camera-defaults —
// the non-policy default fields applied at camera creation. Replaces
// the legacy /v3/config/pathdefaults/get with two changes:
//
//  1. Recording-policy fields (record, recordPath, recordFormat,
//     recordPartDuration, recordMaxPartSize, recordSegmentDuration,
//     recordDeleteAfter) are stripped from the response. Per ADR 0009
//     §D6, the canonical mechanism for shared camera recording
//     behavior is RecordingPolicy at /v1/recording-policies; surfacing
//     the same knobs here would be a parallel write path that violates
//     the "canonical placement is the default" rule.
//  2. The Source URL has its userinfo redacted (matches the existing
//     /v3 behavior; canonical-divergences D4).
//
// Rationale (D6.3): the *concept* of camera-defaults — pre-populating
// fields on newly-created cameras — is a recorder-internal convenience.
// The MS does not need to project default-templates upward; it issues
// fully-specified Camera entities. So the escape hatch persists as a
// recorder-local feature for operators who run the recorder
// stand-alone (pre-MS phase per §D4).
func (a *API) onV1RecorderCameraDefaultsGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	defaults := c.PathDefaults
	defaults.TenantID = ""
	defaults.Source = redactSourceURL(defaults.Source)

	// Round-trip through JSON to drop the policy fields. Doing it on the
	// JSON shape rather than on the Path struct lets us delete fields
	// without forking the conf.Path type — important because conf.Path
	// is shared with the YAML-loader and other call sites.
	body, err := json.Marshal(defaults)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	stripPolicyFields(raw)

	ctx.JSON(http.StatusOK, raw)
}

// onV1RecorderCameraDefaultsPatch handles PATCH /v1/recorder/
// camera-defaults. Recording-policy fields in the body cause a 400 with
// a message pointing to /v1/recording-policies — see the GET handler's
// rationale.
func (a *API) onV1RecorderCameraDefaultsPatch(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	body, ok := a.readTenantScopedBody(ctx)
	if !ok {
		return
	}

	// Inspect the raw body for policy fields *before* decoding into
	// conf.OptionalPath; the OptionalPath shape is reflective and would
	// silently accept the fields even though they shouldn't be set
	// through this endpoint.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		if hits := stripPolicyFields(raw); len(hits) > 0 {
			a.writeError(ctx, http.StatusBadRequest,
				fmt.Errorf("recording-policy fields are not accepted at /v1/recorder/camera-defaults "+
					"(use /v1/recording-policies instead): %v", hits))
			return
		}
	}

	var p conf.OptionalPath
	err := jsonwrapper.Decode(bytes.NewReader(body), &p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	newConf.PatchPathDefaults(&p)

	err = newConf.Validate(nil)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.emitConfigAppliedLocked("server", "", "patch", map[string]string{"surface": "/v1/recorder/camera-defaults"})

	a.writeOK(ctx)
}
