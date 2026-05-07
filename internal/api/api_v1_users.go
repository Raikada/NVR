// Package api: /v1/users handlers per Phase 5 Task 5.8.
//
// Admin-gated user management. Distinguishes from
// /v1/recorder/local-users/me/password (the self-service password
// rotation) by requiring user.* permissions.
//
// DELETE soft-deletes (is_active=false). Hard delete is intentionally
// absent — the audit chain references actor user ids and cannot
// tolerate FK breaks.
//
// Password reset (POST /v1/users/:id/password) sets must_change_password=1
// so the user is forced through the rotation flow on next login.
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1Users(r gin.IRouter) {
	r.GET("/users", rbac.RequirePerm(rbac.PermUserList, a.auditEmitter()), a.onV1UsersList)
	r.GET("/users/:id", rbac.RequirePerm(rbac.PermUserRead, a.auditEmitter()), a.onV1UsersGet)
	r.POST("/users", rbac.RequirePerm(rbac.PermUserCreate, a.auditEmitter()), a.onV1UsersCreate)
	r.PATCH("/users/:id", rbac.RequirePerm(rbac.PermUserUpdate, a.auditEmitter()), a.onV1UsersUpdate)
	r.DELETE("/users/:id", rbac.RequirePerm(rbac.PermUserDelete, a.auditEmitter()), a.onV1UsersDelete)
	r.POST("/users/:id/role", rbac.RequirePerm(rbac.PermUserUpdate, a.auditEmitter()), a.onV1UsersSetRole)
	r.POST("/users/:id/password", rbac.RequirePerm(rbac.PermUserUpdate, a.auditEmitter()), a.onV1UsersResetPassword)
}

type userWire struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"display_name,omitempty"`
	Email              string    `json:"email,omitempty"`
	Role               string    `json:"role"`
	IsActive           bool      `json:"is_active"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
	LastLoginAt        time.Time `json:"last_login_at,omitempty"`
}

func userToWire(u *store.LocalUser) userWire {
	role := string(rbac.RoleViewer)
	// roles table seeds id values "role_admin" / "role_viewer"; the wire
	// shape carries the role name (admin / viewer) for client clarity.
	if u.IsAdmin || u.RoleID == "role_admin" {
		role = string(rbac.RoleAdmin)
	}
	return userWire{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
		Email: u.Email, Role: role,
		IsActive: u.IsActive, MustChangePassword: u.MustChangePassword,
		CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
	}
}

// roleIDForName maps the wire role name (admin/viewer) to the
// roles.id used for FK persistence.
func roleIDForName(role string) string {
	switch role {
	case "admin":
		return "role_admin"
	case "viewer":
		return "role_viewer"
	}
	return ""
}

func (a *API) onV1UsersList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	rows, err := a.Store.LocalUsers.ListAll(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make([]userWire, 0, len(rows))
	for _, u := range rows {
		out = append(out, userToWire(u))
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

func (a *API) onV1UsersGet(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	u, err := a.Store.LocalUsers.GetByID(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("user not found"))
		return
	}
	w := userToWire(u)
	ctx.JSON(http.StatusOK, &w)
}

type userCreateRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

func (a *API) onV1UsersCreate(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req userCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("username and password are required"))
		return
	}
	if req.Role == "" {
		req.Role = string(rbac.RoleViewer)
	}
	if req.Role != string(rbac.RoleAdmin) && req.Role != string(rbac.RoleViewer) {
		a.writeError(ctx, http.StatusBadRequest, errors.New("role must be 'admin' or 'viewer'"))
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	u := &store.LocalUser{
		ID:           uuid.NewString(),
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		PasswordHash: hash,
		IsAdmin:      req.Role == string(rbac.RoleAdmin),
		IsActive:     true,
		RoleID:       roleIDForName(req.Role),
	}
	if err := a.Store.LocalUsers.Insert(ctx.Request.Context(), u); err != nil {
		if errors.Is(err, store.ErrLocalUserExists) {
			a.writeError(ctx, http.StatusConflict, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "user.created", "local_user", u.ID, map[string]string{
		"username": u.Username, "role": req.Role,
	})
	w := userToWire(u)
	ctx.JSON(http.StatusCreated, &w)
}

type userUpdateRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	Role        *string `json:"role"`
	IsActive    *bool   `json:"is_active"`
}

func (a *API) onV1UsersUpdate(ctx *gin.Context) {
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
	var req userUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	existing, err := a.Store.LocalUsers.GetByID(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("user not found"))
		return
	}
	if req.DisplayName != nil {
		existing.DisplayName = *req.DisplayName
	}
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}
	if err := a.Store.LocalUsers.UpdateProfile(ctx.Request.Context(), existing.ID, existing.DisplayName, existing.IsActive); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if req.Email != nil {
		if err := a.Store.LocalUsers.SetEmail(ctx.Request.Context(), id, *req.Email); err != nil {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		existing.Email = *req.Email
	}
	if req.Role != nil {
		role := *req.Role
		if role != string(rbac.RoleAdmin) && role != string(rbac.RoleViewer) {
			a.writeError(ctx, http.StatusBadRequest, errors.New("role must be 'admin' or 'viewer'"))
			return
		}
		if err := a.Store.LocalUsers.SetRole(ctx.Request.Context(), id, roleIDForName(role)); err != nil {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		existing.RoleID = roleIDForName(role)
	}
	a.emitMutationAudit(ctx, "user.updated", "local_user", id, nil)
	w := userToWire(existing)
	ctx.JSON(http.StatusOK, &w)
}

func (a *API) onV1UsersDelete(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	if err := a.Store.LocalUsers.SoftDelete(ctx.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrLocalUserNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "user.deleted", "local_user", id, nil)
	ctx.Status(http.StatusNoContent)
}

func (a *API) onV1UsersSetRole(ctx *gin.Context) {
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
	var req struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Role != string(rbac.RoleAdmin) && req.Role != string(rbac.RoleViewer) {
		a.writeError(ctx, http.StatusBadRequest, errors.New("role must be 'admin' or 'viewer'"))
		return
	}
	if err := a.Store.LocalUsers.SetRole(ctx.Request.Context(), id, roleIDForName(req.Role)); err != nil {
		if errors.Is(err, store.ErrLocalUserNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "user.role_changed", "local_user", id, map[string]string{"role": req.Role})
	ctx.Status(http.StatusNoContent)
}

func (a *API) onV1UsersResetPassword(ctx *gin.Context) {
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
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.NewPassword == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("new_password is required"))
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if err := a.Store.LocalUsers.SetPassword(ctx.Request.Context(), id, hash); err != nil {
		if errors.Is(err, store.ErrLocalUserNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	// Force a re-rotation on next login by setting must_change_password=1.
	// SetPassword resets must_change_password to 0; bump it back via a
	// small targeted exec.
	if _, err := a.Store.DB.ExecContext(ctx.Request.Context(),
		`UPDATE local_users SET must_change_password = 1, updated_at = ? WHERE id = ?`,
		store.Now(), id,
	); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "user.password_reset", "local_user", id, nil)
	ctx.Status(http.StatusNoContent)
}
