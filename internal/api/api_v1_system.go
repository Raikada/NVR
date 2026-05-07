// Package api: /v1/system/* handlers per Phase 5 Task 5.7.
//
// Settings are an allow-list of mutable keys. SMTP password is special-
// cased: accepted in plaintext, encrypted via the vault, persisted as
// hex-encoded ciphertext + nonce under separate keys
// ("smtp_password_ciphertext", "smtp_password_nonce") that the
// dispatcher already reads.
//
// TLS replacement: validates the supplied PEM pair before writing to
// the identity-dir paths so a mis-pasted cert never breaks the running
// server. The Phase 6 fsnotify watcher reloads on the WRITE event.
//
// /v1/system/setup-status and /v1/system/info are anonymous so the
// SPA can render the setup wizard before the operator has a session.
package api //nolint:revive

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/rbac"
)

// systemSettingsAllowList enumerates the keys callers may set via PATCH
// /v1/system/settings. Anything not on this list is rejected with 400.
var systemSettingsAllowList = map[string]struct{}{
	"site_name":                       {},
	"timezone":                        {},
	"language":                        {},
	"lockout_threshold":               {},
	"lockout_duration_minutes":        {},
	"capability_probe_interval_hours": {},
	"health_debounce_seconds":         {},
	"outbox_workers":                  {},
	"outbox_max_attempts":             {},
	"cloud_outbox_horizon_hours":      {},
	"smtp_host":                       {},
	"smtp_port":                       {},
	"smtp_username":                   {},
	"smtp_from_address":               {},
	"smtp_use_tls":                    {},
	"smtp_password":                   {}, // special-cased: encrypts before persist
	"cloud_endpoint":                  {},
	"snapshot_root":                   {},
	"clip_root":                       {},
}

func (a *API) registerV1SystemEndpoints(r gin.IRouter) {
	r.GET("/system/settings", rbac.RequirePerm(rbac.PermSystemSettingsRead, a.auditEmitter()), a.onV1SystemSettingsGet)
	r.PATCH("/system/settings", rbac.RequirePerm(rbac.PermSystemSettingsWrite, a.auditEmitter()), a.onV1SystemSettingsPatch)
	r.PUT("/system/tls", rbac.RequirePerm(rbac.PermSystemTLSWrite, a.auditEmitter()), a.onV1SystemTLSPut)
	r.POST("/system/retention/sweep", rbac.RequirePerm(rbac.PermSystemRetentionRun, a.auditEmitter()), a.onV1SystemRetentionSweep)

	// Anonymous endpoints — registered on the same router but consumed
	// before the auth middleware via isPreAuthBypassPath.
	r.GET("/system/setup-status", a.onV1SystemSetupStatus)
	r.GET("/system/info", a.onV1SystemInfo)
}

func (a *API) onV1SystemSettingsGet(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	all, err := a.Store.SystemSettings.GetAll(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	// Redact the SMTP password ciphertext / nonce from the response.
	delete(all, "smtp_password_ciphertext")
	delete(all, "smtp_password_nonce")
	if _, has := all["smtp_password_ciphertext"]; has || a.smtpPasswordSet(ctx.Request.Context()) {
		all["smtp_password_set"] = "true"
	}
	ctx.JSON(http.StatusOK, gin.H{"settings": all})
}

func (a *API) smtpPasswordSet(ctx context.Context) bool {
	if a.Store == nil {
		return false
	}
	s, err := a.Store.SystemSettings.Get(ctx, "smtp_password_ciphertext")
	return err == nil && s != nil && s.Value != ""
}

func (a *API) onV1SystemSettingsPatch(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req map[string]string
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	for k := range req {
		if _, ok := systemSettingsAllowList[k]; !ok {
			a.writeError(ctx, http.StatusBadRequest, errors.New("unknown setting key: "+k))
			return
		}
	}
	principal := principalFromContext(ctx)
	updates := make(map[string]string, len(req))
	for k, v := range req {
		if k == "smtp_password" {
			if a.Vault == nil {
				a.writeError(ctx, http.StatusServiceUnavailable, errors.New("vault not wired"))
				return
			}
			if v == "" {
				updates["smtp_password_ciphertext"] = ""
				updates["smtp_password_nonce"] = ""
				continue
			}
			ct, nonce, err := a.Vault.Encrypt([]byte(v))
			if err != nil {
				a.writeError(ctx, http.StatusInternalServerError, err)
				return
			}
			updates["smtp_password_ciphertext"] = hex.EncodeToString(ct)
			updates["smtp_password_nonce"] = hex.EncodeToString(nonce)
			continue
		}
		updates[k] = v
	}
	if err := a.Store.SystemSettings.UpsertMany(ctx.Request.Context(), updates, principal.Sub); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "system.settings_updated", "system", "", nil)
	a.onV1SystemSettingsGet(ctx)
}

