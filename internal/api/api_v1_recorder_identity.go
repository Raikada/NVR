// /v1/recorder/identity — recorder bootstrap identity for the
// configuration UI's identity card.
//
// hostname comes from os.Hostname() (read-only — changing it
// requires hostnamectl / /etc/hostname which is invasive and
// usually managed by the host's provisioning system, not the
// recorder process).
//
// timezone comes from the recorder process's local-time zone
// (time.Local). Read-only at this surface; setting the system
// timezone requires writing /etc/localtime which sits outside the
// recorder's responsibilities.
//
// location is operator-set free-form text stored in conf.
// ServerLocation. Read/write — the only field on this surface
// the operator can update.
//
// firmware is the recorder build version (a.Version), pre-stamped
// during build by the existing release process.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/gin-gonic/gin"
)

type v1RecorderIdentity struct {
	ID                     string   `json:"id"`
	TenantID               string   `json:"tenant_id"`
	Hostname               string   `json:"hostname"`
	Location               string   `json:"location"`
	Timezone               string   `json:"timezone"`
	FirmwareVersion        string   `json:"firmware_version"`
	Paired                 bool     `json:"paired"`
	PublicKeyFingerprint   string   `json:"public_key_fingerprint"`
	PinnedRootFingerprints []string `json:"pinned_root_fingerprints"`
	// CanonicalSource is `recorder` pre-slice-4-B-import or `ms` once
	// the recorder has accepted its first MS-source Camera mutation per
	// ADR 0016 D3. Used by the recorder's local SPA to grey out
	// Add/Edit/Delete on the Cameras page when MS-canonical.
	CanonicalSource string `json:"canonical_source"`
	// PolicyCanonicalSource is the slice-4-C analogue per ADR 0017 D3:
	// `recorder` pre-import or `ms` once the recorder has accepted its
	// first MS-source RecordingPolicy mutation. Independent of
	// CanonicalSource (Camera) — a recorder may be at canonical_source
	// = ms AND policy_canonical_source = recorder during the
	// 4-B → 4-C migration. Used by the local SPA to grey out
	// Add/Edit/Delete on the Policies page when MS-canonical.
	PolicyCanonicalSource string `json:"policy_canonical_source"`

	// PendingSoftwareUpdate (Wave A2) is the most-recent MS-approved
	// software-update lifecycle row this recorder has discovered via
	// the updatepoll goroutine. nil when no update is approved (or
	// the poller hasn't completed its first cycle yet, or the
	// recorder is unpaired). The recorder SPA renders a "Software
	// update available" badge when this is non-nil.
	PendingSoftwareUpdate *PendingSoftwareUpdate `json:"pending_software_update,omitempty"`

	// PendingSoftwareUpdatePolledAt is the wall-clock timestamp of the
	// poller's last successful cycle. Empty when the poller has not
	// yet completed a cycle. Used by the SPA to surface staleness.
	PendingSoftwareUpdatePolledAt string `json:"pending_software_update_polled_at,omitempty"`
}

func (a *API) onV1RecorderIdentityGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	tenant := ""
	location := ""
	if c != nil {
		tenant = c.TenantID
		location = c.ServerLocation
	}
	a.mutex.RUnlock()

	hostname, _ := os.Hostname()
	tzName, _ := time.Now().Zone()

	resp := &v1RecorderIdentity{
		TenantID:               tenant,
		Hostname:               hostname,
		Location:               location,
		Timezone:               tzName,
		FirmwareVersion:        a.Version,
		PinnedRootFingerprints: []string{},
		CanonicalSource:        "recorder",
		PolicyCanonicalSource:  "recorder",
	}

	// Identity is set in production by Core.createResources; absent
	// in some lightweight test harnesses. Defensive nil-check.
	if a.Identity != nil {
		resp.ID = a.Identity.ID().String()
		resp.Paired = a.Identity.IsPaired()
		if fp, err := a.Identity.PublicKeyFingerprint(); err == nil {
			resp.PublicKeyFingerprint = fp
		}
		for _, r := range a.Identity.PinnedRoots() {
			resp.PinnedRootFingerprints = append(resp.PinnedRootFingerprints, r.FingerprintSHA256)
		}
		resp.CanonicalSource = a.Identity.CanonicalSource()
		resp.PolicyCanonicalSource = a.Identity.PolicyCanonicalSource()
	}

	a.mutex.RLock()
	pollState := a.updatePollState
	a.mutex.RUnlock()
	if pollState != nil {
		if pending := pollState.Pending(); pending != nil {
			resp.PendingSoftwareUpdate = pending
		}
		if t := pollState.LastPoll(); !t.IsZero() {
			resp.PendingSoftwareUpdatePolledAt = t.UTC().Format(time.RFC3339)
		}
	}

	ctx.JSON(http.StatusOK, resp)
}

func (a *API) onV1RecorderIdentityPatch(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	var meta struct {
		TenantID *string `json:"tenant_id"`
		Location *string `json:"location"`
	}
	if jerr := json.Unmarshal(body, &meta); jerr != nil {
		a.writeError(ctx, http.StatusBadRequest, jerr)
		return
	}
	if meta.TenantID != nil && *meta.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return
	}
	if meta.Location == nil {
		// Nothing else is patchable today; reject empty patches so
		// callers don't think they wrote something they didn't.
		a.writeError(ctx, http.StatusBadRequest,
			fmt.Errorf("no patchable fields present (location is the only one supported)"))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()
	newConf.ServerLocation = *meta.Location
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	a.Conf = newConf

	a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindSystem,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "server",
		Attributes:   map[string]string{"verb": "patch-identity", "field": "location"},
	})
	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		Message:     "server location updated",
		Attributes:  map[string]string{"surface": "/v1/recorder/identity"},
	})

	go a.Parent.APIConfigSet(newConf)
	a.writeOK(ctx)
}
