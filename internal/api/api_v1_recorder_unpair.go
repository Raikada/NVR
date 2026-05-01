// /v1/recorder/unpair — recorder-local unpair: wipe the issued cert
// + chain + pinned roots and reset the pairing flow to idle.
//
// Distinct from the MS-initiated unpair flow per pairing-flows.md
// §2.5 (which lands when the recorder ↔ MS WebSocket exists in a
// later slice). This is the "operator wants to detach this recorder
// from its current MS" path: typed wrong MS URL/token, wants to
// re-pair with a different MS, or recovering from a stale pairing
// record on the MS.
//
// Per ADR 0002 D3, the recorder's UUIDv7 + ECDSA keypair survive
// unpair — those are stable for the life of the install. Only the
// MS-issued material (cert, chain, pinned roots) is wiped.

package api

import (
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/gin-gonic/gin"
)

func (a *API) onV1RecorderUnpairPost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	if a.Identity == nil {
		a.writeError(ctx, http.StatusServiceUnavailable,
			fmt.Errorf("identity not initialized"))
		return
	}
	if !a.Identity.IsPaired() {
		a.writeError(ctx, http.StatusConflict,
			fmt.Errorf("recorder is not currently paired"))
		return
	}

	// Capture pre-clear context for the audit entry. The recorder's
	// own id (UUIDv7) is stable across unpair so emitting it as the
	// resource_id is well-defined.
	recorderID := a.Identity.ID().String()
	preFingerprints := make([]string, 0)
	for _, r := range a.Identity.PinnedRoots() {
		preFingerprints = append(preFingerprints, r.FingerprintSHA256)
	}

	if err := a.Identity.ClearIssuedIdentity(); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	// Reset the pairing flow's transient state so the operator can
	// immediately start a fresh pairing flow without seeing a stale
	// "approved" status.
	if a.Pairing != nil {
		a.Pairing.Reset()
	}

	// Refresh mDNS announcement (paired=true → false) so MS instances
	// on the LAN see the up-to-date state. Best-effort.
	if a.MDNS != nil {
		_ = a.MDNS.Refresh()
	}

	// Audit: device.unpaired per ADR 0006 D2 (recorder-local
	// emission). When the recorder ↔ MS WebSocket lands, the
	// equivalent MS-initiated path will additionally produce a
	// device.identity_revoked entry on the MS chain per
	// pairing-flows.md §2.5.
	attrs := map[string]string{
		"recording_server_id": recorderID,
		"reason":              "operator_initiated_recorder_local",
	}
	for i, fp := range preFingerprints {
		attrs[fmt.Sprintf("pinned_root_%d_fingerprint", i)] = fp
	}
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindSystem,
		Action:       "device.unpaired",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
		Attributes:   attrs,
	})

	a.writeOK(ctx)
}
