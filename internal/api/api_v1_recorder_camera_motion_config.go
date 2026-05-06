// Package api: /v1/recorder/cameras/{id}/motion-config — the per-
// camera motion-detection configuration escape hatch (Wave 4).
//
// motion_config is recorder-local in v1: per the Wave 4
// canonical-divergences entry, the canonical Camera entity does not
// carry these fields. This handler reads + writes the conf-package
// MotionConfigs map keyed by canonical Camera UUID. A future slice
// elevates the shape to canonical Camera.motion_config when MS
// canonical Camera (slice 4-B) gains support.
//
// Permission gates:
//
//   - GET requires recorder_config.read.
//   - PATCH requires recorder_config.manage and goes through
//     guardAdminAction (audit-buffer degraded mode shed).
//
// Wire shape: motion.MotionConfig (snake_case canonical) — see
// internal/motion/config.go for field documentation. Wave 4 also
// emits a "Test Motion" synthetic-event endpoint (POST .../motion-
// config/test) so operators can verify the controller wiring without
// a camera physically moving.
package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/motion"
)

// motionConfigGet serves GET /v1/recorder/cameras/{id}/motion-config.
// Returns the operator-set config when present; the v1 default
// otherwise. Tenant id is stamped on the response per the canonical
// /v1 convention even though the persistence shape (conf.MotionConfigConfig)
// doesn't carry one — the recorder is single-tenant.
func (a *API) onV1RecorderCameraMotionConfigGet(ctx *gin.Context) {
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	if _, ok := pathNameFromCameraID(c.Paths, cameraID); !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	var mc motion.MotionConfig
	if c.MotionConfigs != nil {
		if persisted, ok := c.MotionConfigs[cameraID]; ok {
			mc = motion.FromConfigConfig(persisted)
		} else {
			mc = motion.DefaultMotionConfig()
		}
	} else {
		mc = motion.DefaultMotionConfig()
	}

	resp := struct {
		motion.MotionConfig
		TenantID string `json:"tenant_id,omitempty"`
		CameraID string `json:"camera_id"`
	}{
		MotionConfig: mc,
		TenantID:     c.TenantID,
		CameraID:     cameraID,
	}
	ctx.JSON(http.StatusOK, &resp)
}

// motionConfigPatchRequest is the wire shape for PATCH. All fields
// are optional; unset fields preserve the current persisted value
// (or the default if nothing is persisted yet).
type motionConfigPatchRequest struct {
	Enabled     *bool                  `json:"enabled,omitempty"`
	Source      *string                `json:"source,omitempty"`
	Sensitivity *int                   `json:"sensitivity,omitempty"`
	ROI         *motion.MotionROI      `json:"roi,omitempty"`
	UnsetROI    bool                   `json:"unset_roi,omitempty"`
	Schedule    *motion.MotionSchedule `json:"schedule,omitempty"`
	UnsetSchedule bool                 `json:"unset_schedule,omitempty"`
	CooldownMS  *int                   `json:"cooldown_ms,omitempty"`
}

// onV1RecorderCameraMotionConfigPatch serves PATCH .../motion-config.
// Merges the supplied fields onto the persisted config (or the
// default if absent), validates the merged result, persists, and
// emits config.applied.
func (a *API) onV1RecorderCameraMotionConfigPatch(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req motionConfigPatchRequest
	if len(body) > 0 {
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
			a.writeError(ctx, http.StatusBadRequest, err)
			return
		}
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	if _, ok := pathNameFromCameraID(a.Conf.Paths, cameraID); !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	// Start from the persisted config or the v1 default.
	var current motion.MotionConfig
	if a.Conf.MotionConfigs != nil {
		if existing, ok := a.Conf.MotionConfigs[cameraID]; ok {
			current = motion.FromConfigConfig(existing)
		} else {
			current = motion.DefaultMotionConfig()
		}
	} else {
		current = motion.DefaultMotionConfig()
	}

	// Apply the patch.
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.Source != nil {
		current.Source = motion.MotionConfigSource(*req.Source)
	}
	if req.Sensitivity != nil {
		current.Sensitivity = *req.Sensitivity
	}
	if req.UnsetROI {
		current.ROI = nil
	} else if req.ROI != nil {
		roi := *req.ROI
		current.ROI = &roi
	}
	if req.UnsetSchedule {
		current.Schedule = nil
	} else if req.Schedule != nil {
		sched := *req.Schedule
		// Defensive copy so a later mutation by the caller doesn't
		// race the persisted shape.
		windows := make([]motion.MotionScheduleWindow, len(sched.Windows))
		for i, w := range sched.Windows {
			days := append([]string{}, w.Days...)
			windows[i] = motion.MotionScheduleWindow{Days: days, Start: w.Start, End: w.End}
		}
		current.Schedule = &motion.MotionSchedule{Timezone: sched.Timezone, Windows: windows}
	}
	if req.CooldownMS != nil {
		current.CooldownMS = *req.CooldownMS
	}

	current.Normalize()
	if err := current.Validate(); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	newConf := a.Conf.Clone()
	if newConf.MotionConfigs == nil {
		newConf.MotionConfigs = make(map[string]*conf.MotionConfigConfig)
	}
	newConf.MotionConfigs[cameraID] = motion.ToConfigConfig(current)

	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.emitConfigAppliedLocked("camera", cameraID, "patch", map[string]string{
		"camera_id":  cameraID,
		"surface":    "/v1/recorder/cameras/{id}/motion-config",
		"motion_on":  boolStr(current.Enabled),
		"source":     string(current.Source),
	})

	a.writeOK(ctx)
}

// motionConfigTestRequest is the body for the synthetic-event
// endpoint. Empty body is fine — a synthetic event with no extra
// metadata fires.
type motionConfigTestRequest struct {
	// Topic carries through to the synthetic event's onvif_topic
	// attribute so a UI integration test can distinguish synthetic
	// from real events. Defaults to "tns1:Test/Synthetic" when omitted.
	Topic string `json:"topic,omitempty"`
}

// onV1RecorderCameraMotionConfigTest emits a synthetic
// camera.motion_detected event for the camera. Operators use this
// from the SPA Motion tab to verify the controller wiring without
// physically moving in front of the camera. recorder_config.manage
// gates because firing fake events at the canonical event stream is
// an admin-level action — not for general operators.
//
// The synthetic event carries an attribute "synthetic": "true" so
// downstream consumers can distinguish it from real ONVIF-sourced
// events.
func (a *API) onV1RecorderCameraMotionConfigTest(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req motionConfigTestRequest
	if len(body) > 0 {
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
			a.writeError(ctx, http.StatusBadRequest, err)
			return
		}
	}
	if req.Topic == "" {
		req.Topic = "tns1:Test/Synthetic"
	}

	a.mutex.RLock()
	tenantID := ""
	if a.Conf != nil {
		tenantID = a.Conf.TenantID
	}
	if _, ok := pathNameFromCameraID(a.Conf.Paths, cameraID); !ok {
		a.mutex.RUnlock()
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}
	a.mutex.RUnlock()

	defaultEventStore().Publish(defs.EventInput{
		Kind:        "camera.motion_detected",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "synthetic motion event (operator-triggered test)",
		Attributes: map[string]string{
			"synthetic":   "true",
			"onvif_topic": req.Topic,
		},
		OccurredAt: time.Now().UTC(),
	}, "", tenantID, "")

	a.writeOK(ctx)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
