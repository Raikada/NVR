// Package api: /v1/camera-groups handlers per Phase 5 Task 5.3.
//
// Camera groups are an optional grouping for cameras (lobbies, parking,
// etc.). Backed by store.CameraGroupsRepo. Permissions namespaced under
// camera_group.* (rbac.PermCameraGroup{List,Read,Create,Update,Delete}).
//
// DELETE returns 409 if any camera still references the group; the
// caller must reassign cameras first. The store does not cascade — its
// FK is nullable and would NULL out the cameras.group_id, which the API
// surface treats as a programming error rather than the operator's
// intent.
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

// cameraGroupResponse is the on-wire shape of camera_groups rows.
type cameraGroupResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayOrder int    `json:"display_order"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func cameraGroupToWire(g *store.CameraGroup) cameraGroupResponse {
	return cameraGroupResponse{
		ID:           g.ID,
		Name:         g.Name,
		DisplayOrder: g.DisplayOrder,
		CreatedAt:    g.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    g.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

type cameraGroupCreateRequest struct {
	Name         string `json:"name"`
	DisplayOrder int    `json:"display_order"`
}

type cameraGroupUpdateRequest struct {
	Name         *string `json:"name"`
	DisplayOrder *int    `json:"display_order"`
}

func (a *API) registerV1CameraGroups(r gin.IRouter) {
	r.GET("/camera-groups", rbac.RequirePerm(rbac.PermCameraGroupList, a.auditEmitter()), a.onV1CameraGroupsList)
	r.GET("/camera-groups/:id", rbac.RequirePerm(rbac.PermCameraGroupRead, a.auditEmitter()), a.onV1CameraGroupsGet)
	r.POST("/camera-groups", rbac.RequirePerm(rbac.PermCameraGroupCreate, a.auditEmitter()), a.onV1CameraGroupsCreate)
	r.PATCH("/camera-groups/:id", rbac.RequirePerm(rbac.PermCameraGroupUpdate, a.auditEmitter()), a.onV1CameraGroupsUpdate)
	r.DELETE("/camera-groups/:id", rbac.RequirePerm(rbac.PermCameraGroupDelete, a.auditEmitter()), a.onV1CameraGroupsDelete)
}

func (a *API) onV1CameraGroupsList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	groups, err := a.Store.CameraGroups.List(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make([]cameraGroupResponse, 0, len(groups))
	for _, g := range groups {
		out = append(out, cameraGroupToWire(g))
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

func (a *API) onV1CameraGroupsGet(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	g, err := a.Store.CameraGroups.GetByID(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera group not found"))
		return
	}
	resp := cameraGroupToWire(g)
	ctx.JSON(http.StatusOK, &resp)
}

func (a *API) onV1CameraGroupsCreate(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req cameraGroupCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	g := &store.CameraGroup{
		ID:           uuid.NewString(),
		Name:         req.Name,
		DisplayOrder: req.DisplayOrder,
	}
	if err := a.Store.CameraGroups.Insert(ctx.Request.Context(), g); err != nil {
		if errors.Is(err, store.ErrCameraGroupExists) {
			a.writeError(ctx, http.StatusConflict, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "camera_group.created", "camera_group", g.ID, map[string]string{
		"name": g.Name,
	})
	resp := cameraGroupToWire(g)
	ctx.JSON(http.StatusCreated, &resp)
}

func (a *API) onV1CameraGroupsUpdate(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req cameraGroupUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	existing, err := a.Store.CameraGroups.GetByID(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera group not found"))
		return
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.DisplayOrder != nil {
		existing.DisplayOrder = *req.DisplayOrder
	}
	if err := a.Store.CameraGroups.Update(ctx.Request.Context(), existing); err != nil {
		if errors.Is(err, store.ErrCameraGroupExists) {
			a.writeError(ctx, http.StatusConflict, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "camera_group.updated", "camera_group", id, nil)
	resp := cameraGroupToWire(existing)
	ctx.JSON(http.StatusOK, &resp)
}

func (a *API) onV1CameraGroupsDelete(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	// Refuse delete if any camera still references this group.
	cams, err := a.Store.Cameras.List(ctx.Request.Context(), store.ListCamerasFilter{GroupID: id, Limit: 1})
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if len(cams) > 0 {
		a.writeError(ctx, http.StatusConflict, errors.New("camera group still has cameras; reassign or delete them first"))
		return
	}
	if err := a.Store.CameraGroups.Delete(ctx.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrCameraGroupNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "camera_group.deleted", "camera_group", id, nil)
	ctx.Status(http.StatusNoContent)
}

// (emitMutationAudit + clientIP live in phase5_helpers.go)
