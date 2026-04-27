package api //nolint:revive

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

// recordingGapThreshold is the maximum gap between consecutive on-disk
// segments before they are split into two distinct Recordings during
// synthesis. Per ADR 0009 §"Recording (new canonical entity)" a Recording
// is a continuous span; "continuous" must be operationalized somewhere,
// and this is the recorder's choice for the pre-MS phase. 60 seconds
// matches the existing on-disk segment cadence comfortably (segments are
// minute-class) and is documented in ADR 0009 D8 closure notes.
const recordingGapThreshold = 60 * time.Second

// recordingNamespace is the UUIDv5 namespace for Recording IDs. Stable
// across recorder restarts as long as (camera_id, started_at) doesn't
// change. Distinct from cameraIDFromPathName's namespace so a Camera
// UUID and Recording UUID can never collide.
//
// The bytes are uuid.NameSpaceOID xor'd with the literal "recording"
// (zero-padded) so it sits in a different UUIDv5 subspace from any other
// canonical entity that uses NameSpaceOID directly.
var recordingNamespace = derivedNamespace("recording")

// recordingSegmentNamespace is the UUIDv5 namespace for RecordingSegment
// IDs. Same construction as recordingNamespace.
var recordingSegmentNamespace = derivedNamespace("recseg")

// derivedNamespace builds a stable UUIDv5 namespace by xor'ing the
// well-known NameSpaceOID with a short ASCII tag, zero-padded to 16
// bytes. Pure derivation — no randomness — so the namespace is the same
// across processes, machines, and restarts.
func derivedNamespace(tag string) uuid.UUID {
	var ns uuid.UUID = uuid.NameSpaceOID
	for i := 0; i < len(tag) && i < 16; i++ {
		ns[i] ^= tag[i]
	}
	return ns
}

// recordingIDFor produces the canonical UUIDv5 for a Recording from
// (camera_id, started_at). started_at is encoded as unix-micros to
// avoid sub-microsecond noise causing UUID drift across restarts.
func recordingIDFor(cameraID string, startedAt time.Time) string {
	micros := startedAt.UTC().UnixMicro()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(micros)) //nolint:gosec
	return uuid.NewSHA1(recordingNamespace,
		append([]byte(cameraID+"|"), buf[:]...)).String()
}

// recordingSegmentIDFor produces the canonical UUIDv5 for a segment from
// (camera_id, segment_path). Stable across restarts as long as the
// segment doesn't move on disk; per ADR 0009 D5 / D8 closure the migration
// explicitly does NOT rewrite segment paths.
func recordingSegmentIDFor(cameraID, segmentPath string) string {
	return uuid.NewSHA1(recordingSegmentNamespace,
		[]byte(cameraID+"|"+segmentPath)).String()
}

// recordingRegistry holds the synthesized Recording rows for the
// recorder. Per the user-confirmed Phase 2 decision in ADR 0009 closure
// notes, storage is in-memory; the registry re-synthesizes on recorder
// restart by re-walking recordstore. This is consistent with Phase 2A's
// RecordingPolicy decision.
//
// Synthesis is lazy — the first /v1/recordings* request triggers a walk;
// later requests reuse the cached map until ReloadConf clears it.
type recordingRegistry struct {
	mu         sync.Mutex
	synthAt    time.Time
	recordings map[string]*defs.Recording        // recording_id -> Recording (with Segments populated)
	segments   map[string]*defs.RecordingSegment // segment_id -> Segment
	// pathNameByCameraID resolves a synthesized Recording.camera_id back
	// to the on-disk path-name segments live under. This is distinct from
	// pathNameFromCameraID(c.Paths, ...) when wildcard path entries
	// (e.g., `all_others`) match multiple on-disk directories — in that
	// case c.Paths has only the wildcard, but on-disk pathNames are
	// concrete (cam1, cam2, ...). The playback URL needs the concrete
	// on-disk name.
	pathNameByCameraID map[string]string
}

// reset clears the cached synthesis. Called from API.ReloadConf so a
// config change (which may add/remove cameras) doesn't return stale data.
func (r *recordingRegistry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synthAt = time.Time{}
	r.recordings = nil
	r.segments = nil
	r.pathNameByCameraID = nil
}

