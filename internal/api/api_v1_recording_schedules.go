// Package api: /v1/recording-policies/:id/schedules — Phase 5 Task 5.4.
//
// Stores RecordingSchedule rows backing the schedule.Resolver. The
// canonical surface uses HH:MM strings on the wire for human-friendly
// editing; the store keeps minute-of-day integers (0..1439) so the
// resolver can do simple arithmetic without re-parsing on every check.
//
// PUT replaces the entire schedule set atomically (delete+insert in a
// single transaction). Empty body = no schedules (policy fully off
// when mode=scheduled).
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1RecordingSchedules(r gin.IRouter) {
	r.GET("/recording-policies/:id/schedules", rbac.RequirePerm(rbac.PermPolicyScheduleRead, a.auditEmitter()), a.onV1RecordingSchedulesGet)
	r.PUT("/recording-policies/:id/schedules", rbac.RequirePerm(rbac.PermPolicyScheduleWrite, a.auditEmitter()), a.onV1RecordingSchedulesPut)
}

type recordingScheduleWire struct {
	DayOfWeek int    `json:"day_of_week"` // 0=Sun..6=Sat
	Start     string `json:"start"`       // HH:MM site-local
	End       string `json:"end"`
}

type recordingSchedulesPutRequest struct {
	Schedules []recordingScheduleWire `json:"schedules"`
}

type recordingSchedulesResponse struct {
	Schedules []recordingScheduleWire `json:"schedules"`
}

func (a *API) onV1RecordingSchedulesGet(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	policyID := ctx.Param("id")
	rows, err := a.Store.RecordingSchedules.ListByPolicy(ctx.Request.Context(), policyID)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := recordingSchedulesResponse{Schedules: make([]recordingScheduleWire, 0, len(rows))}
	for _, r := range rows {
		out.Schedules = append(out.Schedules, recordingScheduleWire{
			DayOfWeek: r.DayOfWeek,
			Start:     formatHHMM(r.StartMinute),
			End:       formatHHMM(r.EndMinute),
		})
	}
	ctx.JSON(http.StatusOK, &out)
}

func (a *API) onV1RecordingSchedulesPut(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	policyID := ctx.Param("id")
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req recordingSchedulesPutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	rows := make([]*store.RecordingSchedule, 0, len(req.Schedules))
	for i, s := range req.Schedules {
		if s.DayOfWeek < 0 || s.DayOfWeek > 6 {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("schedules[%d].day_of_week out of range (0..6)", i))
			return
		}
		startMin, ok := parseHHMM(s.Start)
		if !ok {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("schedules[%d].start invalid HH:MM: %s", i, s.Start))
			return
		}
		endMin, ok := parseHHMM(s.End)
		if !ok {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("schedules[%d].end invalid HH:MM: %s", i, s.End))
			return
		}
		rows = append(rows, &store.RecordingSchedule{
			ID:          uuid.NewString(),
			PolicyID:    policyID,
			DayOfWeek:   s.DayOfWeek,
			StartMinute: startMin,
			EndMinute:   endMin,
		})
	}
	if err := a.Store.RecordingSchedules.ReplaceAllForPolicy(ctx.Request.Context(), policyID, rows); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "policy.schedule_replaced", "recording_policy", policyID, map[string]string{
		"count": strconv.Itoa(len(rows)),
	})
	a.onV1RecordingSchedulesGet(ctx)
}

// parseHHMM accepts "HH:MM" (00:00..23:59 inclusive) and returns the
// minute-of-day. Returns ok=false on malformed input.
func parseHHMM(s string) (int, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// formatHHMM renders a minute-of-day as HH:MM.
func formatHHMM(min int) string {
	if min < 0 {
		min = 0
	}
	if min > 1439 {
		min = 1439
	}
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}
