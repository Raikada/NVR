// /v1/recorder/login + /v1/recorder/local-users/me/password — pre-pairing
// operator authentication endpoints.
//
// Both endpoints back onto internal/localauth.Manager. The login
// endpoint is reachable pre-auth (per the pre-pairing auth slice
// 2026-05-06; see middlewareAuth's pre-auth bypass list); the
// password-change endpoint requires a recorder-local JWT (any valid
// recorder-local-issued token, including ones with must_change_password=true
// — that's the whole point of the forced-rotation flow).
//
// Audit chain integration: localauth.Manager emits auth.session_started
// per ADR 0006 D1; we wire its callback to api.emitAudit at startup.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/localauth"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expires_at"`
	UserID             string    `json:"user_id"`
	Username           string    `json:"username"`
	IsAdmin            bool      `json:"is_admin"`
	MustChangePassword bool      `json:"must_change_password"`
	Scope              []string  `json:"scope"`
}

// onV1RecorderLogin handles POST /v1/recorder/login.
//
// Reachable without authentication via middlewareAuth's pre-auth
// bypass list. Body: {username, password}. On success returns a JWT
// the SPA stores and presents on subsequent /v1/* requests.
//
// Error semantics:
//   - 400 on malformed body.
//   - 401 + error_code: "invalid_credentials" on bad username/password
//     (no user-vs-pass disambiguation; standard pattern).
//   - 503 if the LocalAuth manager is not initialized (recorder
//     starting up; should never reach the network in practice).
func (a *API) onV1RecorderLogin(ctx *gin.Context) {
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

	res, err := a.LocalAuth.Login(ctx.Request.Context(), username, req.Password, httpp.RemoteAddr(ctx))
	if err != nil {
		switch {
		case errors.Is(err, localauth.ErrInvalidCredentials),
			errors.Is(err, localauth.ErrAccountLocked),
			errors.Is(err, localauth.ErrAccountInactive):
			// Pause briefly to slow brute force, mirroring auth.PauseAfterError.
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

	ctx.JSON(http.StatusOK, loginResponse{
		Token:              res.Token,
		ExpiresAt:          res.ExpiresAt,
		UserID:             res.UserID,
		Username:           res.Username,
		IsAdmin:            res.IsAdmin,
		MustChangePassword: res.MustChangePassword,
		Scope:              res.Scope,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// onV1RecorderLocalUsersMePasswordPost handles
// POST /v1/recorder/local-users/me/password.
//
// Requires a recorder-local JWT (the principal's PrincipalKind must be
// local_user). The endpoint is intentionally reachable even when the
// operator's JWT carries must_change_password=true — that's the entire
// purpose of the endpoint. We do NOT gate on a specific permission
// because changing one's own password is always available to the
// authenticated user.
//
// Body: {current_password, new_password}. Verifies the current password,
// rotates the hash, clears must_change_password, and re-issues a JWT
// the SPA can use immediately without bouncing back through /login.
func (a *API) onV1RecorderLocalUsersMePasswordPost(ctx *gin.Context) {
	if a.LocalAuth == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, &defs.APIError{
			Status: defs.APIErrorStatusError,
			Error:  "local auth not initialized",
		})
		return
	}
	principal := principalFromContext(ctx)
	if principal.PrincipalKind != defs.AuditActorKindLocalUser || principal.Sub == "" {
		// Only recorder-local users can rotate via this endpoint. MS-
		// authenticated operators rotate via the MS UI.
		ctx.AbortWithStatusJSON(http.StatusForbidden, &defs.APIError{
			Status: defs.APIErrorStatusError,
			Error:  "this endpoint is for recorder-local users only",
		})
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req changePasswordRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.NewPassword == "" || req.CurrentPassword == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, &defs.APIError{
			Status: defs.APIErrorStatusError,
			Error:  "current_password and new_password are required",
		})
		return
	}

	res, err := a.LocalAuth.ChangePassword(ctx.Request.Context(), principal.Sub,
		req.CurrentPassword, req.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, localauth.ErrInvalidCredentials):
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, &defs.APIError{
				Status: defs.APIErrorStatusError,
				Error:  "current password is incorrect",
			})
		case errors.Is(err, localauth.ErrPasswordTooShort):
			ctx.AbortWithStatusJSON(http.StatusBadRequest, &defs.APIError{
				Status: defs.APIErrorStatusError,
				Error:  "new password is too short",
			})
		case errors.Is(err, localauth.ErrAccountInactive):
			ctx.AbortWithStatusJSON(http.StatusForbidden, &defs.APIError{
				Status: defs.APIErrorStatusError,
				Error:  "account is inactive",
			})
		default:
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	// Audit the password rotation. Phase 5 standardizes on
	// auth.password_changed (was user.password_changed; the legacy kind
	// was redundant once auth.* became the canonical authentication
	// namespace).
	a.authPasswordChangedEmit(ctx.Request.Context(), res.UserID, res.Username)

	ctx.JSON(http.StatusOK, loginResponse{
		Token:              res.Token,
		ExpiresAt:          res.ExpiresAt,
		UserID:             res.UserID,
		Username:           res.Username,
		IsAdmin:            res.IsAdmin,
		MustChangePassword: res.MustChangePassword,
		Scope:              res.Scope,
	})
}
