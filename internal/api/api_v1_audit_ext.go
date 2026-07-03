// Package api: /v1/audit/{export,purge} per Phase 5 Task 5.8.
//
// The base GET /v1/audit + GET /v1/audit/:id remain in api_v1_audit.go,
// backed by the in-memory chain buffer. Export streams from the
// store-backed AuditLogRepo (which sees everything, including evicted
// entries from the in-memory ring); purge admin-only, audited FIRST,
// then the delete runs.
package api //nolint:revive

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1AuditExtensions(r gin.IRouter) {
	r.POST("/audit/export", rbac.RequirePerm(rbac.PermAuditRead, a.auditEmitter()), a.onV1AuditExport)
	r.POST("/audit/purge", rbac.RequirePerm(rbac.PermAuditPurge, a.auditEmitter()), a.onV1AuditPurge)
}

// onV1AuditExport streams audit_log entries between from..to query
// params (RFC3339) as ndjson or csv.
func (a *API) onV1AuditExport(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	from, to, err := parseExportTimeRange(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	format := ctx.Query("format")
	if format == "" {
		format = "ndjson"
	}
	stream, err := a.Store.AuditLog.StreamForExport(ctx.Request.Context(), from, to)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	switch format {
	case "ndjson":
		ctx.Header("Content-Type", "application/x-ndjson")
		ctx.Status(http.StatusOK)
		enc := json.NewEncoder(ctx.Writer)
		for e := range stream {
			if err := enc.Encode(auditEntryToWire(e)); err != nil {
				return
			}
		}
	case "csv":
		ctx.Header("Content-Type", "text/csv")
		ctx.Status(http.StatusOK)
		w := csv.NewWriter(ctx.Writer)
		_ = w.Write([]string{"id", "occurred_at", "actor_user_id", "actor_username", "actor_ip",
			"action", "target_kind", "target_id", "details"})
		for e := range stream {
			_ = w.Write([]string{
				e.ID, e.OccurredAt.Format(time.RFC3339),
				e.ActorUserID, e.ActorUsername, e.ActorIP,
				e.Action, e.TargetKind, e.TargetID, e.Details,
			})
		}
		w.Flush()
	default:
		a.writeError(ctx, http.StatusBadRequest, errors.New("format must be 'ndjson' or 'csv'"))
		return
	}
	a.emitMutationAudit(ctx, "audit.exported", "audit_log", "", map[string]string{
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "format": format,
	})
}

// auditEntryWire is a serializable shape for the AuditLog repo rows.
type auditEntryWire struct {
	ID            string    `json:"id"`
	OccurredAt    time.Time `json:"occurred_at"`
	ActorUserID   string    `json:"actor_user_id,omitempty"`
	ActorUsername string    `json:"actor_username,omitempty"`
	ActorIP       string    `json:"actor_ip,omitempty"`
	Action        string    `json:"action"`
	TargetKind    string    `json:"target_kind,omitempty"`
	TargetID      string    `json:"target_id,omitempty"`
	BeforeJSON    string    `json:"before_json,omitempty"`
	AfterJSON     string    `json:"after_json,omitempty"`
	Details       string    `json:"details,omitempty"`
}

func auditEntryToWire(e *store.AuditEntry) auditEntryWire {
	return auditEntryWire{
		ID: e.ID, OccurredAt: e.OccurredAt,
		ActorUserID: e.ActorUserID, ActorUsername: e.ActorUsername, ActorIP: e.ActorIP,
		Action: e.Action, TargetKind: e.TargetKind, TargetID: e.TargetID,
		BeforeJSON: e.BeforeJSON, AfterJSON: e.AfterJSON, Details: e.Details,
	}
}

// onV1AuditPurge removes rows older than the supplied duration. The
// purge action audit row is emitted BEFORE the delete so a purge that
// erases its own record-of-having-been-purged is not possible.
//
// The system.audit_purged row is also written to the store-backed
// AuditLogRepo (in addition to the in-memory chain) with OccurredAt =
// now(); since now() is after cutoff, the row survives the subsequent
// purge sweep.
func (a *API) onV1AuditPurge(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	olderThanStr := ctx.Query("older_than")
	if olderThanStr == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("older_than query param required (e.g. 720h)"))
		return
	}
	d, err := time.ParseDuration(olderThanStr)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid older_than duration: %w", err))
		return
	}
	cutoff := time.Now().Add(-d)
	// Emit to the in-memory chain (read-side endpoints).
	a.emitMutationAudit(ctx, "system.audit_purged", "audit_log", "", map[string]string{
		"older_than": d.String(),
		"cutoff":     cutoff.Format(time.RFC3339),
	})
	// Also persist a parallel store-backed row so the action survives
	// the subsequent purge.
	principal := principalFromContext(ctx)
	_ = a.Store.AuditLog.Insert(ctx.Request.Context(), &store.AuditEntry{
		OccurredAt:  time.Now().UTC(),
		ActorUserID: principal.Sub,
		ActorIP:     clientIP(ctx),
		Action:      "system.audit_purged",
		TargetKind:  "audit_log",
		Details:     "older_than=" + d.String(),
	})
	n, err := a.Store.AuditLog.PurgeOlderThan(ctx.Request.Context(), cutoff)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"deleted": n})
}

func parseExportTimeRange(ctx *gin.Context) (time.Time, time.Time, error) {
	fromStr := ctx.Query("from")
	toStr := ctx.Query("to")
	if fromStr == "" || toStr == "" {
		return time.Time{}, time.Time{}, errors.New("from and to query params required")
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
	}
	return from, to, nil
}
