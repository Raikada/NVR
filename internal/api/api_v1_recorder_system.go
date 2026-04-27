// /v1/recorder/{reboot,config-backup} — system actions exposed to
// the configuration UI's Settings page.
//
// reboot: admin-gated, audit-emitted, shells out to /sbin/reboot
// after a 5-second graceful-shutdown delay so the HTTP response
// makes it back to the caller before the process tears down. On
// non-Linux hosts returns 501 Not Implemented (recorder's
// production target is Linux/Alpine).
//
// config-backup: admin-gated, returns the current running config
// as JSON (the same shape /v1/recorder/config emits). Lets an
// operator capture a known-good state before making changes.
//
// config-restore: admin-gated, audit-emitted. Accepts a JSON body
// matching the same shape /v1/recorder/config GET emits, validates
// it against the existing OptionalGlobal decoder + Conf.Validate
// machinery, and applies it via the standard reload path. Failure
// at any stage leaves the running config untouched.

package api

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
)

const rebootDelay = 5 * time.Second

func (a *API) onV1RecorderRebootPost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	if runtime.GOOS != "linux" {
		a.writeError(ctx, http.StatusNotImplemented,
			fmt.Errorf("reboot is only supported on Linux hosts"))
		return
	}

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    principalFromContext(ctx).PrincipalKind,
		ActorID:      principalFromContext(ctx).Sub,
		Action:       "recorder.reboot",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "server",
		Attributes:   map[string]string{"delay_s": fmt.Sprintf("%d", int(rebootDelay.Seconds()))},
	})

	a.Log(logger.Warn, "reboot requested via /v1/recorder/reboot — shutting down in %s", rebootDelay)

	// Schedule the reboot AFTER returning the response. Best-effort:
	// if /sbin/reboot is missing (containers without CAP_SYS_BOOT)
	// the goroutine logs and exits; the process keeps running and
	// the caller saw success-on-queue.
	go func() {
		time.Sleep(rebootDelay)
		path, err := exec.LookPath("reboot")
		if err != nil {
			a.Log(logger.Error, "reboot: binary not on PATH (%v); recorder will continue running", err)
			return
		}
		cmd := exec.Command(path) // nolint:gosec — admin-gated, no untrusted input on argv
		if err := cmd.Run(); err != nil {
			a.Log(logger.Error, "reboot: invocation failed (%v); recorder will continue running", err)
		}
	}()

	a.writeOK(ctx)
}

func (a *API) onV1RecorderConfigBackup(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()
	if c == nil {
		a.writeError(ctx, http.StatusInternalServerError,
			fmt.Errorf("recorder configuration not loaded"))
		return
	}

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    principalFromContext(ctx).PrincipalKind,
		ActorID:      principalFromContext(ctx).Sub,
		Action:       "recorder.config_backup",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "server",
	})

	// Same serialization as GET /v1/recorder/config; sets the
	// download disposition so a browser kicks off a save dialog.
	ctx.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="recorder-config-%s.json"`,
			time.Now().UTC().Format("20060102-150405")))
	ctx.JSON(http.StatusOK, c.Global())
}

func (a *API) onV1RecorderConfigRestore(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Decode through the same OptionalGlobal pathway PATCH uses,
	// then patch a fresh Conf.Clone() with it. Validation runs the
	// same Conf.Validate() machinery the bootstrap path uses; a
	// validation failure leaves the running config untouched.
	var optional conf.OptionalGlobal
	if err := jsonwrapper.Decode(bytes.NewReader(body), &optional); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("decode config body: %w", err))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()
	newConf.PatchGlobal(&optional)
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("validate restored config: %w", err))
		return
	}

	a.Conf = newConf
	a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    principalFromContext(ctx).PrincipalKind,
		ActorID:      principalFromContext(ctx).Sub,
		Action:       "recorder.config_restore",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "server",
		Attributes:   map[string]string{"bytes": fmt.Sprintf("%d", len(body))},
	})
	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityWarning,
		SubjectKind: defs.EventSubjectKindServer,
		Message:     "recorder config restored from backup",
		Attributes:  map[string]string{"surface": "/v1/recorder/config-restore"},
	})

	go a.Parent.APIConfigSet(newConf)
	a.writeOK(ctx)
}
