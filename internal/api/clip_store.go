// Package api: in-memory Clip store per ADR 0009 §D5 follow-up.
//
// The Clip canonical entity (defined in
// platform/docs/domain-model.md) is recorder-authoritative for
// preparation: stitching source segments, writing an export artifact,
// serving download. This in-memory store holds the recorder's
// authoritative view; cross-restart persistence is out of scope for
// this pre-MS phase, mirroring the same trade-off the synthesized
// Recording registry takes.
//
// Pinning. The store maintains a refcount of `RecordingSegment` ids
// (and their on-disk paths) referenced by clips whose state is
// `requested`, `preparing`, or `ready`. The recordcleaner consults
// the API-level `IsSegmentPathPinned` query to skip pinned segments.
package api //nolint:revive

import (
	"sync"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/recordcleaner"
)

// recordcleanerSetSegmentPinPredicate installs the package-wide
// predicate the recordcleaner consults when deciding whether to
// prune a segment. Wrapped to keep the "import recordcleaner only
// for the predicate hook" surface centralized in this file.
func recordcleanerSetSegmentPinPredicate(p func(string) bool) {
	recordcleaner.SegmentPinPredicate = p
}

// ClipStore is the recorder's in-memory authoritative view of Clips.
// All exported methods are safe for concurrent use.
type ClipStore struct {
	mu sync.RWMutex
	// items keyed by clip id.
	items map[string]*defs.Clip
	// pinnedSegmentIDs[segID] = number of active clips holding it.
	pinnedSegmentIDs map[string]int
	// pinnedPaths[diskPath] = number of active clips holding it.
	pinnedPaths map[string]int
	// segmentIDsByClip / pathsByClip mirror the per-clip pin
	// contributions so Put/Delete can release them precisely.
	segmentIDsByClip map[string][]string
	pathsByClip      map[string][]string
}

// NewClipStore constructs an empty ClipStore.
func NewClipStore() *ClipStore {
	return &ClipStore{
		items:            make(map[string]*defs.Clip),
		pinnedSegmentIDs: make(map[string]int),
		pinnedPaths:      make(map[string]int),
		segmentIDsByClip: make(map[string][]string),
		pathsByClip:      make(map[string][]string),
	}
}

// activeForPinning reports whether a clip's state contributes to
// segment pinning. requested, preparing, ready hold pins; expired,
// failed, deleted release them.
func activeForPinning(s defs.ClipState) bool {
	switch s {
	case defs.ClipStateRequested,
		defs.ClipStatePreparing,
		defs.ClipStateReady:
		return true
	}
	return false
}

// Put inserts or replaces a clip and updates the pin refcounts to
// reflect the new state. `segmentPaths` is the on-disk path list for
// the source segments (parallel to clip.SourceSegmentIDs). The store
// is the authority on pin state — callers must not mutate the
// refcount tables directly.
func (s *ClipStore) Put(clip *defs.Clip, segmentPaths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releasePinsLocked(clip.ID)
	c := *clip
	s.items[clip.ID] = &c
	if activeForPinning(c.State) {
		ids := make([]string, len(c.SourceSegmentIDs))
		copy(ids, c.SourceSegmentIDs)
		paths := make([]string, len(segmentPaths))
		copy(paths, segmentPaths)
		for _, id := range ids {
			s.pinnedSegmentIDs[id]++
		}
		for _, p := range paths {
			s.pinnedPaths[p]++
		}
		s.segmentIDsByClip[c.ID] = ids
		s.pathsByClip[c.ID] = paths
	}
}

// Get returns a copy of the clip with the given id.
func (s *ClipStore) Get(id string) (defs.Clip, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.items[id]
	if !ok {
		return defs.Clip{}, false
	}
	return *c, true
}

// Snapshot returns a copy of every clip. Callers may filter, sort,
// and paginate without holding the lock.
func (s *ClipStore) Snapshot() []defs.Clip {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]defs.Clip, 0, len(s.items))
	for _, c := range s.items {
		out = append(out, *c)
	}
	return out
}

// Delete removes a clip from the store and releases its pins.
// Returns the prior clip and true if found.
func (s *ClipStore) Delete(id string) (defs.Clip, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return defs.Clip{}, false
	}
	prev := *c
	s.releasePinsLocked(id)
	delete(s.items, id)
	return prev, true
}

// IsSegmentPathPinned reports whether any active clip references the
// given on-disk segment path. Used by the recordcleaner pinning hook.
func (s *ClipStore) IsSegmentPathPinned(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pinnedPaths[path] > 0
}

// IsSegmentIDPinned reports whether any active clip references the
// given canonical segment id.
func (s *ClipStore) IsSegmentIDPinned(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pinnedSegmentIDs[id] > 0
}

// Update applies a mutator to the clip in place under the store
// lock. Returns the updated copy and true if found. The mutator MUST
// NOT mutate SourceSegmentIDs (pin tables would drift); use Put for
// that. State transitions are the expected use case.
//
// Pin tables are reconciled after the mutation: if the new state
// drops out of the active set, the clip's pins are released.
func (s *ClipStore) Update(id string, mut func(*defs.Clip)) (defs.Clip, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return defs.Clip{}, false
	}
	wasActive := activeForPinning(c.State)
	mut(c)
	isActive := activeForPinning(c.State)
	if wasActive && !isActive {
		s.releasePinsLocked(id)
	}
	return *c, true
}

// releasePinsLocked drops any pins this clip currently holds. Caller
// holds s.mu. The clip itself stays in s.items unless the caller
// also deletes it.
func (s *ClipStore) releasePinsLocked(id string) {
	if ids, ok := s.segmentIDsByClip[id]; ok {
		for _, segID := range ids {
			if s.pinnedSegmentIDs[segID] > 1 {
				s.pinnedSegmentIDs[segID]--
			} else {
				delete(s.pinnedSegmentIDs, segID)
			}
		}
		delete(s.segmentIDsByClip, id)
	}
	if paths, ok := s.pathsByClip[id]; ok {
		for _, p := range paths {
			if s.pinnedPaths[p] > 1 {
				s.pinnedPaths[p]--
			} else {
				delete(s.pinnedPaths, p)
			}
		}
		delete(s.pathsByClip, id)
	}
}

// clipStoreSingleton is the process-wide store the recordcleaner
// consults via IsRecordingSegmentPinnedByClip. The /v1/clips
// handlers and the recordcleaner share this singleton; tests that
// want isolation construct a per-test store and inject it onto the
// API.
var (
	clipStoreSingletonOnce sync.Once
	clipStoreSingleton     *ClipStore
)

// defaultClipStore returns the process-wide ClipStore, lazily
// constructed on first use.
func defaultClipStore() *ClipStore {
	clipStoreSingletonOnce.Do(func() {
		clipStoreSingleton = NewClipStore()
	})
	return clipStoreSingleton
}

// IsRecordingSegmentPinnedByClip is the package-level entry point
// the recordcleaner consults to skip pinned segments. It checks the
// process-wide ClipStore singleton.
//
// Returns true if any active clip references the given on-disk
// segment path.
func IsRecordingSegmentPinnedByClip(segmentPath string) bool {
	return defaultClipStore().IsSegmentPathPinned(segmentPath)
}

// init wires the recordcleaner's pin predicate to the process-wide
// ClipStore. Done at package init rather than core-startup so the
// predicate is in place before the cleaner runs its first sweep,
// even when api package isn't otherwise initialized (e.g., test
// processes that exercise the cleaner directly).
func init() {
	recordcleanerSetSegmentPinPredicate(IsRecordingSegmentPinnedByClip)
}
