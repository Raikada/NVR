// Package api: /v1/clips handlers per ADR 0009 §D5 follow-up.
//
// The Clip canonical entity is recorder-authoritative for export
// preparation (see ARCHITECTURE.md §5 item 6). This file implements
// the REST surface:
//
//   POST   /v1/clips                 — create + start prep (async)
//   GET    /v1/clips                 — list / filter / paginate
//   GET    /v1/clips/{id}            — fetch one
//   DELETE /v1/clips/{id}            — soft-delete (state=deleted)
//   GET    /v1/clips/{id}/download   — fetch the stitched bytes
//
// snake_case wire format throughout, mirroring /v1/recordings.
//
// Pre-MS phase. `requested_by` is the empty string in this build —
// see internal/defs/clip.go header. The POST request body still
// accepts an opaque `requested_by` value for forward compatibility
// once the Management Server lands; it's stored as-is.
package api //nolint:revive

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

// v1ClipCreateRequest is the POST /v1/clips request body. Only the
// fields required to bound the clip range and identify the source
// camera are mandatory; metadata (label, description, expires_at)
// is optional.
type v1ClipCreateRequest struct {
	CameraID       string     `json:"camera_id"`
	Label          string     `json:"label"`
	Description    *string    `json:"description,omitempty"`
	RangeStartedAt time.Time  `json:"range_started_at"`
	RangeEndedAt   time.Time  `json:"range_ended_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RequestedBy    string     `json:"requested_by,omitempty"`
}

// v1ClipResponse mirrors defs.Clip on the wire and adds a Notice
// field for surfacing operational caveats. The field is currently
// unused on the success path — preparation produces a real
// fmp4-to-mp4 remux — but is retained on the wire shape for
// forward-compat with future limitation-surfacing needs.
type v1ClipResponse struct {
	defs.Clip
	Notice string `json:"notice,omitempty"`
}

// v1ClipList is the list-response envelope. Mirrors the
// /v1/recordings convention.
type v1ClipList struct {
	ItemCount int        `json:"item_count"`
	PageCount int        `json:"page_count"`
	Items     []defs.Clip `json:"items"`
	Notice    string     `json:"notice,omitempty"`
}


// clipStore returns the API's clip store. The orchestrator does not
// yet hold a reference; callers share the package-level singleton
// so the recordcleaner pinning hook sees the same state.
func (a *API) clipStore() *ClipStore {
	_ = a // reserved for a future a.ClipStore field
	return defaultClipStore()
}

func (a *API) onV1ClipsPost(ctx *gin.Context) {
	var req v1ClipCreateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if _, err := uuid.Parse(req.CameraID); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera_id: %w", err))
		return
	}
	if req.Label == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("label is required"))
		return
	}
	if req.RangeStartedAt.IsZero() || req.RangeEndedAt.IsZero() {
		a.writeError(ctx, http.StatusBadRequest, errors.New("range_started_at and range_ended_at are required"))
		return
	}
	if !req.RangeEndedAt.After(req.RangeStartedAt) {
		a.writeError(ctx, http.StatusBadRequest, errors.New("range_ended_at must be after range_started_at"))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()
	if c == nil {
		a.writeError(ctx, http.StatusInternalServerError, errors.New("recorder configuration not loaded"))
		return
	}

	// Resolve camera_id → on-disk path-name. The path table may
	// only contain a wildcard entry (e.g. `all_others`), in which
	// case the concrete on-disk directory must be discovered by
	// re-walking recordstore. We trigger that walk via synthesize
	// (used by /v1/recordings) and consult its path-by-camera map;
	// then fall back to the static config-table reverse lookup for
	// the explicit-path case.
	pathName := ""
	a.synthesize() // populates the registry
	reg := a.recordingRegistry()
	reg.mu.Lock()
	if reg.pathNameByCameraID != nil {
		pathName = reg.pathNameByCameraID[req.CameraID]
	}
	reg.mu.Unlock()
	if pathName == "" {
		if name, ok := pathNameFromCameraID(c.Paths, req.CameraID); ok {
			pathName = name
		}
	}
	if pathName == "" {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	// Resolve the source segments up-front so we can populate
	// SourceSegmentIDs and pin the right paths immediately. If no
	// segments overlap the range, fail fast with 400.
	segs, err := findSegmentsForClip(c.Paths, pathName, req.CameraID, req.RangeStartedAt, req.RangeEndedAt)
	if err != nil && !errors.Is(err, recordstore.ErrNoSegmentsFound) {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("segment lookup: %w", err))
		return
	}
	if len(segs) == 0 {
		a.writeError(ctx, http.StatusBadRequest, errors.New("no recording segments overlap the requested range"))
		return
	}

	now := nowUTC()
	id := uuid.New().String()

	segIDs := make([]string, len(segs))
	segPaths := make([]string, len(segs))
	for i, s := range segs {
		segIDs[i] = s.id
		segPaths[i] = s.path
	}

	clip := &defs.Clip{
		ID:                id,
		TenantID:          a.tenantID(),
		SiteID:            "",
		RecordingServerID: "",
		CameraID:          req.CameraID,
		RequestedBy:       req.RequestedBy,
		Label:             req.Label,
		Description:       req.Description,
		RangeStartedAt:    req.RangeStartedAt.UTC(),
		RangeEndedAt:      req.RangeEndedAt.UTC(),
		Duration:          req.RangeEndedAt.Sub(req.RangeStartedAt),
		RequestedAt:       now,
		ExpiresAt:         req.ExpiresAt,
		State:             defs.ClipStateRequested,
		Container:         defs.ClipContainerMP4,
		SourceSegmentIDs:  segIDs,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	store := a.clipStore()
	store.Put(clip, segPaths)

	// Spawn the preparation worker. Async per the ADR; the response
	// returns immediately with state=requested.
	runPreparerAsync(&clipPreparer{
		store:     store,
		clipID:    id,
		pathName:  pathName,
		pathConfs: c.Paths,
	})

	resp := v1ClipResponse{Clip: *clip}
	ctx.JSON(http.StatusCreated, &resp)
}

func (a *API) onV1ClipsList(ctx *gin.Context) {
	store := a.clipStore()
	all := store.Snapshot()

	cameraFilter := ctx.Query("camera_id")
	stateFilter := ctx.Query("state")

	out := make([]defs.Clip, 0, len(all))
	for _, c := range all {
		if cameraFilter != "" && c.CameraID != cameraFilter {
			continue
		}
		if stateFilter != "" && string(c.State) != stateFilter {
			continue
		}
		out = append(out, c)
	}

	sortClips(out)

	itemCount := len(out)
	pageCount, err := paginate(&out, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	ctx.JSON(http.StatusOK, &v1ClipList{
		ItemCount: itemCount,
		PageCount: pageCount,
		Items:     out,
	})
}

func (a *API) onV1ClipsGet(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	c, ok := a.clipStore().Get(id)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("clip not found"))
		return
	}
	ctx.JSON(http.StatusOK, &v1ClipResponse{Clip: c})
}

func (a *API) onV1ClipsDelete(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	store := a.clipStore()
	prev, ok := store.Get(id)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("clip not found"))
		return
	}
	// Best-effort cleanup of the export artifact. If preparation was
	// in flight, the worker may write the file after this rm; the
	// next cleanup pass (or a follow-up DELETE) handles it. For now
	// we accept that small race because the production write path
	// will retry and clip preparation is an internal-state operation.
	if prev.ExportPath != "" {
		os.Remove(prev.ExportPath) //nolint:errcheck
	}
	// Soft-delete per platform/CLAUDE.md cross-cutting rule 4: prefer
	// state=deleted over hard removal so audit/clip-history surfaces
	// retain the row. The underlying source segments are released
	// because Update + state=deleted drops them from activeForPinning.
	store.Update(id, func(c *defs.Clip) {
		c.State = defs.ClipStateDeleted
		c.UpdatedAt = nowUTC()
	})

	a.writeOK(ctx)
}

func (a *API) onV1ClipsDownload(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	c, ok := a.clipStore().Get(id)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("clip not found"))
		return
	}
	if c.State != defs.ClipStateReady {
		a.writeError(ctx, http.StatusConflict,
			fmt.Errorf("clip is not ready (state=%s)", c.State))
		return
	}
	if c.ExportPath == "" {
		a.writeError(ctx, http.StatusInternalServerError,
			errors.New("clip is ready but export_path missing"))
		return
	}

	// Token validation matches the placeholder pattern from
	// /v1/recordings/{id}/playback. Real validation requires the
	// recorder-side token store that ADR 0002 OQ10 will introduce;
	// today we accept any `token=placeholder-<id>` query parameter
	// or no token, mirroring the recordings playback shape so
	// clients can pin the surface.
	tok := ctx.Query("token")
	if tok != "" && !strings.HasPrefix(tok, "placeholder-") {
		a.writeError(ctx, http.StatusUnauthorized, errors.New("invalid token"))
		return
	}

	f, err := os.Open(c.ExportPath)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("open export: %w", err))
		return
	}
	defer f.Close() //nolint:errcheck
	info, err := f.Stat()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("stat export: %w", err))
		return
	}

	ctx.Header("Content-Type", "video/mp4")
	ctx.Header("Content-Length", fmt.Sprintf("%d", info.Size()))
	ctx.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.mp4"`, c.ID))
	ctx.Status(http.StatusOK)
	if _, err := io.Copy(ctx.Writer, f); err != nil {
		// Best-effort: the response is already streaming, so a
		// failure here is logged but cannot be reflected in the
		// status code.
		a.Log(logger.Error, "clip download stream error: %v", err)
	}
}

// sortClips sorts in requested_at desc, tiebreak by id asc, matching
// the recordings convention.
func sortClips(in []defs.Clip) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0; j-- {
			a, b := in[j-1], in[j]
			if a.RequestedAt.Before(b.RequestedAt) {
				in[j-1], in[j] = b, a
				continue
			}
			if a.RequestedAt.Equal(b.RequestedAt) && strings.Compare(a.ID, b.ID) > 0 {
				in[j-1], in[j] = b, a
				continue
			}
			break
		}
	}
}
