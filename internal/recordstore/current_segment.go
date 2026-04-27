package recordstore

import (
	"sync"
)

// CurrentSegments tracks segment files that are currently being written
// by the recorder, so that callers (notably the API's Recording synthesis
// path) can distinguish a sealed-on-disk segment from an in-flight one.
//
// This is a deliberately narrow registry: the recorder's format writers
// register a segment file when they create it on disk (formatFMP4Segment
// closeCurPart, formatMPEGTSSegment.Write) and unregister it when the
// segment file is Close()d. Lookups answer the binary question "is this
// fpath open right now?" — synthesis at the API layer uses that to mark
// the most-recent Recording per camera as state=active.
//
// Per ADR 0009 §"Recording active-vs-sealed detection" follow-up: the
// recorder is the authoritative side of segment lifecycle, so the
// signal originates here even though the canonical entity (Recording)
// is synthesized at the API layer.
type CurrentSegments struct {
	mu  sync.RWMutex
	set map[string]struct{}
}

// Register marks fpath as currently being written.
func (c *CurrentSegments) Register(fpath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set == nil {
		c.set = make(map[string]struct{})
	}
	c.set[fpath] = struct{}{}
}

// Unregister marks fpath as no longer being written. Safe to call for an
// fpath that was never registered.
func (c *CurrentSegments) Unregister(fpath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.set, fpath)
}

// IsActive returns true iff fpath is currently registered.
func (c *CurrentSegments) IsActive(fpath string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.set[fpath]
	return ok
}

// Snapshot returns a copy of the currently-active segment paths. Mostly
// useful for diagnostics and tests.
func (c *CurrentSegments) Snapshot() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.set))
	for k := range c.set {
		out = append(out, k)
	}
	return out
}

// currentSegments is the package-level singleton. The format writers
// register against it directly; the API consults it via the package
// accessors below.
var currentSegments = &CurrentSegments{}

// RegisterCurrentSegment is the public accessor used by recorder format
// writers to mark a segment file as in-flight.
func RegisterCurrentSegment(fpath string) {
	currentSegments.Register(fpath)
}

// UnregisterCurrentSegment is the public accessor used by recorder format
// writers when a segment file is closed (sealed on disk).
func UnregisterCurrentSegment(fpath string) {
	currentSegments.Unregister(fpath)
}

// IsCurrentSegment reports whether fpath is currently being written.
func IsCurrentSegment(fpath string) bool {
	return currentSegments.IsActive(fpath)
}

// CurrentSegmentSnapshot returns the in-flight segment paths. Test-only
// surface; production callers should use IsCurrentSegment.
func CurrentSegmentSnapshot() []string {
	return currentSegments.Snapshot()
}
