// Package recordcleaner contains the recording cleaner.
package recordcleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

var timeNow = time.Now

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
type Cleaner struct {
	PathConfs map[string]*conf.Path
	Parent    logger.Writer

	ctx       context.Context
	ctxCancel func()

	chReloadConf chan map[string]*conf.Path
	done         chan struct{}
}

// Initialize initializes a Cleaner.
func (c *Cleaner) Initialize() {
	c.ctx, c.ctxCancel = context.WithCancel(context.Background())
	c.chReloadConf = make(chan map[string]*conf.Path)
	c.done = make(chan struct{})

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

	for {
		select {
		case <-time.After(c.cleanInterval()):
			c.doRun()

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
