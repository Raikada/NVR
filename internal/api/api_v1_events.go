// Package api: /v1/events handlers per ADR 0009 §D5 Events.
//
// The recorder emits canonical Events at known trigger points and
// surfaces them through this handler. Producers wired today:
// camera.online / camera.offline (internal/core/path.go via the
// pipeline-event helpers in event_publish.go), auth.failed_login (the
// api.go authentication middleware), config.applied / policy.applied
// (the /v1/cameras, /v1/recording-policies, and /v1/recorder/config
// write handlers), and segment.write_failed (recordstore wires it via
// SetSegmentEventTarget). Producers NOT yet wired: auth.session_started
// (ADR 0011 unblocks the kind via jti-keyed session lifecycle, but the
// recorder doesn't yet track jti-dedup; small follow-up) and
// storage.volume_full (sits deeper in the cleaner subsystem).
package api //nolint:revive

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// eventListResponse is the on-wire shape of GET /v1/events. Mirrors
// the streamListResponse / cameraListResponse envelope (item_count,
// page_count, items) so clients see a consistent shape across the
// canonical /v1/* surface.
type eventListResponse struct {
	ItemCount int          `json:"item_count"`
	PageCount int          `json:"page_count"`
	Items     []defs.Event `json:"items"`

	// Notice surfaces operational notes about the response. Used in
	// Phase 2D to flag the empty-store case ("events store not yet
	// implemented; producers wiring is Phase-2-followup") so clients
	// can distinguish "no events match your filter" from "no producer
	// wiring yet."
	Notice string `json:"notice,omitempty"`
}

// eventFilters captures the query-string filters accepted by GET
// /v1/events.
type eventFilters struct {
	cameraID      string
	startedAfter  *time.Time
	startedBefore *time.Time
	kinds         []string
	severityMin   defs.EventSeverity
	subjectKind   defs.EventSubjectKind
	subjectID     string
}

// severityRank gives a total order on EventSeverity for severity_min
// ("?severity=>=warning" semantics). Lower is less severe; events with
// rank >= the filter rank pass through.
//
// Unknown severities rank at 0 so they're filtered out by any
// non-debug minimum — this matches the conservative "drop unknown"
// posture of the rest of the canonical surface.
func severityRank(s defs.EventSeverity) int {
	switch s {
	case defs.EventSeverityDebug:
		return 1
	case defs.EventSeverityInfo:
		return 2
	case defs.EventSeverityWarning:
		return 3
	case defs.EventSeverityError:
		return 4
	case defs.EventSeverityCritical:
		return 5
	default:
		return 0
	}
}

// parseEventSeverity normalizes a severity filter value. Accepts the
// raw severity name ("warning") or the ADR-documented ">=warning" form.
// Returns the parsed EventSeverity and true on success; empty + true
// when the input is empty (no filter); zero + false on parse error.
func parseEventSeverity(v string) (defs.EventSeverity, bool) {
	if v == "" {
		return "", true
	}
	v = strings.TrimPrefix(v, ">=")
	switch defs.EventSeverity(v) {
	case defs.EventSeverityDebug,
		defs.EventSeverityInfo,
		defs.EventSeverityWarning,
		defs.EventSeverityError,
		defs.EventSeverityCritical:
		return defs.EventSeverity(v), true
	}
	return "", false
}

// parseEventSubjectKind validates a subject_kind query value.
func parseEventSubjectKind(v string) (defs.EventSubjectKind, bool) {
	if v == "" {
		return "", true
	}
	switch defs.EventSubjectKind(v) {
	case defs.EventSubjectKindCamera,
		defs.EventSubjectKindStream,
		defs.EventSubjectKindSegment,
		defs.EventSubjectKindVolume,
		defs.EventSubjectKindServer,
		defs.EventSubjectKindUser,
		defs.EventSubjectKindSession:
		return defs.EventSubjectKind(v), true
	}
	return "", false
}