// recordingsAPI is the helper bundle around an *API. Holding it on *API
// directly would touch api.go (which Phase 2C must not modify), so we
// store the registry on a package-level map keyed by *API. There's
// always exactly one *API per recorder; the map exists only to bind
// state to the receiver without amending the struct.
var (
	recordingRegistries   = make(map[*API]*recordingRegistry)
	recordingRegistriesMu sync.Mutex
)

func (a *API) recordingRegistry() *recordingRegistry {
	recordingRegistriesMu.Lock()
	defer recordingRegistriesMu.Unlock()
	r, ok := recordingRegistries[a]
	if !ok {
		r = &recordingRegistry{}
		recordingRegistries[a] = r
	}
	return r
}

// synthesize walks recordstore for the current path table and produces
// the canonical Recording / RecordingSegment maps. Idempotent within a
// single registry instance (returns cached result on second call until
// reset).
func (a *API) synthesize() (
	recordings map[string]*defs.Recording,
	segments map[string]*defs.RecordingSegment,
) {
	reg := a.recordingRegistry()
	reg.mu.Lock()
	defer reg.mu.Unlock()

	if reg.recordings != nil {
		return reg.recordings, reg.segments
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	recs := make(map[string]*defs.Recording)
	segs := make(map[string]*defs.RecordingSegment)
	pathByCam := make(map[string]string)

	if c == nil {
		reg.recordings = recs
		reg.segments = segs
		reg.pathNameByCameraID = pathByCam
		reg.synthAt = time.Now().UTC()
		return recs, segs
	}

	tenantID := ""
	if c.TenantID != "" {
		tenantID = c.TenantID
	}

	pathNames := recordstore.FindAllPathsWithSegments(c.Paths)
	for _, pathName := range pathNames {
		pathConf, _, err := conf.FindPathConf(c.Paths, pathName)
		if err != nil {
			continue
		}
		cameraID := cameraIDFromPathName(pathName)
		pathByCam[cameraID] = pathName

		rsSegs, err := recordstore.FindSegments(pathConf, pathName, nil, nil)
		if err != nil || len(rsSegs) == 0 {
			continue
		}

		// recordstore.FindSegments returns segments time-sorted ascending
		// (see segment.go::FindSegments).

		canonical := make([]defs.RecordingSegment, 0, len(rsSegs))
		for i, rs := range rsSegs {
			input := defs.RecordstoreSegmentInput{
				Path:      rs.Fpath,
				StartedAt: rs.Start,
			}
			// Best-effort fill from filesystem: size + mtime → byte_size,
			// created_at. Per ADR 0009 D8 closure the migration is
			// best-effort; missing fields stay zero.
			if info, statErr := os.Stat(rs.Fpath); statErr == nil {
				input.SizeBytes = info.Size()
			}
			// EndedAt is left zero pre-Phase-2-followup; recordstore's
			// public surface doesn't expose per-segment end timestamps,
			// and using the next segment's StartedAt as a stand-in breaks
			// gap detection in groupSegmentsByGap (the gap collapses to
			// zero, preventing a Recording boundary at large time jumps).
			_ = i
			seg := defs.SegmentFromRecordstoreFile(
				input,
				recordingSegmentIDFor(cameraID, rs.Fpath),
				tenantID,
				"", // site_id — unknown at recorder level pre-MS
				cameraID,
				"", // recording_server_id — unknown pre-MS
				"", // volume_id — D8: storage volumes Phase 2D
				"", // policy_id — D8: policies Phase 2A
				"", // recording_id — backfilled below once we group
			)
			canonical = append(canonical, seg)
		}

		// Group contiguous segments by gap heuristic.
		groups := groupSegmentsByGap(canonical, recordingGapThreshold)
		// Identify which group (if any) owns the in-flight segment for
		// this camera. recordstore's CurrentSegment registry is populated
		// by the format writers on segment open/close; if the most-recent
		// segment we just walked off disk is registered as in-flight,
		// the Recording it belongs to is active rather than sealed. Only
		// the chronologically last group can hold the in-flight segment
		// (segments are written in time order and a new segment is only
		// created after the previous one was Close()d).
		activeGroupIdx := -1
		if len(groups) > 0 {
			lastGroup := groups[len(groups)-1]
			if len(lastGroup) > 0 {
				lastSeg := lastGroup[len(lastGroup)-1]
				if recordstore.IsCurrentSegment(lastSeg.Path) {
					activeGroupIdx = len(groups) - 1
				}
			}
		}
		for gi, group := range groups {
			if len(group) == 0 {
				continue
			}
			recID := recordingIDFor(cameraID, group[0].StartedAt)
			// Backfill recording_id on each segment.
			for i := range group {
				defs.BackfillRecordingIDOnSegment(&group[i], recID)
			}
			rec := defs.RecordingFromSegments(group, recID)
			// Promote to active when this group owns the in-flight
			// segment. Per ADR 0009 §"Recording (new canonical entity)",
			// active Recordings have ended_at == nil. RecordingFromSegments
			// defaulted state=sealed and ended_at=last.EndedAt; reverse
			// both for the active case.
			if gi == activeGroupIdx {
				rec.State = defs.RecordingStateActive
				rec.EndedAt = nil
			}
			// Attach segments for the GET-by-id response per ADR 0009
			// §D5; list responses must omit them (empty json:"omitempty").
			rec.Segments = group
			recs[recID] = &rec
			for i := range group {
				// Re-key into the by-id map; the version we store has
				// recording_id correctly stamped.
				stored := group[i]
				segs[stored.ID] = &stored
			}
		}
	}

	reg.recordings = recs
	reg.segments = segs
	reg.pathNameByCameraID = pathByCam
	reg.synthAt = time.Now().UTC()
	return recs, segs
}

// groupSegmentsByGap splits a time-sorted slice of canonical segments
// into contiguous runs. A run ends when the next segment's StartedAt is
// more than `gap` after the previous segment's EndedAt. Empty input
// returns an empty slice; never returns nil.
func groupSegmentsByGap(
	segs []defs.RecordingSegment,
	gap time.Duration,
) [][]defs.RecordingSegment {
	if len(segs) == 0 {
		return [][]defs.RecordingSegment{}
	}
	var groups [][]defs.RecordingSegment
	cur := []defs.RecordingSegment{segs[0]}
	for i := 1; i < len(segs); i++ {
		prev := cur[len(cur)-1]
		boundary := prev.EndedAt
		if boundary.IsZero() {
			boundary = prev.StartedAt
		}
		if segs[i].StartedAt.Sub(boundary) > gap {
			groups = append(groups, cur)
			cur = []defs.RecordingSegment{segs[i]}
			continue
		}
		cur = append(cur, segs[i])
	}
	groups = append(groups, cur)
	return groups
}

// scrubSegmentForResponse zero-out fields that must not leave the
// recorder. Per AGENTS.md §6 / ADR 0009 D8 / data-classification, the
// segment's on-disk Path is recorder-local and is excluded from API
// responses even though recordstore exposes it internally.
func scrubSegmentForResponse(seg defs.RecordingSegment) defs.RecordingSegment {
	seg.Path = ""
	return seg
}

func parseTimeQuery(ctx *gin.Context, key string) (*time.Time, error) {
	v := ctx.Query(key)
	if v == "" {
		return nil, nil //nolint:nilnil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		// Try plain RFC3339 as a fallback so callers without nanoseconds
		// still work.
		t, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("invalid '%s' parameter: %w", key, err)
		}
	}
	return &t, nil
}

