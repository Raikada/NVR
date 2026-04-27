package api //nolint:revive

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// v1RecordingSegmentList is the list-response envelope for the canonical
// /v1/recording-segments endpoint. snake_case per ADR 0009 conventions.
type v1RecordingSegmentList struct {
	ItemCount int                     `json:"item_count"`
	PageCount int                     `json:"page_count"`
	Items     []defs.RecordingSegment `json:"items"`
}

func (a *API) onV1RecordingSegmentsList(ctx *gin.Context) {
	_, segs := a.synthesize()

	recordingFilter := ctx.Query("recording_id")
	cameraFilter := ctx.Query("camera_id")
	volumeFilter := ctx.Query("volume_id")
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
	createdAfter, err := parseTimeQuery(ctx, "created_after")
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	createdBefore, err := parseTimeQuery(ctx, "created_before")
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	out := make([]defs.RecordingSegment, 0, len(segs))
	for _, s := range segs {
		if recordingFilter != "" && s.RecordingID != recordingFilter {
			continue
		}
		if cameraFilter != "" && s.CameraID != cameraFilter {
			continue
		}
		if volumeFilter != "" && s.VolumeID != volumeFilter {
			continue
		}
		if stateFilter != "" && string(s.State) != stateFilter {
			continue
		}
		// started_* filters are footage-time; created_* filters are
		// segment-write time. ADR 0009 §D5 spells out the distinction.
		if startedAfter != nil && !s.StartedAt.After(*startedAfter) {
			continue
		}
		if startedBefore != nil && !s.StartedAt.Before(*startedBefore) {
			continue
		}
		if createdAfter != nil && !s.CreatedAt.After(*createdAfter) {
			continue
		}
		if createdBefore != nil && !s.CreatedAt.Before(*createdBefore) {
			continue
		}
		out = append(out, scrubSegmentForResponse(*s))
	}

	sortSegments(out)

	itemCount := len(out)
	pageCount, err := paginate(&out, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	ctx.JSON(http.StatusOK, &v1RecordingSegmentList{
		ItemCount: itemCount,
		PageCount: pageCount,
		Items:     out,
	})
}

func (a *API) onV1RecordingSegmentsGet(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	_, segs := a.synthesize()
	s, ok := segs[id]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording segment not found"))
		return
	}
	ctx.JSON(http.StatusOK, scrubSegmentForResponse(*s))
}

func (a *API) onV1RecordingSegmentsDelete(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid id: %w", err))
		return
	}
	_, segs := a.synthesize()
	s, ok := segs[id]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording segment not found"))
		return
	}

	// The on-disk path was captured at synthesis time but is excluded
	// from API responses (see scrubSegmentForResponse). It still lives
	// in the registry-internal copy via the by-id segments map, where
	// .Path is the unredacted recordstore-supplied value.
	segPath := s.Path
	if segPath == "" {
		a.writeError(ctx, http.StatusInternalServerError,
			fmt.Errorf("segment path missing from registry"))
		return
	}

	if err := os.Remove(segPath); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Invalidate the cache so re-listing reflects the deletion. A more
	// surgical update is possible — drop the segment, decrement the
	// parent Recording's counters, possibly drop the Recording — but
	// since the registry is in-memory and re-synthesis is cheap (it's
	// the same walk recordstore would do anyway), a full invalidation
	// is simpler and correct. Re-synthesis happens lazily on the next
	// request.
	a.recordingRegistry().reset()

	a.writeOK(ctx)
}

func sortSegments(in []defs.RecordingSegment) {
	// Sort started_at desc, tiebreak by id ascending — same convention
	// as recordings. Keeps pagination deterministic.
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
