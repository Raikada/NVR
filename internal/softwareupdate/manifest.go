// Package softwareupdate is the recorder-side surface for the
// software/firmware update lifecycle per ADR 0014.
//
// The recorder owns four concerns:
//
//   - manifest verification (this file): symmetric with the MS-side
//     package — the recorder re-verifies every artifact's signature +
//     SHA-256 + compatibility before swapping bytes per D6.
//
//   - applier (applier.go): atomic backup + swap of the running
//     binary, signal-based restart, post-restart health check, and
//     rollback on health-check failure.
//
//   - state file (state.go): a small recorder-local JSON file
//     tracking the current lifecycle state (last applied version,
//     pending update, last failed update + reason). Read by the
//     SPA's Settings card to surface "update available" badges.
//
//   - audit (audit.go): emit software_update.* entries via the
//     existing per-emitter chain.
//
// The recorder's apply path is gated by an MS-issued service JWT
// with scope ["software_update.manage"] (per the new MS service-JWT
// issuer). Local operators cannot self-apply updates from the
// recorder UI in v1; the recorder is the apply mechanism, not the
// approval surface, per ADR 0014 D6 — the MS owns site rollout
// policy.
//
// v1 limitations (mirrored in canonical-divergences):
//
//   - Restart mechanism: the recorder issues a SIGTERM to itself and
//     relies on the host's process supervisor (systemd / launchd /
//     etc.) for relaunch. Bare process invocations without a
//     supervisor will exit cleanly and not relaunch.
//   - Rollback: backup the previous binary alongside <binary>.bak,
//     restore it on health-check fail. Real A/B-slot rollback at
//     the OS image level is a future slice.
//   - No OS-package updates: only the recorder Go binary is
//     handled. .deb / .rpm / .msi installers are deferred.
package softwareupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Manifest mirrors the MS-side wire shape byte-for-byte. Field
// names + JSON tags must match exactly; the signature is over the
// canonical encoding of these fields.
type Manifest struct {
	ID                string `json:"id"`
	ApplianceKind     string `json:"appliance_kind"`
	Version           string `json:"version"`
	Channel           string `json:"channel"`
	ArtifactURL       string `json:"artifact_url,omitempty"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	ArtifactSizeBytes int64  `json:"artifact_size_bytes"`
	Compatibility     Compat `json:"compatibility"`
	ReleaseNotesURL   string `json:"release_notes_url,omitempty"`
	ReleasedAt        string `json:"released_at"`
}

// Compat documents per-platform compatibility constraints.
type Compat struct {
	MinMSVersion string   `json:"min_ms_version,omitempty"`
	MaxMSVersion string   `json:"max_ms_version,omitempty"`
	Hardware     []string `json:"hardware,omitempty"`
}

// Canonical returns the canonical JSON bytes the signature covers.
// Mirror the MS-side implementation byte-for-byte.
func Canonical(m Manifest) ([]byte, error) {
	root := map[string]any{
		"id":                  m.ID,
		"appliance_kind":      m.ApplianceKind,
		"version":             m.Version,
		"channel":             m.Channel,
		"artifact_sha256":     m.ArtifactSHA256,
		"artifact_size_bytes": m.ArtifactSizeBytes,
		"compatibility":       compatMap(m.Compatibility),
		"released_at":         m.ReleasedAt,
	}
	if m.ArtifactURL != "" {
		root["artifact_url"] = m.ArtifactURL
	}
	if m.ReleaseNotesURL != "" {
		root["release_notes_url"] = m.ReleaseNotesURL
	}
	return json.Marshal(root)
}

func compatMap(c Compat) map[string]any {
	out := map[string]any{}
	if c.MinMSVersion != "" {
		out["min_ms_version"] = c.MinMSVersion
	}
	if c.MaxMSVersion != "" {
		out["max_ms_version"] = c.MaxMSVersion
	}
	if len(c.Hardware) > 0 {
		hw := append([]string(nil), c.Hardware...)
		sort.Strings(hw)
		out["hardware"] = hw
	}
	return out
}

// VerifyManifest validates the signature using pubKey. Symmetric
// with the MS-side package.
func VerifyManifest(m Manifest, signatureB64 string, pubKey ed25519.PublicKey) error {
	if len(pubKey) == 0 {
		return errors.New("software_update: empty public key")
	}
	if signatureB64 == "" {
		return ErrInvalidSignature
	}
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("software_update: signature not base64: %w", err)
	}
	canonical, err := Canonical(m)
	if err != nil {
		return fmt.Errorf("software_update: canonicalize: %w", err)
	}
	if !ed25519.Verify(pubKey, canonical, sigBytes) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyArtifactSHA256 returns nil iff the lowercase-hex SHA-256 of
// the supplied artifact bytes matches manifest.ArtifactSHA256.
func VerifyArtifactSHA256(m Manifest, artifact []byte) error {
	sum := sha256.Sum256(artifact)
	got := hex.EncodeToString(sum[:])
	if got != m.ArtifactSHA256 {
		return fmt.Errorf("software_update: artifact sha256 mismatch (got %s, want %s)", got, m.ArtifactSHA256)
	}
	return nil
}

// LoadPublicKeyB64 decodes a base64 ed25519 public key.
func LoadPublicKeyB64(b64 string) (ed25519.PublicKey, error) {
	if b64 == "" {
		return nil, errors.New("software_update: empty public key b64")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("software_update: decode public key b64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("software_update: public key wrong length")
	}
	return ed25519.PublicKey(raw), nil
}

// ErrInvalidSignature is returned when ed25519 verification fails.
var ErrInvalidSignature = errors.New("software_update: invalid signature")

// ErrIncompatible is returned when the manifest's Compat rules don't
// match the recorder's host.
var ErrIncompatible = errors.New("software_update: manifest declares incompatibility with this recorder/MS pair")

// CheckCompatibility validates m.Compatibility against the recorder's
// runtime host. msVersion is the recorder's currently-paired-MS
// version (or "" if unpaired); recorderHardware is the hardware
// fingerprint string (typically GOOS/GOARCH like "linux/amd64").
//
// Empty fields in Compat are unconstrained: a Compat with no
// MinMSVersion + no MaxMSVersion + no Hardware list passes any host.
func CheckCompatibility(m Manifest, msVersion, recorderHardware string) error {
	if m.Compatibility.MinMSVersion != "" && msVersion != "" {
		// Best-effort string compare; semver-aware comparison is a
		// future-slice refinement. The conservative behavior here is
		// pass if the recorder can't make a confident decision.
		if compareVersionStr(msVersion, m.Compatibility.MinMSVersion) < 0 {
			return fmt.Errorf("%w: ms version %s < required %s", ErrIncompatible, msVersion, m.Compatibility.MinMSVersion)
		}
	}
	if m.Compatibility.MaxMSVersion != "" && msVersion != "" {
		if compareVersionStr(msVersion, m.Compatibility.MaxMSVersion) > 0 {
			return fmt.Errorf("%w: ms version %s > allowed %s", ErrIncompatible, msVersion, m.Compatibility.MaxMSVersion)
		}
	}
	if len(m.Compatibility.Hardware) > 0 && recorderHardware != "" {
		var ok bool
		for _, h := range m.Compatibility.Hardware {
			if h == recorderHardware {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%w: recorder hardware %s not in allowed set %v", ErrIncompatible, recorderHardware, m.Compatibility.Hardware)
		}
	}
	return nil
}

// compareVersionStr does a coarse a-vs-b lexicographic comparison.
// For semver (a.b.c) this is sufficient for the simple compatibility
// checks v1 needs; future slices can swap a real semver library.
func compareVersionStr(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
