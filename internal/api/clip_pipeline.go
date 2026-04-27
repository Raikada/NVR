// Package api: clip preparation pipeline per ADR 0009 §D5 follow-up.
//
// The recorder owns clip preparation (see ARCHITECTURE.md §5 item 6).
// Preparation is asynchronous and idempotent: POST /v1/clips creates
// the clip in `requested` state, this pipeline picks it up, transitions
// to `preparing`, stitches the source segments into a single export
// artifact, and on success transitions to `ready`. On failure the
// clip moves to `failed` with `failure_reason` populated.
//
// Stitching strategy. The current build concatenates the on-disk
// fmp4-fragment segment files byte-wise into a single output file. A
// fully canonical fmp4-to-mp4 remux would require pulling apart each
// fragment's `moof` boxes, recomputing baseMediaDecodeTime against a
// new movie-fragment timeline, and emitting a single `moov` —
// which is a non-trivial amount of new mux code (mediacommon's mp4
// helpers don't expose a one-call remux). We take the simpler concat
// path here per the spec's explicit fallback authorization, document
// it as a known limitation on the response surface (`Notice` field),
// and leave the proper remux for a follow-up. See AGENTS.md §6 — this
// pipeline READS from recordstore and writes a NEW export artifact;
// it does not refactor or alter codec/container/segmenting defaults.
package api //nolint:revive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

// clipExportSubdir is the per-volume subdirectory the recorder
// writes stitched clip artifacts into. Per the platform spec the
// path is `<volume.mount_path>/clips/<clip-uuid>.mp4`; the
// "<volume.mount_path>" bit is derived from the recordstore's
// configured record path tree (volumeRootForRecordPath).
const clipExportSubdir = "clips"

// segmentLookup is the per-segment metadata the preparation
// pipeline needs from a recordstore walk.
type segmentLookup struct {
	id       string
	path     string
	started  time.Time
	cameraID string
}

// findSegmentsForClip walks recordstore for the given camera and
// returns the segments whose start time falls within the clip's
// requested range. Used both by POST (to populate
// clip.SourceSegmentIDs / pin paths up-front) and by the
// preparation worker (to do the actual stitch).
//
// The clip's requested range is treated as half-open
// [range_started_at, range_ended_at]. The lookup is intentionally
// inclusive on the start side: a segment whose start time equals
// `range_started_at` is included, and a segment whose start time is
// before `range_ended_at` is included. This mirrors the existing
// playback semantics in recordstore.FindSegments.
func findSegmentsForClip(
	pathConfs map[string]*conf.Path,
	pathName string,
	cameraID string,
	rangeStart, rangeEnd time.Time,
) ([]segmentLookup, error) {
	pathConf, _, err := conf.FindPathConf(pathConfs, pathName)
	if err != nil {
		return nil, fmt.Errorf("path lookup failed: %w", err)
	}

	segs, err := recordstore.FindSegments(pathConf, pathName, &rangeStart, &rangeEnd)
	if err != nil {
		return nil, err
	}

	// recordstore.FindSegments returns "the segment that may contain
	// the start of the playback" plus everything after, which can
	// include segments that themselves start after rangeEnd or — in
	// the "all segments older than start" fallback — a single old
	// segment that doesn't actually overlap. Apply a strict
	// [rangeStart, rangeEnd) overlap filter here. We treat the
	// segment as covering [start, +inf) since the recordstore public
	// surface doesn't expose per-segment ends; a future field on
	// RecordingSegment.EndedAt would tighten this.
	out := make([]segmentLookup, 0, len(segs))
	for _, s := range segs {
		// Drop segments that start at or after rangeEnd (no overlap).
		if !s.Start.Before(rangeEnd) {
			continue
		}
		// Drop the recordstore "fallback last segment" when it's
		// strictly older than rangeStart and there's no following
		// segment that brings coverage forward into the range.
		// Approximation: if it's the only result and starts before
		// rangeStart by more than the typical max segment length,
		// it's almost certainly not covering rangeStart. We use a
		// generous 1h cap so reasonable segment lengths (1m–10m per
		// RecordingPolicy) are kept.
		if s.Start.Before(rangeStart) && rangeStart.Sub(s.Start) > time.Hour {
			continue
		}
		out = append(out, segmentLookup{
			id:       recordingSegmentIDFor(cameraID, s.Fpath),
			path:     s.Fpath,
			started:  s.Start,
			cameraID: cameraID,
		})
	}
	return out, nil
}