type systemTLSRequest struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

func (a *API) onV1SystemTLSPut(ctx *gin.Context) {
	if a.Identity == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("identity not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req systemTLSRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.CertPEM == "" || req.KeyPEM == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("cert_pem and key_pem are required"))
		return
	}
	if _, err := tls.X509KeyPair([]byte(req.CertPEM), []byte(req.KeyPEM)); err != nil {
		a.writeError(ctx, http.StatusBadRequest, errors.New("invalid PEM pair: "+err.Error()))
		return
	}
	idDir := a.Identity.Dir()
	if idDir == "" {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("identity dir unavailable"))
		return
	}
	certPath := filepath.Join(idDir, "tls.crt")
	keyPath := filepath.Join(idDir, "tls.key")
	if err := os.WriteFile(certPath, []byte(req.CertPEM), 0o600); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(keyPath, []byte(req.KeyPEM), 0o600); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "system.tls_replaced", "system", "", nil)
	ctx.Status(http.StatusNoContent)
}

func (a *API) onV1SystemRetentionSweep(ctx *gin.Context) {
	if a.RetentionMgr == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("retention manager not wired"))
		return
	}
	if err := a.RetentionMgr.SweepAll(ctx.Request.Context()); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "system.retention_swept", "system", "", nil)
	ctx.Status(http.StatusNoContent)
}

// onV1SystemSetupStatus is anonymous: returns whether the recorder
// needs initial setup.
//
// setup_required = (local_users.count == 0) OR (the bootstrap admin
// still has must_change_password = 1).
func (a *API) onV1SystemSetupStatus(ctx *gin.Context) {
	out := gin.H{"setup_required": false}
	if a.Store == nil {
		// Without a wired store the recorder can't determine setup
		// state; default to "yes" so the SPA shows the wizard.
		out["setup_required"] = true
		ctx.JSON(http.StatusOK, out)
		return
	}
	n, err := a.Store.LocalUsers.Count(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if n == 0 {
		out["setup_required"] = true
		ctx.JSON(http.StatusOK, out)
		return
	}
	users, err := a.Store.LocalUsers.ListAll(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	for _, u := range users {
		if u.IsAdmin && u.MustChangePassword {
			out["setup_required"] = true
			break
		}
	}
	ctx.JSON(http.StatusOK, out)
}

// onV1SystemInfo is anonymous: returns version + recorder id +
// setup-status. The SPA hits this on first paint.
func (a *API) onV1SystemInfo(ctx *gin.Context) {
	out := gin.H{
		"version":     a.Version,
		"recorder_id": a.recorderID(),
	}
	if a.Store == nil {
		out["setup_required"] = true
		ctx.JSON(http.StatusOK, out)
		return
	}
	n, err := a.Store.LocalUsers.Count(ctx.Request.Context())
	if err == nil && n == 0 {
		out["setup_required"] = true
	}
	ctx.JSON(http.StatusOK, out)
}

