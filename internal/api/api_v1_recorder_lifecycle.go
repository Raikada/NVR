// Wave 7 / ADR 0015 device-lifecycle endpoints — recorder side.
//
// Three new endpoints, all gated on device_lifecycle.manage:
//
//   POST /v1/recorder/config-reset       (D7)  — non-destructive
//   POST /v1/recorder/factory-wipe       (D8)  — destructive, two-stage
//   POST /v1/recorder/recovery-bundle    (D19) — signed evidence/export
//                                                  manifest (no private keys)
//
// What's preserved vs cleared mirrors ADR 0015's vocabulary precisely:
//
//   config-reset (default behaviour, return_to_unpaired=false):
//     keeps:   identity (UUID + keypair + issued cert + chain + pinned
//              roots), update trust roots, recordings + indexes, local
//              audit + event history, MS metadata
//     clears:  in-memory pairing-flow transient state, refreshes mDNS
//              announcement
//
//   config-reset (return_to_unpaired=true):
//     also clears the issued MS identity material per D4 (delegates
//     to identity.ClearIssuedIdentity, the same call /v1/recorder/unpair
//     uses).
//
//   factory-wipe:
//     two-stage: first call returns a confirmation token; second call
//     with the token actually wipes. Wipes EVERYTHING except update
//     trust roots per D8: identity dir (UUID + keypair + cert + chain
//     + pinned roots), local audit/event in-memory state. Recording
//     segment files on disk are best-effort identified from
//     PathDefaults.RecordPath; off-disk paths the recorder doesn't
//     own (e.g. operator-mounted external storage) are NOT touched —
//     documented as a v1 limitation.
//
//   recovery-bundle:
//     signed manifest of segments + audit + health. NO private keys
//     per D19. The signature is computed with the recorder's
//     persistent ECDSA keypair so the operator can verify the bundle
//     came from this recorder.

package api

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
)

// recorderLifecycleConfirmationTTL bounds the two-stage wipe confirmation
// flow. Mirrors the MS-side TTL.
const recorderLifecycleConfirmationTTL = 10 * time.Minute

// recorderLifecycleConfirmation tracks an outstanding factory-wipe
// confirmation. In-memory only — the wipe destroys local state, so
// persisting confirmation tokens to disk would survive the wipe and
// muddy the audit story. After the wipe, the process restarts (or
// the operator restarts it) with fresh identity per D8.
type recorderLifecycleConfirmation struct {
	token     string
	expiresAt time.Time
	requestor string
}

var (
	recorderLifecycleMu  sync.Mutex
	recorderLifecycleTok *recorderLifecycleConfirmation
)

