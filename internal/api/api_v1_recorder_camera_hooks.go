package api //nolint:revive

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/gin-gonic/gin"
)

// CameraHooks is the recorder-localized escape-hatch shape for the
// per-camera shell hooks that conf.Path carries. It mirrors the hook
// fields on conf.Path 1:1; JSON tags match conf.Path so PATCH bodies
// round-trip cleanly through copyStructFields.
//
// The strings are at minimum Sensitive (data-classification.md) — they
// can carry credentials if the operator embeds them in shell commands.
// On both reads and writes we scan for credential markers and emit a
// Warn log; we do NOT redact the hook contents on read because the
// operator who set them needs to be able to read them back to edit
// them.
type CameraHooks struct {
	// TenantID JSON tag is snake_case per the canonical /v1 surface
	// convention; the runOn* fields retain MediaMTX-lineage camelCase
	// per the escape-hatch carve-out (they mirror conf.Path 1:1 for
	// round-trip compatibility with the upstream shape).
	TenantID string `json:"tenant_id,omitempty"`

	RunOnInit                  string        `json:"runOnInit"`
	RunOnInitRestart           bool          `json:"runOnInitRestart"`
	RunOnDemand                string        `json:"runOnDemand"`
	RunOnDemandRestart         bool          `json:"runOnDemandRestart"`
	RunOnDemandStartTimeout    conf.Duration `json:"runOnDemandStartTimeout"`
	RunOnDemandCloseAfter      conf.Duration `json:"runOnDemandCloseAfter"`
	RunOnUnDemand              string        `json:"runOnUnDemand"`
	RunOnReady                 string        `json:"runOnReady"`
	RunOnReadyRestart          bool          `json:"runOnReadyRestart"`
	RunOnNotReady              string        `json:"runOnNotReady"`
	RunOnRead                  string        `json:"runOnRead"`
	RunOnReadRestart           bool          `json:"runOnReadRestart"`
	RunOnUnread                string        `json:"runOnUnread"`
	RunOnRecordSegmentCreate   string        `json:"runOnRecordSegmentCreate"`
	RunOnRecordSegmentComplete string        `json:"runOnRecordSegmentComplete"`
}

// cameraHooksFromPath copies the hook fields out of a conf.Path into
// the wire shape.
func cameraHooksFromPath(p *conf.Path) *CameraHooks {
	return &CameraHooks{
		RunOnInit:                  p.RunOnInit,
		RunOnInitRestart:           p.RunOnInitRestart,
		RunOnDemand:                p.RunOnDemand,
		RunOnDemandRestart:         p.RunOnDemandRestart,
		RunOnDemandStartTimeout:    p.RunOnDemandStartTimeout,
		RunOnDemandCloseAfter:      p.RunOnDemandCloseAfter,
		RunOnUnDemand:              p.RunOnUnDemand,
		RunOnReady:                 p.RunOnReady,
		RunOnReadyRestart:          p.RunOnReadyRestart,
		RunOnNotReady:              p.RunOnNotReady,
		RunOnRead:                  p.RunOnRead,
		RunOnReadRestart:           p.RunOnReadRestart,
		RunOnUnread:                p.RunOnUnread,
		RunOnRecordSegmentCreate:   p.RunOnRecordSegmentCreate,
		RunOnRecordSegmentComplete: p.RunOnRecordSegmentComplete,
	}
}

// inspectCameraHooksForSecrets warns when any hook string carries an
// inline credential marker. Field names only are logged — never the
// hook contents themselves — to avoid leaking the very values the
// classification flags as Sensitive.
func (a *API) inspectCameraHooksForSecrets(scope string, h *CameraHooks) {
	for field, val := range map[string]string{
		"runOnInit":                  h.RunOnInit,
		"runOnDemand":                h.RunOnDemand,
		"runOnUnDemand":              h.RunOnUnDemand,
		"runOnReady":                 h.RunOnReady,
		"runOnNotReady":              h.RunOnNotReady,
		"runOnRead":                  h.RunOnRead,
		"runOnUnread":                h.RunOnUnread,
		"runOnRecordSegmentCreate":   h.RunOnRecordSegmentCreate,
		"runOnRecordSegmentComplete": h.RunOnRecordSegmentComplete,
	} {
		a.warnIfHookCarriesSecrets(scope+"."+field, val)
	}
}

// onV1RecorderCameraHooksGet serves /v1/recorder/cameras/{id}/hooks —
// the per-camera shell hooks. Recorder-localized escape hatch per
// ADR 0009 §D5/§D6.
//
// Rationale (D6.3): shell hooks are recorder-script behavior. They
// invoke local shell commands when paths transition state (init,
// demand, ready, read, segment-create, segment-complete). The platform
// can't sensibly reason about them across tiers because the commands
// only make sense on the recorder host's filesystem and process tree;
// the MS has no business knowing what shell command runs when a path
// becomes ready. They are also at-minimum Sensitive, so promoting them
// to canonical would force a cross-tier classification regime around
// per-camera config.
func (a *API) onV1RecorderCameraHooksGet(ctx *gin.Context) {
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	pathName, ok := pathNameFromCameraID(c.Paths, cameraID)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	out := cameraHooksFromPath(c.Paths[pathName])
	out.TenantID = ""
	a.inspectCameraHooksForSecrets("camera="+cameraID, out)
	ctx.JSON(http.StatusOK, out)
}

// onV1RecorderCameraHooksPatch handles PATCH /v1/recorder/cameras/
// {id}/hooks. The body is decoded as a conf.OptionalPath so existing
// copyStructFields machinery patches only the hook fields the operator
// supplies; non-hook fields are not surfaced because the wire shape
// (CameraHooks) only documents hook fields, but the underlying
// conf.OptionalPath would still accept any Path field — we audit the
// raw body for non-hook keys post-decode.
//
// Rationale (D6.3): see the GET handler.
func (a *API) onV1RecorderCameraHooksPatch(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	body, ok := a.readTenantSnakeCaseScopedBody(ctx)
	if !ok {
		return
	}

	// Pre-flight: warn for credential markers in any hook string the
	// operator is about to install.
	var probe CameraHooks
	if jerr := jsonwrapper.Decode(bytes.NewReader(body), &probe); jerr == nil {
		a.inspectCameraHooksForSecrets("camera="+cameraID+",incoming", &probe)
	}

	var p conf.OptionalPath
	err = jsonwrapper.Decode(bytes.NewReader(body), &p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	pathName, ok := pathNameFromCameraID(a.Conf.Paths, cameraID)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	newConf := a.Conf.Clone()

	err = newConf.PatchPath(pathName, &p)
	if err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}

	err = newConf.Validate(nil)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.emitConfigAppliedLocked("camera", cameraID, "patch", map[string]string{
		"camera_id": cameraID,
		"surface":   "/v1/recorder/cameras/{id}/hooks",
	})

	a.writeOK(ctx)
}