func (a *API) onV1RecordingsList(ctx *gin.Context) {
	recs, _ := a.synthesize()

	cameraFilter := ctx.Query("camera_id")
	stateFilter := ctx.Query("state")

	startedAfter, err := parseTimeQuery(ctx, "started_after")
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	startedBefore, err := parseTimeQuery(ctx, "started_before")
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	out := make([]defs.Recording, 0, len(recs))
	for _, r := range recs {
		if cameraFilter != "" && r.CameraID != cameraFilter {
			continue
		}
		if stateFilter != "" && string(r.State) != stateFilter {
			continue
		}
		if startedAfter != nil && !r.StartedAt.After(*startedAfter) {
			continue
		}
		if startedBefore != nil && !r.StartedAt.Before(*startedBefore) {
			continue
		}
		// List response per ADR 0009 §D5: do NOT embed segments. Copy
		// without the slice.
		row := *r
		row.Segments = nil
		out = append(out, row)
	}

	// Stable, deterministic ordering for pagination: started_at desc
	// (most-recent first), tiebreak by id.
	sortRecordings(out)

	itemCount := len(out)
	pageCount, err := paginate(&out, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	ctx.JSON(http.StatusOK, &v1RecordingList{
		ItemCount: itemCount,
		PageCount: pageCount,
		Items:     out,
	})
}

func (a *API) onV1RecordingsGet(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	recs, _ := a.synthesize()
	r, ok := recs[id]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording not found"))
		return
	}
	// Embed segment list per ADR 0009 §D5, scrubbing recorder-local Path.
	row := *r
	row.Segments = make([]defs.RecordingSegment, len(r.Segments))
	for i, s := range r.Segments {
		row.Segments[i] = scrubSegmentForResponse(s)
	}
	ctx.JSON(http.StatusOK, row)
}

