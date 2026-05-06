// /v1/recorder/software-updates/apply — Wave 6 software-update
// applier endpoint per ADR 0014.
//
// The MS instructs the recorder to apply an update by POSTing here.
// Auth: the existing JWT/mTLS path; the MS issues a service JWT with
// scope ["software_update.manage"] which the per-route permission
// gate consumes. The recorder re-verifies the manifest signature
// locally (D6) — the trust boundary is the recorder's pinned Raikada
// release public key, not the inbound JWT.
//
// Body shape (mirrors the MS push body):
//
//	{
//	  "manifest":  { ... },
//	  "signature": "<base64 ed25519>",
//	  "artifact_base64": "<optional inline bytes>",
//	  "artifact_url":    "<optional fallback URL>"
//	}
//
// Response: 202 Accepted on apply-started; 4xx on preflight failure;
// 5xx on unexpected internal failure.

package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/softwareupdate"
)

// softwareUpdateApplyRequest is the inbound body shape.
type softwareUpdateApplyRequest struct {
	Manifest    softwareupdate.Manifest `json:"manifest"`
	Signature   string                  `json:"signature"`
	ArtifactB64 string                  `json:"artifact_base64,omitempty"`
	ArtifactURL string                  `json:"artifact_url,omitempty"`
	LifecycleID string                  `json:"lifecycle_id,omitempty"`
	MSVersion   string                  `json:"ms_version,omitempty"`
}

// SoftwareUpdateApplier is the API-side wrapper that owns the
// recorder's softwareupdate.Applier. Wired by Core at startup when a
// pinned release public key is configured. nil pre-config (or in
// test harnesses).
type SoftwareUpdateApplier interface {
	Apply(ctx ginContext, opts softwareupdate.ApplyOptions) error
}

// SoftwareUpdateApplier on *API; the API exposes a setter so Core
// can wire the applier without import cycles.
type softwareUpdateApplierField struct {
	apply func(*gin.Context, softwareupdate.ApplyOptions) error
}

// SetSoftwareUpdateApplier wires the applier function into the API.
// Called by Core after the applier package is constructed. The
// applier function is invoked from the handler with the inbound
// gin.Context for source-IP / request-id propagation.
func (a *API) SetSoftwareUpdateApplier(fn func(*gin.Context, softwareupdate.ApplyOptions) error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.softwareUpdateApplier.apply = fn
}

// onV1RecorderSoftwareUpdateApplyPost handles the inbound MS push.
func (a *API) onV1RecorderSoftwareUpdateApplyPost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req softwareUpdateApplyRequest
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		a.writeError(ctx, http.StatusBadRequest, jerr)
		return
	}
	if req.Manifest.ID == "" || req.Signature == "" {
		a.writeError(ctx, http.StatusBadRequest,
			errors.New("software_update: manifest + signature required"))
		return
	}
	var artifact []byte
	if req.ArtifactB64 != "" {
		decoded, derr := base64.StdEncoding.DecodeString(req.ArtifactB64)
		if derr != nil {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("artifact_base64 not base64: %w", derr))
			return
		}
		artifact = decoded
	}
	opts := softwareupdate.ApplyOptions{
		Manifest:         req.Manifest,
		Signature:        req.Signature,
		ArtifactBytes:    artifact,
		ArtifactURL:      req.ArtifactURL,
		LifecycleID:      req.LifecycleID,
		MSVersion:        req.MSVersion,
		RecorderHardware: runtime.GOOS + "/" + runtime.GOARCH,
	}

	a.mutex.RLock()
	fn := a.softwareUpdateApplier.apply
	a.mutex.RUnlock()
	if fn == nil {
		// Applier not wired — recorder build doesn't have a pinned
		// release public key, or running in a test harness. Surface a
		// stable error.
		a.writeError(ctx, http.StatusServiceUnavailable,
			errors.New("software_update: recorder has no release public key configured"))
		return
	}
	if err := fn(ctx, opts); err != nil {
		// Audit a permission-denied / preflight-failed via the existing
		// chain. The applier itself emits preflight_failed; the
		// failure-to-apply is the surface here.
		a.emitAudit(defs.AuditLogEntryInput{
			ActorKind:    defs.AuditActorKindServiceAccount,
			ActorID:      "ms-service-software-update-push",
			Action:       "software_update.failed",
			Outcome:      defs.AuditOutcomeFailure,
			ResourceKind: "software_update_lifecycle",
			ResourceID:   req.LifecycleID,
			Attributes: map[string]string{
				"version": req.Manifest.Version,
				"reason":  err.Error(),
			},
		})
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	ctx.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

// ginContext is a tiny adapter so SoftwareUpdateApplier can be
// satisfied by an applier that doesn't import gin. Currently unused;
// the wiring goes through SetSoftwareUpdateApplier directly.
type ginContext interface{}
