// Package api: Phase A B6 — per-camera stream stats + per-user UI
// preferences.
//
//	GET /v1/cameras/:id/stats   (camera.read)   raw path counters; the
//	                                            client derives rates
//	                                            from deltas between polls
//	GET /v1/me/prefs            (authenticated) opaque client JSON
//	PUT /v1/me/prefs            (authenticated) merge-upsert by key
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/rbac"
)

func (a *API) registerV1StatsPrefs(r gin.IRouter) {
	r.GET("/cameras/:id/stats", rbac.RequirePerm(rbac.PermCameraRead, a.auditEmitter()), a.onV1CameraStats)
	// Prefs are permission-free (any authenticated account owns its own
	// prefs), mirroring /auth/password.
	r.GET("/me/prefs", a.onV1MePrefsGet)
	r.PUT("/me/prefs", a.onV1MePrefsPut)
}

func (a *API) onV1CameraStats(ctx *gin.Context) {
	if a.PathManager == nil || interfaceIsEmpty(a.PathManager) {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("path manager not available"))
		return
	}
	camID := ctx.Param("id")

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	// Camera id → path name: store row first (canonical since the F1
	// fix), conf reverse-lookup as fallback.
	pathName := ""
	if a.Store != nil {
		if cam, err := a.Store.Cameras.GetByID(ctx.Request.Context(), camID); err == nil {
			pathName = cam.Name
		}
	}
	if pathName == "" && c != nil {
		if name, ok := pathNameFromCameraID(c.Paths, camID); ok {
			pathName = name
		}
	}
	if pathName == "" {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera not found"))
		return
	}

	p, err := a.PathManager.APIPathsGet(pathName)
	if err != nil || p == nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera has no active path"))
		return
	}

	tracks := make([]string, 0, len(p.Tracks2))
	for _, tr := range p.Tracks2 {
		tracks = append(tracks, string(tr.Codec))
	}
	out := gin.H{
		"online":          p.Online,
		"bytes_received":  p.InboundBytes,
		"bytes_sent":      p.OutboundBytes,
		"frames_in_error": p.InboundFramesInError,
		"readers":         len(p.Readers),
		"tracks":          tracks,
		"ts":              time.Now().UTC(),
	}
	if p.OnlineTime != nil {
		out["online_since"] = p.OnlineTime.UTC()
	}
	ctx.JSON(http.StatusOK, out)
}

func (a *API) onV1MePrefsGet(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	sub := a.localUserSub(ctx)
	if sub == "" {
		a.writeError(ctx, http.StatusUnauthorized, errors.New("authentication error"))
		return
	}
	prefs, err := a.Store.UserPrefs.List(ctx.Request.Context(), sub)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make(map[string]json.RawMessage, len(prefs))
	for k, v := range prefs {
		if json.Valid([]byte(v)) {
			out[k] = json.RawMessage(v)
		}
	}
	ctx.JSON(http.StatusOK, out)
}

func (a *API) onV1MePrefsPut(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	sub := a.localUserSub(ctx)
	if sub == "" {
		a.writeError(ctx, http.StatusUnauthorized, errors.New("authentication error"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal(body, &incoming); err != nil {
		a.writeError(ctx, http.StatusBadRequest, errors.New("body must be a JSON object of key → value"))
		return
	}
	for k, v := range incoming {
		if err := a.Store.UserPrefs.Set(ctx.Request.Context(), sub, k, string(v)); err != nil {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
	}
	a.writeOK(ctx)
}