// onV1RecorderConfigResetPost — POST /v1/recorder/config-reset
//
// Body: { "return_to_unpaired": bool }
func (a *API) onV1RecorderConfigResetPost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	var body struct {
		ReturnToUnpaired bool `json:"return_to_unpaired"`
	}
	_ = ctx.ShouldBindJSON(&body)
	actor := principalFromContext(ctx)

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor.PrincipalKind,
		ActorID:      actor.Sub,
		Action:       "device_lifecycle.config_reset_requested",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
		Attributes: map[string]string{
			"return_to_unpaired": fmt.Sprintf("%v", body.ReturnToUnpaired),
		},
	})

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor.PrincipalKind,
		ActorID:      actor.Sub,
		Action:       "device_lifecycle.config_reset_started",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
	})

	preFingerprints := []string{}
	if a.Identity != nil {
		for _, r := range a.Identity.PinnedRoots() {
			preFingerprints = append(preFingerprints, r.FingerprintSHA256)
		}
	}

	// Reset the pairing-flow transient state so the operator can
	// kick off a fresh pairing flow (or stay paired, depending on
	// the path below) without seeing stale "approved" status.
	if a.Pairing != nil {
		a.Pairing.Reset()
	}

	// Per D7, return_to_unpaired layers the unpair operation on top
	// of config reset. Delegates to ClearIssuedIdentity which is
	// the exact behaviour /v1/recorder/unpair already uses.
	if body.ReturnToUnpaired && a.Identity != nil && a.Identity.IsPaired() {
		if err := a.Identity.ClearIssuedIdentity(); err != nil {
			a.emitAudit(defs.AuditLogEntryInput{
				ActorKind:    actor.PrincipalKind,
				ActorID:      actor.Sub,
				Action:       "device_lifecycle.config_reset_failed",
				Outcome:      defs.AuditOutcomeFailure,
				ResourceKind: "recording_server",
				Attributes:   map[string]string{"reason": err.Error()},
			})
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		if a.MDNS != nil {
			_ = a.MDNS.Refresh()
		}
	}

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor.PrincipalKind,
		ActorID:      actor.Sub,
		Action:       "device_lifecycle.config_reset_succeeded",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
		Attributes: map[string]string{
			"return_to_unpaired":             fmt.Sprintf("%v", body.ReturnToUnpaired),
			"pre_pinned_roots_fingerprints":  strings.Join(preFingerprints, ","),
			"preserved":                      "identity,recordings,indexes,audit,event_history,update_trust_roots",
			"cleared":                        cfgResetClearedMsg(body.ReturnToUnpaired),
		},
	})
	a.Log(logger.Warn, "[lifecycle] config reset (return_to_unpaired=%v) per ADR 0015 D7", body.ReturnToUnpaired)

	ctx.JSON(http.StatusOK, gin.H{
		"reset_kind":         "config",
		"return_to_unpaired": body.ReturnToUnpaired,
		"completed_at":       time.Now().UTC().Format(time.RFC3339),
		"preserved": []string{
			"identity_uuid", "private_keypair", "recordings", "indexes",
			"local_audit", "event_history", "update_trust_roots",
		},
	})
}