// v1PlaybackResponse is the canonical playback-handle shape: { url,
// expires_at }. ADR 0009 §D5 originally specified a `token` field; ADR
// 0011 subsequently established that the recorder does not issue tokens
// (it is purely a validator). Clients reuse the user JWT they already
// hold from Cloud/MS when fetching the playback URL — the recorder's
// playback `/get` endpoint authenticates via the existing auth.Manager.
// The token field has been dropped to keep the wire contract honest;
// `expires_at` is retained as the validity bound on the URL.
type v1PlaybackResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (a *API) onV1RecordingsPlayback(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	recs, _ := a.synthesize()
	r, ok := recs[id]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording not found"))
		return
	}

	// Resolve the recorder-internal path-name from the canonical camera_id
	// so we can build a playback URL the existing playback server
	// understands. The playback server's /get endpoint takes
	// `?path=...&start=...&duration=...` per playback/on_get.go; we use
	// that surface unchanged. Token validation does not exist on the
	// playback server today — see Phase 2 follow-up below.
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	pathName := ""
	// Prefer the on-disk path-name captured during synthesis (matters when
	// wildcard config entries like `all_others` match multiple concrete
	// directories). Fall back to the config-table reverse lookup.
	reg := a.recordingRegistry()
	reg.mu.Lock()
	if reg.pathNameByCameraID != nil {
		pathName = reg.pathNameByCameraID[r.CameraID]
	}
	reg.mu.Unlock()
	if pathName == "" && c != nil {
		if name, ok := pathNameFromCameraID(c.Paths, r.CameraID); ok {
			pathName = name
		}
	}

	// Build the playback URL. The recorder doesn't know its own external
	// hostname (TLS / reverse-proxy concerns are above this layer), so we
	// emit a relative path; clients resolve against the recorder host
	// they're already talking to. The host-aware variant is a Phase 2
	// follow-up — see ADR 0009 OQ2 / playback URL composition.
	scheme := "http"
	if c != nil && c.PlaybackEncryption {
		scheme = "https"
	}
	host := ""
	if c != nil {
		host = c.PlaybackAddress
	}
	url := fmt.Sprintf("%s://%s/get?path=%s&start=%s",
		scheme,
		host,
		pathName,
		r.StartedAt.UTC().Format(time.RFC3339Nano),
	)
	if r.EndedAt != nil && !r.EndedAt.IsZero() {
		dur := r.EndedAt.Sub(r.StartedAt)
		url += fmt.Sprintf("&duration=%s", dur.String())
	}

	// Per ADR 0011: the recorder does not issue tokens. Clients send
	// their existing user JWT directly to the playback `/get` endpoint,
	// which validates via auth.Manager. expires_at bounds how long the
	// caller should treat this URL as valid before re-requesting.
	expiresAt := time.Now().UTC().Add(5 * time.Minute)

	ctx.JSON(http.StatusOK, &v1PlaybackResponse{
		URL:       url,
		ExpiresAt: expiresAt,
	})
}

