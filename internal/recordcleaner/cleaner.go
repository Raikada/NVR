// Package recordcleaner contains the recording cleaner.
package recordcleaner

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

var timeNow = time.Now

// capacityProbeInterval is how often the cleaner samples per-volume
// disk usage to emit storage.volume_full / storage.volume_degraded
// transition events. Held as a package-level var (not a const) so
// tests can shorten it without exposing the knob through the public
// Cleaner shape.
var capacityProbeInterval = 60 * time.Second

// volumeFullThresholdPct is the used-percentage at or above which a
// volume crosses into the storage.volume_full state. The threshold is
// intentionally tighter than api_v1_storage_volumes.go's 99% "full"
// status so the event fires before the read-side surface flips —
// operators get warning before the recorder stops accepting writes.
const volumeFullThresholdPct = 95.0

// statfsFn is the seam tests use to mock filesystem stats without
// touching real disks. The default delegates to syscall.Statfs.
var statfsFn = func(path string, st *syscall.Statfs_t) error {
	return syscall.Statfs(path, st)
}

// SegmentPinPredicate reports whether an on-disk segment path is
// pinned by an external owner (today: a Clip whose state is
// requested/preparing/ready). When set, the cleaner consults this
// before removing any segment file. nil means "no external pin
// authority" — the cleaner falls back to its policy-only behavior.
//
// The hook is intentionally a single package-level function variable
// rather than an interface or constructor parameter so its addition
// is strictly additive (no Cleaner-struct shape change, no callsite
// migration in core). The internal/api package registers a
// predicate at startup that checks the process-wide ClipStore; tests
// can override it.
//
// Per platform/docs/domain-model.md Clip notes: "the recorder MUST
// NOT prune any RecordingSegment referenced by a clip whose state is
// requested, preparing, or ready, even when the segment is older
// than the policy retention window."
var SegmentPinPredicate func(segmentPath string) bool

// Cleaner removes expired recording segments from disk.
//
// Beyond segment cleanup, the Cleaner runs a periodic capacity probe
// over the volumes its path table refers to and emits canonical
// storage.volume_full / storage.volume_degraded Events when a volume
// transitions into or out of those states. The publisher functions
// are injected (PublishVolumeFull / PublishVolumeDegraded) so this
// package does not import internal/api — matching the dependency
// direction wired in core.go. When unset, capacity-probe emission is
// skipped silently; tests that want to exercise the threshold logic
// supply test publishers directly.
type Cleaner struct {
	PathConfs map[string]*conf.Path
	Parent    logger.Writer

	// PublishVolumeFull is called once per volume the moment its used
	// fraction crosses volumeFullThresholdPct. Once the transition is
	// emitted the Cleaner suppresses re-emission until the volume
	// drops back below the threshold. Optional — when nil, the
	// capacity probe runs but emits nothing.
	PublishVolumeFull func(volumeID, mountPath string)

	// PublishVolumeDegraded is called once per volume the moment it
	// enters a degraded or read-only status. Reasoning string is
	// passed through to the publisher's reason attribute. Optional.
	PublishVolumeDegraded func(volumeID, mountPath, reason string)

	ctx       context.Context
	ctxCancel func()

	chReloadConf chan map[string]*conf.Path
	done         chan struct{}

	// volumeStateMu guards volumeFullState and volumeDegradedState.
	// The capacity probe and Close race only on shutdown, but reading
	// state in tests via test-only accessors needs the lock too.
	volumeStateMu       sync.Mutex
	volumeFullState     map[string]bool // mountPath -> currently full
	volumeDegradedState map[string]bool // mountPath -> currently degraded
}

// Initialize initializes a Cleaner.
func (c *Cleaner) Initialize() {
	c.ctx, c.ctxCancel = context.WithCancel(context.Background())
	c.chReloadConf = make(chan map[string]*conf.Path)
	c.done = make(chan struct{})
	c.volumeFullState = map[string]bool{}
	c.volumeDegradedState = map[string]bool{}

	go c.run()
}

// Close closes the Cleaner.
func (c *Cleaner) Close() {
	c.ctxCancel()
	<-c.done
}

// Log implements logger.Writer.
func (c *Cleaner) Log(level logger.Level, format string, args ...any) {
	c.Parent.Log(level, "[record cleaner]"+format, args...)
}

