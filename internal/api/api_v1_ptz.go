// Package api: Phase A B5 — PTZ control + camera reboot.
//
//	POST /v1/cameras/:id/ptz/move               (camera.ptz)
//	POST /v1/cameras/:id/ptz/stop               (camera.ptz)
//	POST /v1/cameras/:id/ptz/home               (camera.ptz)
//	GET  /v1/cameras/:id/ptz/presets            (camera.ptz)
//	POST /v1/cameras/:id/ptz/presets            (camera.ptz)   save current position
//	POST /v1/cameras/:id/ptz/presets/:tok/goto  (camera.ptz)
//	POST /v1/cameras/:id/reboot                 (camera.reboot)
//
// Presets live camera-side (ONVIF-native); the recorder stores
// nothing. The PTZ XAddr is the camera's device-service URL from the
// capability probe; credentials come from the vault.
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

// PTZController abstracts onvif.PTZClient for tests.
type PTZController interface {
	ContinuousMove(ctx context.Context, profileToken string, pan, tilt, zoom float64) error
	Stop(ctx context.Context, profileToken string) error
	GotoHome(ctx context.Context, profileToken string) error
	GetPresets(ctx context.Context, profileToken string) ([]onvif.PTZPreset, error)
	GotoPreset(ctx context.Context, profileToken, presetToken string) error
	SetPreset(ctx context.Context, profileToken, name string) (string, error)
}

func (a *API) registerV1PTZ(r gin.IRouter) {
	ptz := rbac.RequirePerm(rbac.PermCameraPTZ, a.auditEmitter())
	r.POST("/cameras/:id/ptz/move", ptz, a.onV1PTZMove)
	r.POST("/cameras/:id/ptz/stop", ptz, a.onV1PTZStop)
	r.POST("/cameras/:id/ptz/home", ptz, a.onV1PTZHome)
	r.GET("/cameras/:id/ptz/presets", ptz, a.onV1PTZPresetsList)
	r.POST("/cameras/:id/ptz/presets", ptz, a.onV1PTZPresetSave)
	r.POST("/cameras/:id/ptz/presets/:token/goto", ptz, a.onV1PTZPresetGoto)
	r.POST("/cameras/:id/reboot", rbac.RequirePerm(rbac.PermCameraReboot, a.auditEmitter()), a.onV1CameraReboot)
}

// ptzTarget resolves a camera to a controller + profile token, or
// writes the error (404 unknown, 409 not PTZ-capable / no xaddr / no
// creds) and returns ok=false.
func (a *API) ptzTarget(ctx *gin.Context) (PTZController, string, bool) {
	if a.CamerasService == nil || a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("cameras stack not wired"))
		return nil, "", false
	}
	id := ctx.Param("id")
	cam, err := a.CamerasService.Get(ctx.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrCameraNotFound) {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera not found"))
		return nil, "", false
	}
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return nil, "", false
	}
	caps, err := a.Store.CameraCapabilities.Get(ctx.Request.Context(), id)
	if err != nil || !caps.HasPTZ {
		a.writeError(ctx, http.StatusConflict, errors.New("camera is not PTZ-capable (probe it first?)"))
		return nil, "", false
	}
	if cam.OnvifXAddr == "" {
		a.writeError(ctx, http.StatusConflict, errors.New("camera has no onvif_xaddr"))
		return nil, "", false
	}
	username, password, err := a.CamerasService.PlaintextCredentials(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusConflict, fmt.Errorf("camera has no stored credentials: %w", err))
		return nil, "", false
	}
	profile := caps.SelectedProfileToken
	if profile == "" {
		profile = "Profile000"
	}
	return a.newPTZClient(cam.OnvifXAddr, username, password), profile, true
}

// newPTZClient builds the real client unless a test injected a fake.
func (a *API) newPTZClient(xaddr, username, password string) PTZController {
	if a.NewPTZClientFn != nil {
		return a.NewPTZClientFn(xaddr, username, password)
	}
	return &onvif.PTZClient{XAddr: xaddr, Username: username, Password: password}
}

