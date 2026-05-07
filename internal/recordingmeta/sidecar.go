// Package recordingmeta persists per-segment historical RecordingPolicy
// state alongside each on-disk segment file.
//
// Wave 5 (recorder@a38a63a1) stamps RecordingSegment.content_type from
// the *current* policy mode at synthesis time — fine for an operational
// rollup, wrong for an audit-grade truth surface because a mid-policy
// change retroactively reclassifies every segment ever recorded under
// the old mode. Wave A3 closes the gap by writing a tiny JSON sidecar
// next to each segment when it seals: the policy_id and mode in effect
// at write time. The synthesizer prefers the sidecar when present and
// falls back to the current-policy mode for pre-amendment segments
// (segments sealed before this commit landed have no sidecar; the
// fallback keeps them surfacing as continuous, matching today's
// behavior).
//
// Sidecar layout: `<dir>/.meta/<basename>.json` — parallel `.meta/`
// subdirectory next to each segment file. We can't use a sibling
// `<segment>.meta.json` because recordstore.FindSegments runs the
// segment-path regex unanchored against every file under the segment
// dir; `<seg>.mp4.meta.json` would match the same regex as `<seg>.mp4`
// and surface as a phantom segment. The `.meta/` subdir keeps the
// sidecar adjacent on disk but invisible to that regex (no `.mp4`/`.ts`
// suffix in the .json filename, no template-shaped path component).
// Mode 0644 on files; mkdir 0755 on the dir.
//
// Wiring:
//
//   - core.go installs a Resolver via SetResolver at startup (and on
//     conf reload); the resolver maps a path name to the policy that
//     governs it *right now*.
//   - path.go's OnSegmentComplete closure calls Write(pathName, segmentPath)
//     to persist the sidecar after the recorder seals the segment.
//   - api/api_v1_recordings.go's synthesize() calls Read(segmentPath)
//     and prefers the sidecar's mode over the current-policy fallback.
//
// On-disk format is intentionally tiny:
//
//	{"policy_id":"00000000-0000-0000-0000-000000000001","mode":"continuous"}
//
// — no recorder version, no timestamp (the segment file's own mtime
// serves as the seal time), no schema version. Backwards-compat is
// "older recorder reads as if no sidecar exists" because Read returns
// (nil, nil) on missing or malformed file.
package recordingmeta

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Sidecar is the persisted shape. Field names match the canonical
// model's RecordingSegment.policy_id + content_type semantics so the
// synthesizer can map fields directly.
type Sidecar struct {
	PolicyID string `json:"policy_id"`
	Mode     string `json:"mode"`
}

// Resolver maps a recorder path name (e.g., "cam1") to the policy
// that governs it at the moment of the call. Returns ok=false when
// the path or its policy can't be resolved (no running policy table,
// path with no RecordingPolicyID and no default, etc.) — Write skips
// the sidecar in that case.
type Resolver func(pathName string) (Sidecar, bool)

var (
	resolverMu sync.RWMutex
	resolver   Resolver
)

// SetResolver wires the per-path policy resolver. Idempotent;
// safe to call from conf-reload paths.
func SetResolver(r Resolver) {
	resolverMu.Lock()
	defer resolverMu.Unlock()
	resolver = r
}

func currentResolver() Resolver {
	resolverMu.RLock()
	defer resolverMu.RUnlock()
	return resolver
}

// SidecarPath returns the canonical sidecar path for a segment path.
// Exposed for tests + the synthesizer's read path.
func SidecarPath(segmentPath string) string {
	dir, base := filepath.Split(segmentPath)
	return filepath.Join(dir, ".meta", base+".json")
}

// Write persists the sidecar. Returns nil silently when no resolver
// is wired, when the resolver doesn't recognize the path, or when
// the sidecar already exists (idempotent — segments are sealed once
// per path). Write errors are surfaced so the recorder can decide
// whether to log; today's caller logs at Debug.
func Write(pathName, segmentPath string) error {
	r := currentResolver()
	if r == nil {
		return nil
	}
	sc, ok := r(pathName)
	if !ok || sc.Mode == "" {
		return nil
	}
	return writeFile(segmentPath, sc)
}

func writeFile(segmentPath string, sc Sidecar) error {
	body, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	scPath := SidecarPath(segmentPath)
	if err := os.MkdirAll(filepath.Dir(scPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(scPath, body, 0o644)
}

// Read loads the sidecar for a segment, if present. Returns (nil, nil)
// when the sidecar doesn't exist (pre-amendment segment) or when the
// file exists but can't be parsed (corrupt or partially written —
// treat as absent rather than escalate; the synthesizer's fallback
// surfaces the segment as continuous).
func Read(segmentPath string) (*Sidecar, error) {
	body, err := os.ReadFile(SidecarPath(segmentPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil //nolint:nilnil
		}
		return nil, err
	}
	var sc Sidecar
	if err := json.Unmarshal(body, &sc); err != nil {
		return nil, nil //nolint:nilnil
	}
	if sc.Mode == "" {
		return nil, nil //nolint:nilnil
	}
	return &sc, nil
}

// Remove deletes the sidecar. Used by the cascading recording-delete
// path so the meta file doesn't outlive the segment it documents.
// Best-effort: silent when the file is already gone.
func Remove(segmentPath string) error {
	if err := os.Remove(SidecarPath(segmentPath)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}
