// Package api: /v1/auth/* handlers — consumer-friendly aliases for the
// pre-existing /v1/recorder/login + /v1/recorder/local-users/me/password
// endpoints, plus the new /v1/auth/me + /v1/auth/logout shapes.
//
// Login sets a HttpOnly Secure SameSite=Lax cookie named raikada_session
// alongside the JSON body so the SPA can authenticate cross-tab without
// shipping the JWT to JS code.
//
// Audit: auth.login.success, auth.login.failure, auth.logout,
// auth.password_changed.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/localauth"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
	"github.com/bluenviron/mediamtx/internal/rbac"
)

// sessionCookieName is the cookie name the SPA relies on. HttpOnly
// (browser JS can't read), Secure (HTTPS only — recorder ships
// self-signed TLS by default), SameSite=Lax (works on top-level POSTs
// from operator UIs while preventing classic CSRF).
const sessionCookieName = "raikada_session"

// authMeResponse is the body shape of GET /v1/auth/me. Mirrors the
// login response's user block plus the resolved permission set so the
// SPA can hide/show controls without a separate round trip.
type authMeResponse struct {
	User        authMeUser         `json:"user"`
	Permissions []rbac.Permission  `json:"permissions"`
}

type authMeUser struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name,omitempty"`
	Email              string `json:"email,omitempty"`
	Role               string `json:"role"`
	IsActive           bool   `json:"is_active"`
	MustChangePassword bool   `json:"must_change_password"`
}

// authLoginResponse is the body shape returned by POST /v1/auth/login.
// Mirrors the existing loginResponse (recorder/login) but with the
// cleaner consumer surface: access_token + nested user object instead
// of flat fields. The cookie carries the same JWT.
type authLoginResponse struct {
	AccessToken string     `json:"access_token"`
	ExpiresAt   time.Time  `json:"expires_at"`
	User        authMeUser `json:"user"`
}

// onV1AuthLogin handles POST /v1/auth/login. Body: {username, password}.
// Reachable without authentication via the pre-auth bypass list.
func (a *API) onV1AuthLogin(ctx *gin.Context) {
	if a.LocalAuth == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, &defs.APIError{
			Status: defs.APIErrorStatusError,
			Error:  "local auth not initialized",
		})
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req loginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	username := localauth.SanitizeUsername(req.Username)
	if username == "" || req.Password == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, &defs.APIError{
			Status: defs.APIErrorStatusError,
			Error:  "username and password are required",
		})
		return
	}

	ip := httpp.RemoteAddr(ctx)
	res, err := a.LocalAuth.Login(ctx.Request.Context(), username, req.Password, ip)
	if err != nil {
		a.emitAudit(defs.AuditLogEntryInput{
			ActorKind:    defs.AuditActorKindUnauthenticated,
			Action:       "auth.login.failure",
			Outcome:      defs.AuditOutcomeFailure,
			ResourceKind: "session",
			SourceIP:     ip,
			Attributes:   map[string]string{"username": username},
		})
		switch {
		case errors.Is(err, localauth.ErrInvalidCredentials),
			errors.Is(err, localauth.ErrAccountLocked),
			errors.Is(err, localauth.ErrAccountInactive):
			<-time.After(2 * time.Second)
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, &defs.APIError{
				Status: defs.APIErrorStatusError,
				Error:  "invalid credentials",
			})
		default:
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindLocalUser,
		ActorID:      res.UserID,
		Action:       "auth.login.success",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "session",
		SourceIP:     ip,
		Attributes:   map[string]string{"username": res.Username},
	})

	// Set the session cookie. MaxAge mirrors the JWT TTL (15m) so the
	// browser drops the cookie when the token expires.
	maxAge := int(time.Until(res.ExpiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(sessionCookieName, res.Token, maxAge, "/", "", true, true)

	ctx.JSON(http.StatusOK, authLoginResponse{
		AccessToken: res.Token,
		ExpiresAt:   res.ExpiresAt,
		User: authMeUser{
			ID:                 res.UserID,
			Username:           res.Username,
			Role:               roleNameForLogin(res.IsAdmin),
			IsActive:           true,
			MustChangePassword: res.MustChangePassword,
		},
	})
}

// onV1AuthLogout handles POST /v1/auth/logout. Clears the session
// cookie and emits an audit row. Always returns 204 — logout is
// idempotent.
func (a *API) onV1AuthLogout(ctx *gin.Context) {
	principal := principalFromContext(ctx)
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    principal.PrincipalKind,
		ActorID:      principal.Sub,
		Action:       "auth.logout",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "session",
		SourceIP:     httpp.RemoteAddr(ctx),
	})
	// Clear cookie by setting an immediate expiration.
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(sessionCookieName, "", -1, "/", "", true, true)
	ctx.Status(http.StatusNoContent)
}