// volumeRootForPath returns the deepest no-format-token prefix of a
// recordstore record-path string, used as the volume mount-path
// proxy for clip-export placement. Mirrors the existing helper in
// api_v1_storage_volumes.go without re-exporting it; we duplicate
// rather than reach into that file because it's a tiny path utility
// and binding the two helpers tightly increases risk of accidental
// drift across the storage-volumes / clips boundary.
func volumeRootForPathClip(recordPath string) string {
	// Walk from the end backwards splitting on '/' until we find a
	// segment with no '%' format token; that prefix is the volume
	// root. If every segment contains '%' (degenerate config), fall
	// back to "/".
	dir := recordPath
	for {
		base := filepath.Base(dir)
		if base == dir || base == "/" || base == "." {
			break
		}
		if !containsRune(base, '%') {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return "/"
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// resolveClipExportDir returns the absolute directory the export
// artifact for the given path-name's volume should be written into.
// Created on first use; idempotent.
func resolveClipExportDir(pathConfs map[string]*conf.Path, pathName string) (string, error) {
	pathConf, _, err := conf.FindPathConf(pathConfs, pathName)
	if err != nil {
		return "", fmt.Errorf("path lookup failed: %w", err)
	}
	recordPath := pathConf.RecordPath
	if recordPath == "" {
		return "", fmt.Errorf("path %q has no recordPath configured", pathName)
	}
	root := volumeRootForPathClip(recordPath)
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("absolutize volume root: %w", err)
	}
	dir := filepath.Join(abs, clipExportSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir clip export dir: %w", err)
	}
	return dir, nil
}

// stitchSegments concatenates the supplied segment files into one
// output file at outPath. Returns the byte size and sha256 checksum
// of the produced file.
//
// Implementation note: this is the simplified concat strategy
// described at the top of the file. fmp4 fragments concatenated
// byte-wise are commonly playable by ffmpeg-class players because
// each fragment is self-describing; standard QuickTime-style
// players may need an explicit remux. The limitation is documented
// in the POST response and on the prepared clip via the spec
// `notice` surface.
func stitchSegments(segPaths []string, outPath string) (int64, string, error) {
	out, err := os.Create(outPath)
	if err != nil {
		return 0, "", fmt.Errorf("create export file: %w", err)
	}
	defer out.Close() //nolint:errcheck

	hasher := sha256.New()
	multi := io.MultiWriter(out, hasher)

	var total int64
	for _, p := range segPaths {
		f, err := os.Open(p)
		if err != nil {
			return 0, "", fmt.Errorf("open segment %s: %w", p, err)
		}
		n, err := io.Copy(multi, f)
		f.Close() //nolint:errcheck
		if err != nil {
			return 0, "", fmt.Errorf("copy segment %s: %w", p, err)
		}
		total += n
	}

	if err := out.Sync(); err != nil {
		return 0, "", fmt.Errorf("sync export file: %w", err)
	}

	return total, "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// clipPreparer drives one clip from `requested` to `ready` (or
// `failed`). Constructed and started by the POST handler.
type clipPreparer struct {
	store     *ClipStore
	clipID    string
	pathName  string
	pathConfs map[string]*conf.Path
}

// prepare runs synchronously; it is invoked from a goroutine spawned
// in the POST handler. The whole pipeline holds no API-level locks;
// the ClipStore guards its own state.
func (p *clipPreparer) prepare() {
	// Transition requested → preparing.
	clip, ok := p.store.Update(p.clipID, func(c *defs.Clip) {
		if c.State != defs.ClipStateRequested {
			return
		}
		c.State = defs.ClipStatePreparing
		c.UpdatedAt = nowUTC()
	})
	if !ok || clip.State != defs.ClipStatePreparing {
		return
	}

	exportDir, err := resolveClipExportDir(p.pathConfs, p.pathName)
	if err != nil {
		p.fail(fmt.Sprintf("resolve export dir: %s", err.Error()))
		return
	}

	segs, err := findSegmentsForClip(
		p.pathConfs,
		p.pathName,
		clip.CameraID,
		clip.RangeStartedAt,
		clip.RangeEndedAt,
	)
	if err != nil {
		p.fail(fmt.Sprintf("find segments: %s", err.Error()))
		return
	}
	if len(segs) == 0 {
		p.fail("no segments overlap requested range")
		return
	}

	paths := make([]string, len(segs))
	for i, s := range segs {
		paths[i] = s.path
	}

	outPath := filepath.Join(exportDir, clip.ID+".mp4")
	size, checksum, err := stitchSegments(paths, outPath)
	if err != nil {
		// Best-effort cleanup of a partial output file.
		os.Remove(outPath) //nolint:errcheck
		p.fail(fmt.Sprintf("stitch: %s", err.Error()))
		return
	}

	now := nowUTC()
	p.store.Update(p.clipID, func(c *defs.Clip) {
		c.State = defs.ClipStateReady
		c.PreparedAt = &now
		c.ExportPath = outPath
		c.SizeBytes = &size
		cs := checksum
		c.Checksum = &cs
		c.UpdatedAt = now
	})
}

// fail transitions the clip to failed with the given reason.
func (p *clipPreparer) fail(reason string) {
	now := nowUTC()
	r := reason
	p.store.Update(p.clipID, func(c *defs.Clip) {
		c.State = defs.ClipStateFailed
		c.FailureReason = &r
		c.UpdatedAt = now
	})
}

// preparerWG is held only by tests that need to wait for in-flight
// preparers to finish before assertions. Production code does not
// gate on this — preparation is fire-and-forget per the async
// contract in domain-model.md.
var preparerWG sync.WaitGroup

// runPreparerAsync spawns a goroutine running the preparer; tests
// can call preparerWG.Wait() to drain.
func runPreparerAsync(p *clipPreparer) {
	preparerWG.Add(1)
	go func() {
		defer preparerWG.Done()
		p.prepare()
	}()
}