type ptzMoveRequest struct {
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	Zoom float64 `json:"zoom"`
}

func (a *API) onV1PTZMove(ctx *gin.Context) {
	pc, profile, ok := a.ptzTarget(ctx)
	if !ok {
		return
	}
	var req ptzMoveRequest
	_ = ctx.ShouldBindJSON(&req)
	for _, v := range []float64{req.Pan, req.Tilt, req.Zoom} {
		if v < -1 || v > 1 {
			a.writeError(ctx, http.StatusBadRequest, errors.New("pan/tilt/zoom velocities must be in -1..1"))
			return
		}
	}
	if err := pc.ContinuousMove(ctx.Request.Context(), profile, req.Pan, req.Tilt, req.Zoom); err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	a.writeOK(ctx)
}

func (a *API) onV1PTZStop(ctx *gin.Context) {
	pc, profile, ok := a.ptzTarget(ctx)
	if !ok {
		return
	}
	if err := pc.Stop(ctx.Request.Context(), profile); err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	a.writeOK(ctx)
}

func (a *API) onV1PTZHome(ctx *gin.Context) {
	pc, profile, ok := a.ptzTarget(ctx)
	if !ok {
		return
	}
	if err := pc.GotoHome(ctx.Request.Context(), profile); err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	a.writeOK(ctx)
}

func (a *API) onV1PTZPresetsList(ctx *gin.Context) {
	pc, profile, ok := a.ptzTarget(ctx)
	if !ok {
		return
	}
	presets, err := pc.GetPresets(ctx.Request.Context(), profile)
	if err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": presets})
}

type ptzPresetSaveRequest struct {
	Name string `json:"name"`
}

func (a *API) onV1PTZPresetSave(ctx *gin.Context) {
	pc, profile, ok := a.ptzTarget(ctx)
	if !ok {
		return
	}
	var req ptzPresetSaveRequest
	_ = ctx.ShouldBindJSON(&req)
	if req.Name == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	token, err := pc.SetPreset(ctx.Request.Context(), profile, req.Name)
	if err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	a.emitMutationAudit(ctx, "camera.ptz_preset_saved", "camera", ctx.Param("id"),
		map[string]string{"preset": token, "name": req.Name})
	ctx.JSON(http.StatusCreated, gin.H{"token": token, "name": req.Name})
}

func (a *API) onV1PTZPresetGoto(ctx *gin.Context) {
	pc, profile, ok := a.ptzTarget(ctx)
	if !ok {
		return
	}
	if err := pc.GotoPreset(ctx.Request.Context(), profile, ctx.Param("token")); err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	a.writeOK(ctx)
}

// onV1CameraReboot asks the camera itself to reboot (ONVIF
// SystemReboot with vault credentials).
func (a *API) onV1CameraReboot(ctx *gin.Context) {
	if a.CamerasService == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("cameras stack not wired"))
		return
	}
	id := ctx.Param("id")
	cam, err := a.CamerasService.Get(ctx.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrCameraNotFound) {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera not found"))
		return
	}
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if cam.OnvifXAddr == "" {
		a.writeError(ctx, http.StatusConflict, errors.New("camera has no onvif_xaddr"))
		return
	}
	username, password, err := a.CamerasService.PlaintextCredentials(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusConflict, fmt.Errorf("camera has no stored credentials: %w", err))
		return
	}
	msg, err := a.rebootCamera(ctx.Request.Context(), cam.OnvifXAddr, username, password)
	if err != nil {
		a.writeError(ctx, http.StatusBadGateway, err)
		return
	}
	a.emitMutationAudit(ctx, "camera.rebooted", "camera", id, nil)
	ctx.JSON(http.StatusOK, gin.H{"message": msg})
}

// rebootCamera is the ONVIF call, injectable for tests.
func (a *API) rebootCamera(ctx context.Context, xaddr, username, password string) (string, error) {
	if a.RebootCameraFn != nil {
		return a.RebootCameraFn(ctx, xaddr, username, password)
	}
	dc := &onvif.DeviceClient{XAddr: xaddr, Username: username, Password: password}
	return dc.SystemReboot(ctx)
}