// onV1AuthPassword handles POST /v1/auth/password — a consumer-friendly
// alias for POST /v1/recorder/local-users/me/password.
func (a *API) onV1AuthPassword(ctx *gin.Context) {
	a.onV1RecorderLocalUsersMePasswordPost(ctx)
	// Audit emission is added inside onV1RecorderLocalUsersMePasswordPost.
}

// onV1AuthMe handles GET /v1/auth/me. Returns the current user record
// + their permission set. Authenticated route.
func (a *API) onV1AuthMe(ctx *gin.Context) {
	principal := principalFromContext(ctx)
	if principal.Sub == "" {
		// Pre-OQ10 service-account path; surface a synthetic admin
		// principal so legacy callers don't 401.
		claims, _ := ctx.Get(rbac.ClaimsContextKey)
		c, _ := claims.(rbac.Claims)
		ctx.JSON(http.StatusOK, authMeResponse{
			User:        authMeUser{Role: string(c.Role), IsActive: true},
			Permissions: rbac.AllPermissionsFor(c.Role),
		})
		return
	}
	if a.Store == nil {
		// Without store wiring (test path) fall back to the principal's
		// scope claims projected as Role.
		c := claimsFromPrincipal(principal)
		ctx.JSON(http.StatusOK, authMeResponse{
			User: authMeUser{
				ID: principal.Sub, Role: string(c.Role), IsActive: true,
			},
			Permissions: rbac.AllPermissionsFor(c.Role),
		})
		return
	}
	user, err := a.Store.LocalUsers.GetByID(ctx.Request.Context(), principal.Sub)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	role := rbac.RoleViewer
	if user.IsAdmin || user.RoleID == string(rbac.RoleAdmin) {
		role = rbac.RoleAdmin
	}
	ctx.JSON(http.StatusOK, authMeResponse{
		User: authMeUser{
			ID:                 user.ID,
			Username:           user.Username,
			DisplayName:        user.DisplayName,
			Email:              user.Email,
			Role:               string(role),
			IsActive:           user.IsActive,
			MustChangePassword: user.MustChangePassword,
		},
		Permissions: rbac.AllPermissionsFor(role),
	})
}

// roleNameForLogin maps the bootstrap is_admin bit to the rbac.Role
// string used in the login response. Foundation single-tier model:
// admin or viewer.
func roleNameForLogin(isAdmin bool) string {
	if isAdmin {
		return string(rbac.RoleAdmin)
	}
	return string(rbac.RoleViewer)
}

// authPasswordChangedEmit is invoked from onV1RecorderLocalUsersMePasswordPost
// after a successful password rotation. Wires the auth.password_changed
// canonical action expected by the audit allow-list.
//
// Internal helper: kept here so the additive Phase 5 audit kind ships
// with the auth handlers and not in the legacy file.
func (a *API) authPasswordChangedEmit(_ context.Context, userID, username string) {
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindLocalUser,
		ActorID:      userID,
		Action:       "auth.password_changed",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "local_user",
		ResourceID:   userID,
		Attributes:   map[string]string{"username": username},
	})
}