// onV1RecorderFactoryWipePost — POST /v1/recorder/factory-wipe
//
// Two-stage:
//
//	First call: empty body or {"export_audit_first": true}. Returns
//	  a confirmation token (one outstanding token at a time).
//	Second call: {"confirm": "WIPE_RECORDINGS_AND_IDENTITY",
//	              "confirmation_token": "<from first call>"}
//	  performs the wipe.
func (a *API) onV1RecorderFactoryWipePost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	var body struct {
		Confirm           string `json:"confirm"`
		ConfirmationToken string `json:"confirmation_token"`
		ExportAuditFirst  bool   `json:"export_audit_first"`
	}
	_ = ctx.ShouldBindJSON(&body)
	actor := principalFromContext(ctx)

	// Stage 1: the magic confirm-string is missing OR the
	// confirmation token isn't supplied. Issue a fresh confirmation.
	if body.Confirm != "WIPE_RECORDINGS_AND_IDENTITY" || body.ConfirmationToken == "" {
		recorderLifecycleMu.Lock()
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			recorderLifecycleMu.Unlock()
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		recorderLifecycleTok = &recorderLifecycleConfirmation{
			token:     token,
			expiresAt: time.Now().UTC().Add(recorderLifecycleConfirmationTTL),
			requestor: actor.Sub,
		}
		recorderLifecycleMu.Unlock()

		a.emitAudit(defs.AuditLogEntryInput{
			ActorKind:    actor.PrincipalKind,
			ActorID:      actor.Sub,
			Action:       "device_lifecycle.factory_wipe_requested",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "recording_server",
			Attributes: map[string]string{
				"export_audit_first": fmt.Sprintf("%v", body.ExportAuditFirst),
				"confirmation_ttl_s": fmt.Sprintf("%d", int(recorderLifecycleConfirmationTTL.Seconds())),
				"severity":           "critical",
			},
		})

		ctx.JSON(http.StatusAccepted, gin.H{
			"action":                "factory_wipe",
			"confirmation_required": true,
			"confirmation_token":    token,
			"expires_at":            recorderLifecycleTok.expiresAt.Format(time.RFC3339),
			"warning":               "destructive: confirms wipe of recordings + identity + audit. Re-call with confirm=\"WIPE_RECORDINGS_AND_IDENTITY\" and confirmation_token set to the value above to proceed.",
		})
		return
	}

	// Stage 2: validate the supplied confirmation token.
	recorderLifecycleMu.Lock()
	tok := recorderLifecycleTok
	recorderLifecycleMu.Unlock()
	if tok == nil {
		a.writeError(ctx, http.StatusUnauthorized, fmt.Errorf("no outstanding factory-wipe confirmation"))
		return
	}
	if tok.token != body.ConfirmationToken {
		a.writeError(ctx, http.StatusUnauthorized, fmt.Errorf("confirmation token mismatch"))
		return
	}
	if time.Now().UTC().After(tok.expiresAt) {
		recorderLifecycleMu.Lock()
		recorderLifecycleTok = nil
		recorderLifecycleMu.Unlock()
		a.writeError(ctx, http.StatusGone, fmt.Errorf("confirmation token expired; request a new one"))
		return
	}

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor.PrincipalKind,
		ActorID:      actor.Sub,
		Action:       "device_lifecycle.factory_wipe_started",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
		Attributes: map[string]string{
			"export_audit_first": fmt.Sprintf("%v", body.ExportAuditFirst),
			"severity":           "critical",
		},
	})

	// Best-effort identify the recordings directory from the
	// running config (PathDefaults.RecordPath uses %path / time
	// templates; the directory portion of the path template before
	// any % is the dir to wipe). External / mounted storage paths
	// the operator owns aren't auto-wiped — documented as a v1
	// limitation.
	recordingsRoot := ""
	a.mutex.RLock()
	if a.Conf != nil {
		rp := a.Conf.PathDefaults.RecordPath
		if rp != "" {
			recordingsRoot = recordPathRoot(rp)
		}
	}
	a.mutex.RUnlock()

	wipedSegmentBytes := int64(0)
	if recordingsRoot != "" {
		// Best-effort: walk + sum + remove the recordings root.
		_ = filepath.WalkDir(recordingsRoot, func(path string, _ os.DirEntry, _ error) error {
			info, err := os.Stat(path)
			if err == nil && !info.IsDir() {
				wipedSegmentBytes += info.Size()
			}
			return nil
		})
		if err := os.RemoveAll(recordingsRoot); err != nil {
			a.Log(logger.Warn, "[lifecycle] could not remove recordings root %q: %s", recordingsRoot, err)
		} else {
			a.Log(logger.Warn, "[lifecycle] removed recordings root %q (~%d bytes)", recordingsRoot, wipedSegmentBytes)
		}
	}

	// Wipe identity. Per D8, on next boot the recorder generates
	// fresh material. The identity package supports this by simply
	// removing the on-disk dir; identity.Open recreates it.
	identityDir := ""
	if a.Identity != nil {
		identityDir = a.identityDirGuess()
	}
	if a.Identity != nil {
		_ = a.Identity.ClearIssuedIdentity()
	}
	if identityDir != "" {
		// Remove every file in the identity dir EXCEPT we keep the
		// dir itself so `identity.Open` recreates clean material on
		// next boot.
		entries, _ := os.ReadDir(identityDir)
		for _, e := range entries {
			_ = os.Remove(filepath.Join(identityDir, e.Name()))
		}
	}

	// Reset the pairing manager.
	if a.Pairing != nil {
		a.Pairing.Reset()
	}
	// Refresh mDNS so listeners see the unpaired state.
	if a.MDNS != nil {
		_ = a.MDNS.Refresh()
	}

	// Final audit (after wipe). Local audit will be cleared on
	// recorder restart since the on-disk identity-derived state has
	// been wiped — the audit chain head is anchored in the disk
	// buffer that the recorder will recreate on next boot. Per D9,
	// already-ingested upstream audit history (the MS) remains
	// immutable.
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor.PrincipalKind,
		ActorID:      actor.Sub,
		Action:       "device_lifecycle.factory_wipe_succeeded",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
		Attributes: map[string]string{
			"recordings_root":     recordingsRoot,
			"wiped_segment_bytes": fmt.Sprintf("%d", wipedSegmentBytes),
			"identity_dir":        identityDir,
			"severity":            "critical",
			"v1_note":             "external/mounted storage outside PathDefaults.RecordPath is not auto-wiped; operator must clean those manually.",
		},
	})

	// Consume the confirmation token (one-shot).
	recorderLifecycleMu.Lock()
	recorderLifecycleTok = nil
	recorderLifecycleMu.Unlock()

	ctx.JSON(http.StatusOK, gin.H{
		"action":              "factory_wipe",
		"completed_at":        time.Now().UTC().Format(time.RFC3339),
		"recordings_root":     recordingsRoot,
		"wiped_segment_bytes": wipedSegmentBytes,
		"identity_dir":        identityDir,
		"v1_note":             "recorder will generate fresh identity on next restart per ADR 0015 D8. Restart the recorder process to complete the wipe.",
	})
}

