// Package api: SP4 — DB-backed /v1/events list/get.
//
// The foundation's canonical events live in SQLite via events.Service
// (health transitions, vendor channels); the legacy in-memory buffer
// only ever saw config/auth operational noise, which belongs to the
// audit log. When the events service is wired (always, in production)
// the list/get endpoints serve the DB; the legacy path survives for
// pre-foundation tests and degraded boots.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/events"
)

// eventDBWire is the DB-backed event wire shape. Snapshot/thumbnail
// URLs are signed and short-lived; clients re-list to refresh them.
type eventDBWire struct {
	ID             string          `json:"id"`
	CameraID       string          `json:"camera_id"`
	TypeID         string          `json:"type_id"`
	Source         string          `json:"source"`
	OccurredAt     time.Time       `json:"occurred_at"`
	ReceivedAt     time.Time       `json:"received_at"`
	Severity       string          `json:"severity"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	AcknowledgedAt *time.Time      `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string          `json:"acknowledged_by,omitempty"`
	ExpiresAt      time.Time       `json:"expires_at"`
	SnapshotURL    string          `json:"snapshot_url,omitempty"`
	ThumbnailURL   string          `json:"thumbnail_url,omitempty"`
	ClipID         string          `json:"clip_id,omitempty"`
}

// eventDBWireFromRow converts + enriches one event.
func (a *API) eventDBWireFromRow(ctx *gin.Context, ev *events.Event) eventDBWire {
	w := eventDBWire{
		ID:             ev.ID,
		CameraID:       ev.CameraID,
		TypeID:         ev.TypeID,
		Source:         ev.Source,
		OccurredAt:     ev.OccurredAt,
		ReceivedAt:     ev.ReceivedAt,
		Severity:       ev.Severity,
		AcknowledgedBy: ev.AcknowledgedBy,
		ExpiresAt:      ev.ExpiresAt,
	}
	if len(ev.PayloadJSON) > 0 && json.Valid(ev.PayloadJSON) {
		w.Payload = ev.PayloadJSON
	}
	if !ev.AcknowledgedAt.IsZero() {
		t := ev.AcknowledgedAt
		w.AcknowledgedAt = &t
	}
	if a.Store != nil && a.Signer != nil {
		if rows, err := a.Store.EventSnapshots.ListByEvent(ctx.Request.Context(), ev.ID); err == nil {
			for _, r := range rows {
				u := a.signedMediaURL("/v1/media/snapshots/" + ev.ID + "/" + r.Kind)
				switch r.Kind {
				case "full":
					w.SnapshotURL = u
				case "thumb":
					w.ThumbnailURL = u
				}
			}
		}
	}
	if clip, ok := a.clipStore().FindByEventID(ev.ID); ok {
		w.ClipID = clip.ID
	}
	return w
}

// onV1EventsListDB serves GET /v1/events off the canonical store.
func (a *API) onV1EventsListDB(ctx *gin.Context) {
	filter := events.ListFilter{}
	if v := ctx.Query("camera_id"); v != "" {
		filter.CameraIDs = strings.Split(v, ",")
	}
	if v := ctx.Query("type_id"); v != "" {
		filter.TypeIDs = strings.Split(v, ",")
	}
	if v := ctx.Query("source"); v != "" {
		filter.Sources = strings.Split(v, ",")
	}
	if v := ctx.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			a.writeError(ctx, http.StatusBadRequest, errors.New("from must be RFC3339"))
			return
		}
		filter.From = t
	}
	if v := ctx.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			a.writeError(ctx, http.StatusBadRequest, errors.New("to must be RFC3339"))
			return
		}
		filter.To = t
	}
	if v := ctx.Query("min_severity"); v != "" {
		filter.MinSev = v
	}
	filter.OnlyUnack = ctx.Query("unacknowledged") == "true"
	filter.Cursor = ctx.Query("cursor")
	if v := ctx.Query("items_per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			a.writeError(ctx, http.StatusBadRequest, errors.New("items_per_page must be a positive integer"))
			return
		}
		filter.Limit = n
	}

	rows, next, err := a.EventsService.List(ctx.Request.Context(), filter)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	items := make([]eventDBWire, 0, len(rows))
	for _, ev := range rows {
		items = append(items, a.eventDBWireFromRow(ctx, ev))
	}
	ctx.JSON(http.StatusOK, gin.H{
		"items":       items,
		"item_count":  len(items),
		"next_cursor": next,
	})
}

// onV1EventsGetDB serves GET /v1/events/:id off the canonical store.
func (a *API) onV1EventsGetDB(ctx *gin.Context) {
	ev, err := a.EventsService.Get(ctx.Request.Context(), ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("event not found"))
		return
	}
	w := a.eventDBWireFromRow(ctx, ev)
	ctx.JSON(http.StatusOK, &w)
}

// ----- Phase A B3: acknowledge-all + bookmarks ----------------------

type ackAllRequest struct {
	CameraID string     `json:"camera_id"`
	Before   *time.Time `json:"before"`
}

// onV1EventsAcknowledgeAll handles POST /v1/events/acknowledge-all.
func (a *API) onV1EventsAcknowledgeAll(ctx *gin.Context) {
	if a.EventsService == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("events service not wired"))
		return
	}
	var req ackAllRequest
	_ = ctx.ShouldBindJSON(&req) // empty body = ack everything
	var before time.Time
	if req.Before != nil {
		before = *req.Before
	}
	n, err := a.EventsService.AcknowledgeAll(ctx.Request.Context(), req.CameraID, before, a.localUserSub(ctx))
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"acknowledged": n})
}

type bookmarkRequest struct {
	Time *time.Time `json:"time"`
	Note string     `json:"note"`
}

// onV1CameraBookmarkPost handles POST /v1/cameras/:id/bookmarks —
// bookmarks are plain events of the seeded `bookmark` type, so the
// timeline, retention, and export machinery inherit them for free.
func (a *API) onV1CameraBookmarkPost(ctx *gin.Context) {
	if a.EventsService == nil || a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("events service not wired"))
		return
	}
	camID := ctx.Param("id")
	if _, err := a.Store.Cameras.GetByID(ctx.Request.Context(), camID); err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera not found"))
		return
	}
	var req bookmarkRequest
	_ = ctx.ShouldBindJSON(&req)
	at := time.Now().UTC()
	if req.Time != nil {
		at = req.Time.UTC()
	}
	payload, _ := json.Marshal(map[string]string{"note": req.Note})
	ev := &events.Event{
		CameraID:    camID,
		TypeID:      "bookmark",
		Source:      "user",
		OccurredAt:  at,
		Severity:    "info",
		PayloadJSON: payload,
	}
	if err := a.EventsService.Insert(ctx.Request.Context(), ev); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "event.bookmark_created", "event", ev.ID, map[string]string{
		"camera_id": camID,
	})
	w := a.eventDBWireFromRow(ctx, ev)
	ctx.JSON(http.StatusCreated, &w)
}
