// Package api: Phase 5 events extensions per Task 5.5.
//
// Adds:
//   - POST /v1/events/:id/acknowledge        (event.ack)
//   - GET  /v1/events/stream                  (event.stream; SSE)
//   - GET/POST/PATCH /v1/event-types[/:id]    (event_type.*)
//   - GET  /v1/event-retention                (event_retention.read)
//   - PUT  /v1/event-retention/:type_id       (event_retention.write)
//
// Note: GET /v1/events list/get already exist in api_v1_events.go and
// remain wired against the in-memory event store. The Phase 5 plan
// rewires those to events.Service when the in-memory store retires;
// for now, the legacy endpoints continue to serve the SPA's existing
// event view, and the new ack + SSE endpoints exercise events.Service
// directly.
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1EventsExtensions(r gin.IRouter) {
	r.POST("/events/:id/acknowledge", rbac.RequirePerm(rbac.PermEventAck, a.auditEmitter()), a.onV1EventAcknowledge)
	r.GET("/events/stream", rbac.RequirePerm(rbac.PermEventStream, a.auditEmitter()), a.onV1EventsStream)

	r.GET("/event-types", rbac.RequirePerm(rbac.PermEventTypeList, a.auditEmitter()), a.onV1EventTypesList)
	r.POST("/event-types", rbac.RequirePerm(rbac.PermEventTypeCreate, a.auditEmitter()), a.onV1EventTypesCreate)
	r.PATCH("/event-types/:id", rbac.RequirePerm(rbac.PermEventTypeUpdate, a.auditEmitter()), a.onV1EventTypesUpdate)

	r.GET("/event-retention", rbac.RequirePerm(rbac.PermEventRetentionRead, a.auditEmitter()), a.onV1EventRetentionList)
	r.PUT("/event-retention/:type_id", rbac.RequirePerm(rbac.PermEventRetentionWrite, a.auditEmitter()), a.onV1EventRetentionPut)
}

// onV1EventAcknowledge marks an event acknowledged by the current user.
func (a *API) onV1EventAcknowledge(ctx *gin.Context) {
	if a.EventsService == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("events service not wired"))
		return
	}
	id := ctx.Param("id")
	principal := principalFromContext(ctx)
	// Fallback to "system" when the principal carries no user identity
	// (legacy internal/HTTP auth path or anonymous tests). The audit
	// row records the real principal kind separately.
	userID := principal.Sub
	if userID == "" {
		userID = "system"
	}
	if err := a.EventsService.Acknowledge(ctx.Request.Context(), id, userID); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "event.acknowledged", "event", id, nil)
	ctx.Status(http.StatusNoContent)
}

// onV1EventsStream serves Server-Sent Events. The connection stays open
// until the client disconnects or the server is shutting down. Each
// canonical event is encoded as `data: <json>\n\n`.
//
// Optional query filters:
//   - camera_id: only emit events for this camera
//   - type:      only emit events of this type id
func (a *API) onV1EventsStream(ctx *gin.Context) {
	if a.EventsService == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("events service not wired"))
		return
	}
	cameraID := ctx.Query("camera_id")
	typeID := ctx.Query("type")

	bus, unsub := a.EventsService.Subscribe()
	defer unsub()

	ctx.Writer.Header().Set("Content-Type", "text/event-stream")
	ctx.Writer.Header().Set("Cache-Control", "no-cache")
	ctx.Writer.Header().Set("Connection", "keep-alive")
	ctx.Writer.WriteHeader(http.StatusOK)
	ctx.Writer.Flush()

	// Send a comment line right away so the browser commits to the connection.
	_, _ = ctx.Writer.Write([]byte(": ok\n\n"))
	ctx.Writer.Flush()

	for {
		select {
		case <-ctx.Request.Context().Done():
			return
		case ev, ok := <-bus:
			if !ok {
				return
			}
			if cameraID != "" && ev.CameraID != cameraID {
				continue
			}
			if typeID != "" && ev.TypeID != typeID {
				continue
			}
			if err := writeSSEEvent(ctx, &ev); err != nil {
				return
			}
		}
	}
}

func writeSSEEvent(ctx *gin.Context, ev *events.Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := ctx.Writer.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := ctx.Writer.Write(raw); err != nil {
		return err
	}
	if _, err := ctx.Writer.Write([]byte("\n\n")); err != nil {
		return err
	}
	ctx.Writer.Flush()
	return nil
}

// ----- event_types ---------------------------------------------------

type eventTypeWire struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Vendor      string `json:"vendor,omitempty"`
	Description string `json:"description,omitempty"`
}

func (a *API) onV1EventTypesList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	rows, err := a.Store.EventTypes.List(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make([]eventTypeWire, 0, len(rows))
	for _, e := range rows {
		out = append(out, eventTypeWire{
			ID: e.ID, DisplayName: e.DisplayName,
			Vendor: e.Vendor, Description: e.Description,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

type eventTypeCreateRequest struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

func (a *API) onV1EventTypesCreate(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req eventTypeCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.ID == "" || req.DisplayName == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("id and display_name are required"))
		return
	}
	row := &store.EventType{
		ID: req.ID, DisplayName: req.DisplayName,
		Vendor: "custom", Description: req.Description,
	}
	if err := a.Store.EventTypes.Insert(ctx.Request.Context(), row); err != nil {
		if errors.Is(err, store.ErrEventTypeExists) {
			a.writeError(ctx, http.StatusConflict, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "event_type.created", "event_type", row.ID, nil)
	ctx.JSON(http.StatusCreated, eventTypeWire{
		ID: row.ID, DisplayName: row.DisplayName, Vendor: row.Vendor, Description: row.Description,
	})
}

type eventTypeUpdateRequest struct {
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

func (a *API) onV1EventTypesUpdate(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	existing, err := a.Store.EventTypes.GetByID(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("event type not found"))
		return
	}
	// Seeded types (vendor != 'custom') can only have display_name and
	// description updated. Vendor is immutable across the board.
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req eventTypeUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.DisplayName != nil {
		existing.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if err := a.Store.EventTypes.Update(ctx.Request.Context(), existing); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "event_type.updated", "event_type", id, nil)
	ctx.JSON(http.StatusOK, eventTypeWire{
		ID: existing.ID, DisplayName: existing.DisplayName,
		Vendor: existing.Vendor, Description: existing.Description,
	})
}

// ----- event_retention ----------------------------------------------

type eventRetentionWire struct {
	TypeID              string `json:"type_id"`
	KeepDurationSeconds int64  `json:"keep_duration_seconds"`
}

func (a *API) onV1EventRetentionList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	rows, err := a.Store.EventRetention.List(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.TypeID] = r.KeepDurationSeconds
	}
	ctx.JSON(http.StatusOK, gin.H{"retention": out})
}

func (a *API) onV1EventRetentionPut(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	typeID := ctx.Param("type_id")
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req struct {
		KeepDurationSeconds int64 `json:"keep_duration_seconds"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.KeepDurationSeconds <= 0 {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("keep_duration_seconds must be positive"))
		return
	}
	if err := a.Store.EventRetention.Upsert(ctx.Request.Context(), &store.EventRetention{
		TypeID:              typeID,
		KeepDurationSeconds: req.KeepDurationSeconds,
	}); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "event_retention.updated", "event_retention", typeID, map[string]string{
		"keep_duration_seconds": strconv.FormatInt(req.KeepDurationSeconds, 10),
	})
	ctx.JSON(http.StatusOK, eventRetentionWire{
		TypeID: typeID, KeepDurationSeconds: req.KeepDurationSeconds,
	})
}