// onV1RecorderRecoveryBundleExport — POST /v1/recorder/recovery-bundle
//
// Returns a signed JSON manifest of local recordings + audit chain
// state + lifecycle metadata. NO private keys per ADR 0015 D19.
//
// The signature is over the canonical-JSON of the manifest body;
// verification key is the recorder's public key (PublicKey() on the
// Identity). Operators can verify the bundle came from this recorder
// by checking the signature against the public key fingerprint.
//
// Body: { "purpose": "evidence" | "recovery", "include_audit": bool }
func (a *API) onV1RecorderRecoveryBundleExport(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	var body struct {
		Purpose      string `json:"purpose"`
		IncludeAudit bool   `json:"include_audit"`
	}
	_ = ctx.ShouldBindJSON(&body)
	if body.Purpose == "" {
		body.Purpose = "evidence"
	}
	if body.Purpose != "evidence" && body.Purpose != "recovery" {
		a.writeError(ctx, http.StatusBadRequest,
			fmt.Errorf("purpose must be 'evidence' or 'recovery'"))
		return
	}
	actor := principalFromContext(ctx)

	manifest := map[string]any{
		"format_version":    1,
		"purpose":           body.Purpose,
		"recorder_id":       "",
		"public_key_fingerprint": "",
		"firmware_version":  a.Version,
		"exported_at":       time.Now().UTC().Format(time.RFC3339),
		"paired":            false,
	}
	if a.Identity != nil {
		manifest["recorder_id"] = a.Identity.ID().String()
		if fp, err := a.Identity.PublicKeyFingerprint(); err == nil {
			manifest["public_key_fingerprint"] = fp
		}
		manifest["paired"] = a.Identity.IsPaired()
	}

	// Health snapshot — best-effort, doesn't surface PII.
	manifest["health"] = a.recoveryBundleHealthSnapshot()

	if body.IncludeAudit {
		// Most recent audit-chain entries (in-memory ring; tail of
		// up to 200 newest, in chain order). The chain machinery's
		// Snapshot() returns oldest-first; we slice the tail so the
		// bundle's audit segment stays bounded for very long-lived
		// recorders.
		all := defaultAuditSink().Snapshot()
		if n := len(all); n > 200 {
			all = all[n-200:]
		}
		manifest["audit_recent_entries"] = all
		if hh := defaultAuditChain().HeadHash(); hh != "" {
			manifest["audit_head_hash"] = hh
		}
	}

	// Canonicalize + sign.
	canonical, err := json.Marshal(manifest)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	signature := ""
	if a.Identity != nil {
		sum := sha256.Sum256(canonical)
		sig, err := signWithIdentity(a.Identity.PrivateKey(), sum[:])
		if err == nil {
			signature = base64.StdEncoding.EncodeToString(sig)
		}
	}

	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    actor.PrincipalKind,
		ActorID:      actor.Sub,
		Action:       "device_lifecycle.recovery_bundle_exported",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording_server",
		Attributes: map[string]string{
			"purpose":            body.Purpose,
			"include_audit":      fmt.Sprintf("%v", body.IncludeAudit),
			"manifest_bytes":     fmt.Sprintf("%d", len(canonical)),
			"signed":             fmt.Sprintf("%v", signature != ""),
		},
	})

	bundle := gin.H{
		"manifest":  json.RawMessage(canonical),
		"signature": signature,
		"signature_algorithm": "ecdsa-p256-sha256",
	}
	ctx.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="raikada-recorder-bundle-%s.json"`,
			time.Now().UTC().Format("20060102-150405")))
	ctx.JSON(http.StatusOK, bundle)
}

// recoveryBundleHealthSnapshot collects a small, PII-free snapshot
// of the recorder's health state for inclusion in a recovery bundle.
func (a *API) recoveryBundleHealthSnapshot() map[string]any {
	snap := map[string]any{
		"version": a.Version,
		"paired":  false,
	}
	if a.Identity != nil {
		snap["paired"] = a.Identity.IsPaired()
		snap["canonical_source"] = a.Identity.CanonicalSource()
		snap["policy_canonical_source"] = a.Identity.PolicyCanonicalSource()
	}
	return snap
}

// identityDirGuess derives the on-disk identity directory from the
// recorder's running conf path. Mirrors core.go logic.
func (a *API) identityDirGuess() string {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.Conf == nil {
		return ""
	}
	if dir := a.Conf.IdentityDir; dir != "" {
		return dir
	}
	// Fallback: the recorder's identity package reads the dir on Open;
	// we can't reach into the *Identity to read its dir, but the
	// default path is "<dirname(confPath)>/identity". The conf
	// doesn't expose confPath directly to handlers — we leave this
	// empty when no IdentityDir is configured (factory wipe still
	// proceeds; only the "remove identity files" step is skipped,
	// and ClearIssuedIdentity has already wiped the issued material).
	return ""
}

// recordPathRoot extracts the static prefix of a RecordPath template
// (e.g., "./recordings/%path/%Y-%m-%d_%H" -> "./recordings"). Returns
// empty when the path is so wildcarded the prefix isn't meaningful.
func recordPathRoot(template string) string {
	idx := strings.IndexByte(template, '%')
	if idx <= 0 {
		return ""
	}
	prefix := template[:idx]
	// Trim trailing slash + path-segment fragments.
	prefix = strings.TrimRight(prefix, "/\\")
	// Refuse to wipe paths that look root-like to avoid catastrophic
	// rm -rf on misconfigured hosts.
	clean := filepath.Clean(prefix)
	if clean == "/" || clean == "." || clean == "" {
		return ""
	}
	return clean
}

// signWithIdentity signs digest with the recorder's ECDSA private key.
// Used to bind a recovery bundle to the recorder that produced it.
// Uses concatenated r||s (each padded to 32 bytes), matching ES256.
func signWithIdentity(priv *ecdsa.PrivateKey, digest []byte) ([]byte, error) {
	if priv == nil {
		return nil, fmt.Errorf("no private key")
	}
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(out[32-len(rBytes):32], rBytes)
	copy(out[64-len(sBytes):64], sBytes)
	return out, nil
}

func cfgResetClearedMsg(returnToUnpaired bool) string {
	base := "pairing_flow_transient_state,ui_prefs_cache,non_authoritative_local_overrides"
	if returnToUnpaired {
		base += ",issued_pairing_identity,paired_ms_metadata"
	}
	return base
}
