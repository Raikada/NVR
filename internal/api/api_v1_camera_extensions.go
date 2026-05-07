// Package api: Phase 5 additions to the existing /v1/cameras handlers.
//
// Adds:
//   - PUT  /v1/cameras/:id/credentials       (camera.credentials.write)
//   - POST /v1/cameras/:id/probe             (camera.probe; 501 stub)
//   - GET  /v1/cameras/:id/health            (camera.read)
//   - GET  /v1/cameras/:id/recording-state   (camera.read)
//
// The base CRUD handlers in api_v1_cameras.go remain rewired to the
// path manager + Conf; per Phase 5 task 5.2 we add the additive
// surfaces that exercise the new Phase 2/3 stack (cameracred + cameras
// service + camera_health + schedule.Resolver) without disturbing the
// existing path-manager-backed CRUD.
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1CameraExtensions(r gin.IRouter) {
	r.PUT("/cameras/:id/credentials", rbac.RequirePerm(rbac.PermCameraCredentialsWrite, a.auditEmitter()), a.onV1CameraCredentialsPut)
	r.POST("/cameras/:id/probe", rbac.RequirePerm(rbac.PermCameraProbe, a.auditEmitter()), a.onV1CameraProbe)
	r.GET("/cameras/:id/health", rbac.RequirePerm(rbac.PermCameraRead, a.auditEmitter()), a.onV1CameraHealth)
	r.GET("/cameras/:id/recording-state", rbac.RequirePerm(rbac.PermCameraRead, a.auditEmitter()), a.onV1CameraRecordingState)
}

type cameraCredentialsRequest struct {
	RTSPUsername  string `json:"rtsp_username"`
	RTSPPassword  string `json:"rtsp_password"`
	OnvifUsername string `json:"onvif_username"`
	OnvifPassword string `json:"onvif_password"`
}

type cameraCredentialsResponse struct {
	Username    string    `json:"username"`
	PasswordSet bool      `json:"password_set"`
	RotatedAt   time.Time `json:"rotated_at"`
}

func (a *API) onV1CameraCredentialsPut(ctx *gin.Context) {
	if a.CamerasService == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("cameras service not wired"))
		return
	}
	id := ctx.Param("id")
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req cameraCredentialsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.RTSPUsername == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("rtsp_username is required"))
		return
	}
	if err := a.CamerasService.SetCredentials(ctx.Request.Context(), id, req.RTSPUsername, req.RTSPPassword); err != nil {
		if errors.Is(err, store.ErrCameraNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "camera.credentials_rotated", "camera", id, nil)
	creds, err := a.Store.CameraCredentials.Get(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	ctx.JSON(http.StatusOK, cameraCredentialsResponse{
		Username:    creds.Username,
		PasswordSet: len(creds.PasswordCiphertext) > 0,
		RotatedAt:   creds.RotatedAt,
	})
}

// onV1CameraProbe is a foundation-phase stub: real RTSP/ONVIF probing
// lands in sub-project 2. Returns 501.
func (a *API) onV1CameraProbe(ctx *gin.Context) {
	a.writeError(ctx, http.StatusNotImplemented, errors.New("camera probe is not implemented yet"))
}

type cameraHealthResponse struct {
	CameraID            string    `json:"camera_id"`
	RTSPState           string    `json:"rtsp_state"`
	LastKeyframeAt      time.Time `json:"last_keyframe_at,omitempty"`
	LastEventAt         time.Time `json:"last_event_at,omitempty"`
	LastSeenAt          time.Time `json:"last_seen_at,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastError           string    `json:"last_error,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (a *API) onV1CameraHealth(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	row, err := a.Store.CameraHealth.Get(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrCameraHealthNotFound) {
			ctx.JSON(http.StatusOK, &cameraHealthResponse{CameraID: id, RTSPState: "unknown"})
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	ctx.JSON(http.StatusOK, &cameraHealthResponse{
		CameraID:            row.CameraID,
		RTSPState:           row.RTSPState,
		LastKeyframeAt:      row.LastKeyframeAt,
		LastEventAt:         row.LastEventAt,
		LastSeenAt:          row.LastSeenAt,
		ConsecutiveFailures: row.ConsecutiveFailures,
		LastError:           row.LastError,
		UpdatedAt:           row.UpdatedAt,
	})
}

type cameraRecordingStateResponse struct {
	Active bool      `json:"active"`
	Mode   string    `json:"mode"`
	Reason string    `json:"reason,omitempty"`
	Until  time.Time `json:"until,omitempty"`
}

func (a *API) onV1CameraRecordingState(ctx *gin.Context) {
	if a.ScheduleResolver == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("schedule resolver not wired"))
		return
	}
	id := ctx.Param("id")
	res, err := a.ScheduleResolver.IsActive(ctx.Request.Context(), id, time.Now())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	ctx.JSON(http.StatusOK, &cameraRecordingStateResponse{
		Active: res.On,
		Mode:   res.Mode,
		Reason: res.Reason,
		Until:  res.Until,
	})
}
