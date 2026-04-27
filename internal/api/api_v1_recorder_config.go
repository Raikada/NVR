package api //nolint:revive

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
)

// hookSensitiveFragmentPattern matches common credential markers inside
// shell-hook strings (recorder-script content is at minimum Sensitive
// per data-classification.md and may carry secrets if the operator
// embeds them in hook commands). On read we don't redact the hook
// strings (the operator who set them owns the consequences and must be
// able to read them back to edit them) but we log a warning so the
// classification is visible in audit. Case-insensitive substring match.
var hookSensitiveFragmentPattern = regexp.MustCompile(`(?i)password=|token=|secret=|apikey=|api_key=`)

// warnIfHookCarriesSecrets emits a Warn log line for any hook string
// that looks like it contains an inline credential. Called from PATCH
// handlers (so writes that introduce credentials get flagged) and from
// GET handlers when a hook with credentials is served back. The actual
// hook contents are NOT logged — only the field name and a sentinel —
// to avoid the redaction-of-the-redactor problem.
func (a *API) warnIfHookCarriesSecrets(field, value string) {
	if hookSensitiveFragmentPattern.MatchString(value) {
		a.Log(logger.Warn,
			"escape-hatch: hook field %q appears to contain a credential fragment; "+
				"shell-hook strings are at-minimum Sensitive per data-classification.md", field)
	}
}

// onV1RecorderConfigGet serves /v1/recorder/config — the recorder's
// operational config (logging, timeouts, server bind addresses, TLS
// material refs, metrics/pprof/playback servers, recorder-level shell
// hooks). Recorder-localized escape hatch per ADR 0009 §D5/§D6.
//
// Rationale (D6.3): canonical placement was rejected because this is
// recorder-process bootstrap configuration (where to bind, how to log,
// what TLS material to load, which protocol servers to start) — it is
// recorder-instance-specific by definition and the canonical model has
// nothing comparable. Promoting any of these fields to canonical
// vocabulary would force every other tier (MS, Cloud) to model
// recorder-process internals.
//
// Covers ~95% of conf.GlobalConf; the remaining 5% (PathDefaults, the
// per-path map) lives at /v1/recorder/camera-defaults and /v1/cameras
// respectively.
func (a *API) onV1RecorderConfigGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	// Surface a warning on read for any hook value that looks credentialed,
	// so the operator sees the audit annotation each time the field is
	// inspected.
	if c != nil {
		a.warnIfHookCarriesSecrets("runOnConnect", c.RunOnConnect)
		a.warnIfHookCarriesSecrets("runOnDisconnect", c.RunOnDisconnect)
	}

	// c.Global() reflects every Conf field, including TenantID. The Global
	// shape is recorder-local already (it is not a defs.* canonical type),
	// so it is safe to return as-is for the escape hatch.
	ctx.JSON(http.StatusOK, c.Global())
}

// onV1RecorderConfigPatch handles PATCH /v1/recorder/config with the
// same partial-update semantics as the legacy /v3/config/global/patch.
//
// Rationale (D6.3): recorder bootstrap configuration is not a canonical
// entity; see onV1RecorderConfigGet for the full justification.
func (a *API) onV1RecorderConfigPatch(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	body, ok := a.readTenantScopedBody(ctx)
	if !ok {
		return
	}

	var c conf.OptionalGlobal
	err := jsonwrapper.Decode(bytes.NewReader(body), &c)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Best-effort scan of incoming hook strings for embedded credentials.
	// We re-marshal the optional-global to JSON and look for the two hook
	// keys the global config exposes; this avoids reflecting over the
	// dynamically-typed OptionalGlobal struct.
	if strings.Contains(string(body), "runOnConnect") || strings.Contains(string(body), "runOnDisconnect") {
		var hooks struct {
			RunOnConnect    *string `json:"runOnConnect"`
			RunOnDisconnect *string `json:"runOnDisconnect"`
		}
		_ = jsonwrapper.Decode(bytes.NewReader(body), &hooks) //nolint:errcheck
		if hooks.RunOnConnect != nil {
			a.warnIfHookCarriesSecrets("runOnConnect", *hooks.RunOnConnect)
		}
		if hooks.RunOnDisconnect != nil {
			a.warnIfHookCarriesSecrets("runOnDisconnect", *hooks.RunOnDisconnect)
		}
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()

	newConf.PatchGlobal(&c)

	err = newConf.Validate(nil)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		Message:     "recorder configuration patched",
		Attributes: map[string]string{
			"surface": "/v1/recorder/config",
		},
	})
	a.emitConfigAppliedLocked("server", "", "patch", map[string]string{"surface": "/v1/recorder/config"})

	// since reloading the configuration can cause the shutdown of the API,
	// call it in a goroutine
	go a.Parent.APIConfigSet(newConf)

	a.writeOK(ctx)
}
