// Package api: /v1/audit handler per ADR 0006.
//
// GET only — the chain is append-only and the recorder does not
// expose modification operations. Filters: time range
// (occurred_after, occurred_before), kind (multi-value), actor_kind,
// actor_id, outcome, resource_kind. Pagination via the existing
// items_per_page / page convention.
//
// The audit chain is the security-focused record that the eventual
// MS aggregation layer ingests; this endpoint exists primarily for
// operator visibility and for tests of the chain machinery. Per ADR
// 0006 D2 the recorder is one emitter — every entry returned here
// has emitter_kind=recording_server.
package api //nolint:revive

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// auditListResponse is the on-wire shape of GET /v1/audit. Mirrors
// eventListResponse (item_count, page_count, items) for consistency
// across the canonical /v1/* surface.
type auditListResponse struct {
	ItemCount int                   `json:"item_count"`
	PageCount int                   `json:"page_count"`
	Items     []defs.AuditLogEntry  `json:"items"`
}

// auditFilters captures the query-string filters accepted by GET
// /v1/audit.
type auditFilters struct {
	occurredAfter  *time.Time
	occurredBefore *time.Time
	kinds          []string
	actorKind      defs.AuditActorKind
	actorID        string
	outcome        defs.AuditOutcome
	resourceKind   string
}

// parseAuditActorKind validates an actor_kind query value.
func parseAuditActorKind(v string) (defs.AuditActorKind, bool) {
	if v == "" {
		return "", true
	}
	switch defs.AuditActorKind(v) {
	case defs.AuditActorKindCloudUser,
		defs.AuditActorKindLocalUser,
		defs.AuditActorKindServiceAccount,
		defs.AuditActorKindSystem,
		defs.AuditActorKindUnauthenticated:
		return defs.AuditActorKind(v), true
	}
	return "", false
}

// parseAuditOutcome validates an outcome query value.
func parseAuditOutcome(v string) (defs.AuditOutcome, bool) {
	if v == "" {
		return "", true
	}
	switch defs.AuditOutcome(v) {
	case defs.AuditOutcomeSuccess,
		defs.AuditOutcomeFailure,
		defs.AuditOutcomeDenied:
		return defs.AuditOutcome(v), true
	}
	return "", false
}

func parseAuditFilters(ctx *gin.Context) (auditFilters, error) {
	var f auditFilters

	if v := ctx.Query("occurred_after"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid occurred_after: %w", err)
		}
		f.occurredAfter = &t
	}
	if v := ctx.Query("occurred_before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid occurred_before: %w", err)
		}
		f.occurredBefore = &t
	}
	if kinds := ctx.QueryArray("kind"); len(kinds) > 0 {
		f.kinds = kinds
	}
	if v := ctx.Query("actor_kind"); v != "" {
		ak, ok := parseAuditActorKind(v)
		if !ok {
			return f, fmt.Errorf("invalid actor_kind: %s", v)
		}
		f.actorKind = ak
	}
	f.actorID = ctx.Query("actor_id")
	if v := ctx.Query("outcome"); v != "" {
		o, ok := parseAuditOutcome(v)
		if !ok {
			return f, fmt.Errorf("invalid outcome: %s", v)
		}
		f.outcome = o
	}
	f.resourceKind = ctx.Query("resource_kind")

	return f, nil
}

// match returns true when the supplied entry passes every set filter.
// "kind" filters on the action vocabulary (ADR 0006 D7) — i.e. it's
// "?kind=auth.login" matched against AuditLogEntry.Action — to mirror
// the operator-mental-model match between Event.Kind and
// AuditLogEntry.Action.
func (f auditFilters) match(e *defs.AuditLogEntry) bool {
	if f.occurredAfter != nil && e.OccurredAt.Before(*f.occurredAfter) {
		return false
	}
	if f.occurredBefore != nil && e.OccurredAt.After(*f.occurredBefore) {
		return false
	}
	if len(f.kinds) > 0 {
		ok := false
		for _, k := range f.kinds {
			if e.Action == k {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.actorKind != "" && e.ActorKind != f.actorKind {
		return false
	}
	if f.actorID != "" && e.ActorID != f.actorID {
		return false
	}
	if f.outcome != "" && e.Outcome != f.outcome {
		return false
	}
	if f.resourceKind != "" && e.ResourceKind != f.resourceKind {
		return false
	}
	return true
}

// onV1AuditList serves GET /v1/audit. Filters, sorts newest-first,
// paginates, and returns the in-memory chain buffer's contents.
func (a *API) onV1AuditList(ctx *gin.Context) {
	filters, err := parseAuditFilters(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	all := defaultAuditBuffer().Snapshot()

	filtered := make([]defs.AuditLogEntry, 0, len(all))
	for i := range all {
		if filters.match(&all[i]) {
			filtered = append(filtered, all[i])
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].OccurredAt.Equal(filtered[j].OccurredAt) {
			return filtered[i].OccurredAt.After(filtered[j].OccurredAt)
		}
		return filtered[i].ID < filtered[j].ID
	})

	resp := auditListResponse{Items: filtered}
	resp.ItemCount = len(resp.Items)

	pageCount, err := paginate(&resp.Items, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	resp.PageCount = pageCount

	ctx.JSON(http.StatusOK, resp)
}