// ReloadPathConfs is called by core.Core.
func (c *Cleaner) ReloadPathConfs(pathConfs map[string]*conf.Path) {
	select {
	case c.chReloadConf <- pathConfs:
	case <-c.ctx.Done():
	}
}

func (c *Cleaner) run() {
	defer close(c.done)

	c.doRun() //nolint:errcheck
	c.probeCapacity()

	probe := time.NewTicker(capacityProbeInterval)
	defer probe.Stop()

	for {
		select {
		case <-time.After(c.cleanInterval()):
			c.doRun()

		case <-probe.C:
			c.probeCapacity()

		case cnf := <-c.chReloadConf:
			c.PathConfs = cnf

		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Cleaner) cleanInterval() time.Duration {
	interval := 30 * 60 * time.Second

	for _, e := range c.PathConfs {
		if e.RecordDeleteAfter != 0 &&
			interval > (time.Duration(e.RecordDeleteAfter)/2) {
			interval = time.Duration(e.RecordDeleteAfter) / 2
		}
	}

	return interval
}

func (c *Cleaner) doRun() {
	now := timeNow()

	pathNames := recordstore.FindAllPathsWithSegments(c.PathConfs)

	for _, pathName := range pathNames {
		c.processPath(now, pathName) //nolint:errcheck
	}
}

func (c *Cleaner) processPath(now time.Time, pathName string) error {
	pathConf, _, err := conf.FindPathConf(c.PathConfs, pathName)
	if err != nil {
		return err
	}

	if pathConf.RecordDeleteAfter == 0 {
		return nil
	}

	err = c.deleteExpiredSegments(now, pathName, pathConf)
	if err != nil {
		return err
	}

	c.deleteEmptyDirs(pathConf)

	return nil
}

func (c *Cleaner) deleteExpiredSegments(now time.Time, pathName string, pathConf *conf.Path) error {
	end := now.Add(-time.Duration(pathConf.RecordDeleteAfter))
	segments, err := recordstore.FindSegments(pathConf, pathName, nil, &end)
	if err != nil {
		return err
	}

	for _, seg := range segments {
		// Pin check per Clip canonical-entity rules: an external pin
		// holder (e.g., a clip in requested/preparing/ready state)
		// vetoes pruning of its source segments even when policy
		// retention has expired. See SegmentPinPredicate doc above
		// and platform/docs/domain-model.md Clip Notes.
		if SegmentPinPredicate != nil && SegmentPinPredicate(seg.Fpath) {
			c.Log(logger.Debug, "skipping pinned segment %s", seg.Fpath)
			continue
		}
		c.Log(logger.Debug, "removing %s", seg.Fpath)
		os.Remove(seg.Fpath)
	}

	return nil
}

// volumeRootForRecordPath mirrors api_v1_storage_volumes.go's helper
// of the same name. Reimplemented here (rather than imported) so
// recordcleaner doesn't take a dependency on internal/api — the api
// package is wired-in above this layer in the import graph; calling
// up to it would invert the direction.
func volumeRootForRecordPath(recordPath string) string {
	if recordPath == "" {
		return ""
	}
	recordPath = strings.TrimRight(recordPath, "/")
	parts := strings.Split(recordPath, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.Contains(p, "%") {
			break
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return ""
	}
	root := strings.Join(out, "/")
	if root == "" {
		return "/"
	}
	return root
}

// volumeIDFromMountPath mirrors api_v1_storage_volumes.go's helper of
// the same name. The two stay in lockstep so an Event's subject_id
// matches the canonical volume id served at /v1/storage-volumes.
func volumeIDFromMountPath(mountPath string) string {
	abs, _ := filepath.Abs(mountPath)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("storage-volume|"+abs)).String()
}

// volumeMountPaths walks PathConfs and returns the deterministically-
// ordered set of absolute volume roots, deduped. Mirrors the head of
// api_v1_storage_volumes.go::collectStorageVolumes — minus the wire-
// shape construction the api package layers on top.
func (c *Cleaner) volumeMountPaths() []string {
	roots := map[string]struct{}{}
	for _, p := range c.PathConfs {
		if p == nil {
			continue
		}
		root := volumeRootForRecordPath(p.RecordPath)
		if root == "" {
			continue
		}
		abs, _ := filepath.Abs(root)
		roots[abs] = struct{}{}
	}
	if len(roots) == 0 {
		return nil
	}
	out := make([]string, 0, len(roots))
	for r := range roots {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// probeCapacity samples each volume's used fraction and emits the
// transition events when a volume crosses into (or out of) the full
// or degraded state. Once-per-state-change discipline lives here:
// volumeFullState / volumeDegradedState track whether the cleaner
// has already announced the volume's current state, so the periodic
// probe doesn't re-emit on every tick.
//
// "Degraded" here is detected indirectly: the recorder doesn't track
// a real read-only mount flag (the api/ layer's Status field is a
// derivation of the same statfs sample), so this layer treats a
// volume as degraded when its used fraction is in the warning band
// (>= 90% but < volumeFullThresholdPct) or when statfs fails outright.
// Either signal is what an operator wants to know about.
func (c *Cleaner) probeCapacity() {
	for _, mountPath := range c.volumeMountPaths() {
		full, degraded, reason := c.sampleVolume(mountPath)
		c.applyVolumeTransition(mountPath, full, degraded, reason)
	}
}

// sampleVolume returns the (full, degraded, reason) triple for one
// volume. reason is empty unless degraded is true; tests that drive
// the helper directly bypass statfs by replacing statfsFn.
func (c *Cleaner) sampleVolume(mountPath string) (full, degraded bool, reason string) {
	var st syscall.Statfs_t
	if err := statfsFn(mountPath, &st); err != nil {
		// A recordings directory that doesn't exist yet (fresh install,
		// nothing recorded) is not a degraded volume — the recorder
		// creates it at first segment write (F10).
		if errors.Is(err, syscall.ENOENT) {
			return false, false, ""
		}
		return false, true, "statfs_failed"
	}
	bsize := int64(st.Bsize)
	if bsize <= 0 {
		bsize = 4096
	}
	capacity := int64(st.Blocks) * bsize //nolint:gosec
	free := int64(st.Bavail) * bsize     //nolint:gosec
	if free > capacity {
		free = capacity
	}
	if capacity <= 0 {
		return false, false, ""
	}
	used := capacity - free
	if used < 0 {
		used = 0
	}
	usedPct := float64(used) / float64(capacity) * 100.0
	switch {
	case usedPct >= volumeFullThresholdPct:
		return true, false, ""
	case usedPct >= 90.0:
		return false, true, "capacity_headroom_low"
	}
	return false, false, ""
}

// applyVolumeTransition compares the freshly-sampled state for one
// volume against the cleaner's remembered state and fires the
// publisher exactly once on each rising edge. Falling edges (volume
// recovers) clear the flag silently — there is no canonical
// "volume_recovered" Event today, so the cleaner just resets so the
// next rising edge re-fires.
func (c *Cleaner) applyVolumeTransition(mountPath string, full, degraded bool, reason string) {
	c.volumeStateMu.Lock()
	wasFull := c.volumeFullState[mountPath]
	wasDegraded := c.volumeDegradedState[mountPath]
	c.volumeFullState[mountPath] = full
	c.volumeDegradedState[mountPath] = degraded
	c.volumeStateMu.Unlock()

	volumeID := volumeIDFromMountPath(mountPath)

	if full && !wasFull {
		if c.PublishVolumeFull != nil {
			c.PublishVolumeFull(volumeID, mountPath)
		}
		c.Log(logger.Warn, "storage volume full: %s", mountPath)
	}
	if degraded && !wasDegraded {
		if c.PublishVolumeDegraded != nil {
			c.PublishVolumeDegraded(volumeID, mountPath, reason)
		}
		c.Log(logger.Warn, "storage volume degraded: %s (%s)", mountPath, reason)
	}
}

func (c *Cleaner) deleteEmptyDirs(pathConf *conf.Path) {
	recordPath := strings.ReplaceAll(pathConf.RecordPath, "%path", pathConf.Name)
	commonPath := recordstore.CommonPath(recordPath)

	filepath.WalkDir(commonPath, func(fpath string, info fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil {
			return err
		}

		if info.IsDir() {
			os.Remove(fpath)
		}

		return nil
	})
}