// parseEventFilters extracts every filter from the query string. The
// per-ADR filter set: camera_id, started_after, started_before, kind
// (multi-value), severity / severity_min, subject_kind, subject_id.
func parseEventFilters(ctx *gin.Context) (eventFilters, error) {
	var f eventFilters

	if v := ctx.Query("camera_id"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			return f, fmt.Errorf("invalid camera_id: %w", err)
		}
		f.cameraID = v
	}

	if v := ctx.Query("started_after"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid started_after: %w", err)
		}
		f.startedAfter = &t
	}

	if v := ctx.Query("started_before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid started_before: %w", err)
		}
		f.startedBefore = &t
	}

	// kind is multi-value: ?kind=a&kind=b. Gin exposes QueryArray.
	if kinds := ctx.QueryArray("kind"); len(kinds) > 0 {
		f.kinds = kinds
	}

	// Accept either ?severity=warning, ?severity=>=warning, or the
	// explicit ?severity_min= form.
	rawSeverity := ctx.Query("severity")
	if rawSeverity == "" {
		rawSeverity = ctx.Query("severity_min")
	}
	if rawSeverity != "" {
		s, ok := parseEventSeverity(rawSeverity)
		if !ok {
			return f, fmt.Errorf("invalid severity: %s", rawSeverity)
		}
		f.severityMin = s
	}

	if v := ctx.Query("subject_kind"); v != "" {
		sk, ok := parseEventSubjectKind(v)
		if !ok {
			return f, fmt.Errorf("invalid subject_kind: %s", v)
		}
		f.subjectKind = sk
	}

	if v := ctx.Query("subject_id"); v != "" {
		// Subject ids are opaque strings on the canonical Event, but in
		// practice they're UUID strings; validate the form when present.
		if _, err := uuid.Parse(v); err != nil {
			return f, fmt.Errorf("invalid subject_id: %w", err)
		}
		f.subjectID = v
	}

	return f, nil
}

// match returns true when the supplied event passes every set filter.
// Unset filters are wildcards.
func (f eventFilters) match(e *defs.Event) bool {
	if f.cameraID != "" {
		// camera_id filter applies when subject_kind is camera and the
		// subject id matches, OR when an Attributes["camera_id"] is set.
		if !(e.SubjectKind == defs.EventSubjectKindCamera && e.SubjectID == f.cameraID) &&
			e.Attributes["camera_id"] != f.cameraID {
			return false
		}
	}
	if f.startedAfter != nil && e.OccurredAt.Before(*f.startedAfter) {
		return false
	}
	if f.startedBefore != nil && e.OccurredAt.After(*f.startedBefore) {
		return false
	}
	if len(f.kinds) > 0 {
		ok := false
		for _, k := range f.kinds {
			if e.Kind == k {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.severityMin != "" {
		if severityRank(e.Severity) < severityRank(f.severityMin) {
			return false
		}
	}
	if f.subjectKind != "" && e.SubjectKind != f.subjectKind {
		return false
	}
	if f.subjectID != "" && e.SubjectID != f.subjectID {
		return false
	}
	return true
}

// eventStore returns the process-wide EventStore. The orchestrator
// (api.go) does not yet hold a reference; until it does, the handlers
// share a package-level singleton so a future producer-side wiring
// has a single point to publish into. The singleton starts empty;
// the canonical "events store not yet wired" notice surfaces in
// onV1EventsList when the buffer is empty.
//
// Receiver-method form (not a free function) leaves room for the
// orchestrator to inject a per-API store later without changing
// callsites.
func (a *API) eventStore() *EventStore {
	_ = a // reserved for a future a.EventStore field on the API struct
	return defaultEventStore()
}

// onV1EventsList serves GET /v1/events. Filters, paginates, and
// returns the buffer's current contents.
//
// When the buffer is empty the response carries a `notice` field
// noting that some producers (segment.write_failed,
// storage.volume_full) are still unwired, so a quiet buffer can mean
// "no events match your filter," "no events have occurred since
// startup," or "the unwired producers haven't fired anyway." Clients
// that want to detect the unwired-producers gap explicitly can probe
// for any of the wired kinds and check whether they ever appear.
func (a *API) onV1EventsList(ctx *gin.Context) {
	filters, err := parseEventFilters(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	store := a.eventStore()
	all := store.Snapshot()

	// Filter post-snapshot. The buffer is small (capacity 1024 by
	// default), so a linear filter is fine.
	filtered := make([]defs.Event, 0, len(all))
	for i := range all {
		if filters.match(&all[i]) {
			filtered = append(filtered, all[i])
		}
	}

	// Sort newest-first for typical "what just happened" UX. Pagination
	// then takes the page-th window.
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].OccurredAt.Equal(filtered[j].OccurredAt) {
			return filtered[i].OccurredAt.After(filtered[j].OccurredAt)
		}
		return filtered[i].ID < filtered[j].ID
	})

	resp := eventListResponse{
		Items: filtered,
	}
	resp.ItemCount = len(resp.Items)

	pageCount, err := paginate(&resp.Items, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	resp.PageCount = pageCount

	if len(all) == 0 {
		resp.Notice = "no events buffered: camera-state, auth, and config-apply " +
			"producers are wired but may not have fired since startup; " +
			"segment-write and storage producers remain unwired"
	}

	ctx.JSON(http.StatusOK, resp)
}

// onV1EventsGet serves GET /v1/events/:id.
func (a *API) onV1EventsGet(ctx *gin.Context) {
	idStr := ctx.Param("id")
	if _, err := uuid.Parse(idStr); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid event id: %w", err))
		return
	}
	e, ok := a.eventStore().GetByID(idStr)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("event not found"))
		return
	}
	ctx.JSON(http.StatusOK, &e)
}