// onV1RecordingsDelete handles DELETE /v1/recordings/{id}: a cascading
// delete of every segment that composes the Recording. Equivalent to
// the client iterating /v1/recording-segments/{id} DELETE for each
// segment, but in one call so clients don't race against the recorder
// rotating new segments mid-iteration on an active Recording.
//
// Behavior matches per-segment DELETE: each segment file is removed
// from disk, then the synthesis cache is invalidated. On the next
// listing the Recording disappears (it had no live segments left and
// synthesis re-walks recordstore from scratch). ADR 0009 §D5 specifies
// a persistent `state=deleted` tombstone for the Recording row; the
// recorder doesn't yet have persistent Recording storage (synthesis
// is in-memory, lazy, and re-derived on every restart), so the
// tombstone semantic is documented as a divergence from the spec
// rather than a bug in this handler. Promoting Recording state to
// persistent storage is its own follow-up.
//
// Active Recordings are deletable: the in-flight segment is removed
// along with the rest. The recorder's record loop will discover the
// missing file on its next write, log the error, and the
// segment.write_failed Event producer surfaces the condition. The
// alternative — refusing to DELETE while active — would force
// operators to stop the camera before reclaiming space, which is the
// opposite of the property they want from a "delete this footage now"
// verb.
func (a *API) onV1RecordingsDelete(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	recs, _ := a.synthesize()
	r, ok := recs[id]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording not found"))
		return
	}

	// Walk the parent Recording's segment list and remove each from
	// disk. Best-effort per segment: a single failure (e.g., a file
	// already pruned by retention since synthesis cached it) shouldn't
	// abort the cascade — the goal is "after this call, this
	// Recording's footage is gone." Per-segment errors aggregate into
	// the response so clients can see what didn't land if any part
	// failed.
	var removalErrors []string
	for _, s := range r.Segments {
		if s.Path == "" {
			removalErrors = append(removalErrors,
				fmt.Sprintf("segment %s: path missing from registry", s.ID))
			continue
		}
		if err := os.Remove(s.Path); err != nil {
			// os.IsNotExist: file already gone. Treat as success-by-
			// concurrence (retention or another DELETE got there first).
			if !os.IsNotExist(err) {
				removalErrors = append(removalErrors,
					fmt.Sprintf("segment %s: %v", s.ID, err))
			}
		}
	}

	a.recordingRegistry().reset()

	// Audit the cascade. recording.deleted isn't a wired Event kind
	// today (the producer set focuses on system signals, not admin-
	// action lifecycle); the audit chain is the durable record.
	a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    principalFromContext(ctx).PrincipalKind,
		ActorID:      principalFromContext(ctx).Sub,
		Action:       "recording.delete",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "recording",
		ResourceID:   id,
		Attributes: map[string]string{
			"camera_id":     r.CameraID,
			"segment_count": fmt.Sprintf("%d", len(r.Segments)),
		},
	})

	if len(removalErrors) > 0 {
		// Partial success: the response includes the per-segment errors
		// so the client knows what didn't drop. The cache has already
		// been invalidated, so a follow-up GET will reflect whatever
		// segments survived (if any).
		a.writeError(ctx, http.StatusInternalServerError,
			fmt.Errorf("recording partially deleted: %s",
				strings.Join(removalErrors, "; ")))
		return
	}

	a.writeOK(ctx)
}

// v1RecordingList is the list-response envelope. Unlike the legacy
// APIRecordingList (camelCase) per Phase 2 conventions and ADR 0009 it
// uses snake_case json tags.
type v1RecordingList struct {
	ItemCount int              `json:"item_count"`
	PageCount int              `json:"page_count"`
	Items     []defs.Recording `json:"items"`
}

func sortRecordings(in []defs.Recording) {
	// Sort started_at desc, tiebreak by id ascending. Keeps pagination
	// deterministic across calls.
	for i := 1; i < len(in); i++ {
		for j := i; j > 0; j-- {
			a, b := in[j-1], in[j]
			if a.StartedAt.Before(b.StartedAt) {
				in[j-1], in[j] = b, a
				continue
			}
			if a.StartedAt.Equal(b.StartedAt) && strings.Compare(a.ID, b.ID) > 0 {
				in[j-1], in[j] = b, a
				continue
			}
			break
		}
	}
}
