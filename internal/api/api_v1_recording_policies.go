// Package api: /v1/recording-policies handlers per ADR 0009 §D5
// Recording-policies.
//
// RecordingPolicy is a canonical entity exposed for the first time by
// this ADR. The recorder still stores recording rules per-camera in
// conf.Path; the policies map is an in-memory cache keyed by UUID,
// produced by lazy synthesis on first access (defs.SynthesizePoliciesFromPaths).
//
// Per-restart persistence is intentional for Phase 2 — there are no
// stable cross-restart IDs in the pre-MS phase per ADR 0009 §D4.
// Synthesis re-runs from existing per-camera recording fields when the
// recorder restarts.
package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// recordingPolicyListResponse is the paginated list shape returned by
// GET /v1/recording-policies.
type recordingPolicyListResponse struct {
	ItemCount int                    `json:"item_count"`
	PageCount int                    `json:"page_count"`
	Items     []defs.RecordingPolicy `json:"items"`
}

// ensurePoliciesSynthesized lazily fills a.Conf.RecordingPolicies on
// first access. The map is in-memory only (yaml:"-" json:"-") and goes
// away on recorder restart, at which point the next list-call re-runs
// synthesis from per-camera recording fields. Caller must hold a write
// lock on a.mutex when this might mutate.
//
// Returns the policies map keyed by UUID. Values are *defs.RecordingPolicy
// stored as `any` to avoid the conf↔defs import cycle; this function
// surfaces them with their concrete type.
func (a *API) ensurePoliciesSynthesized() map[string]*defs.RecordingPolicy {
	if a.Conf.RecordingPolicies != nil && len(a.Conf.RecordingPolicies) > 0 {
		return policiesAsTyped(a.Conf.RecordingPolicies)
	}

	policies, cameraNameToPolicyID := defs.SynthesizePoliciesFromPaths(
		a.Conf.Paths,
		a.Conf.TenantID,
		func() string { return uuid.New().String() },
	)

	a.Conf.RecordingPolicies = make(map[string]any, len(policies))
	for i := range policies {
		p := policies[i]
		a.Conf.RecordingPolicies[p.ID] = &p
	}
	for name, policyID := range cameraNameToPolicyID {
		if path, ok := a.Conf.Paths[name]; ok && path != nil {
			path.RecordingPolicyID = policyID
		}
	}
	return policiesAsTyped(a.Conf.RecordingPolicies)
}

// policiesAsTyped reads the in-memory map back as the typed value. The
// map is always written with *defs.RecordingPolicy values; this helper
// just enforces that contract at the read boundary.
func policiesAsTyped(m map[string]any) map[string]*defs.RecordingPolicy {
	out := make(map[string]*defs.RecordingPolicy, len(m))
	for k, v := range m {
		if p, ok := v.(*defs.RecordingPolicy); ok {
			out[k] = p
		}
	}
	return out
}

// sortedPolicyIDs returns policy UUIDs from the in-memory map in
// deterministic order (lexical UUID).
func sortedPolicyIDs(m map[string]*defs.RecordingPolicy) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (a *API) onV1RecordingPoliciesList(ctx *gin.Context) {
	a.mutex.Lock() // write lock — synthesis may mutate
	policies := a.ensurePoliciesSynthesized()
	a.mutex.Unlock()

	ids := sortedPolicyIDs(policies)
	out := &recordingPolicyListResponse{
		Items: make([]defs.RecordingPolicy, 0, len(ids)),
	}
	for _, id := range ids {
		p := policies[id]
		if p == nil {
			continue
		}
		// Stamp tenant_id at the response edge per the established
		// pattern; the map values may have been synthesized at startup
		// when the conf tenant was already set, but reading a fresh
		// tenant_id at every response keeps the contract simple.
		copy := *p
		copy.TenantID = a.tenantID()
		out.Items = append(out.Items, copy)
	}

	out.ItemCount = len(out.Items)
	pageCount, err := paginate(&out.Items, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	out.PageCount = pageCount

	ctx.JSON(http.StatusOK, out)
}

func (a *API) onV1RecordingPoliciesGet(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid policy id: %w", err))
		return
	}

	a.mutex.Lock() // write lock — first-access synthesis may mutate
	policies := a.ensurePoliciesSynthesized()
	policy, ok := policies[id.String()]
	a.mutex.Unlock()

	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording policy not found"))
		return
	}
	resp := *policy
	resp.TenantID = a.tenantID()
	ctx.JSON(http.StatusOK, &resp)
}

func (a *API) onV1RecordingPoliciesPost(ctx *gin.Context) {
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var policy defs.RecordingPolicy
	if err := json.Unmarshal(body, &policy); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if policy.TenantID != "" && policy.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.ensurePoliciesSynthesized()

	policy.ID = uuid.New().String()
	policy.TenantID = a.Conf.TenantID
	now := time.Now().UTC()
	policy.CreatedAt = now
	policy.UpdatedAt = now

	if a.Conf.RecordingPolicies == nil {
		a.Conf.RecordingPolicies = make(map[string]any)
	}
	stored := policy
	a.Conf.RecordingPolicies[policy.ID] = &stored

	ctx.JSON(http.StatusCreated, &policy)
}

func (a *API) onV1RecordingPoliciesPatch(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid policy id: %w", err))
		return
	}

	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	var meta struct {
		TenantID *string `json:"tenant_id"`
	}
	if jerr := json.Unmarshal(body, &meta); jerr == nil &&
		meta.TenantID != nil && *meta.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()
	policies := a.ensurePoliciesSynthesized()

	existing, ok := policies[id.String()]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording policy not found"))
		return
	}

	// Decode the patch onto a fresh struct, then overlay onto the
	// existing one. Snake_case JSON tags on defs.RecordingPolicy carry
	// the canonical shape.
	merged := *existing
	if err := json.Unmarshal(body, &merged); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	// The id is route-pinned; client cannot rewrite it.
	merged.ID = existing.ID
	merged.CreatedAt = existing.CreatedAt
	merged.TenantID = a.Conf.TenantID
	merged.UpdatedAt = time.Now().UTC()

	a.Conf.RecordingPolicies[id.String()] = &merged
	ctx.JSON(http.StatusOK, &merged)
}

func (a *API) onV1RecordingPoliciesDelete(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid policy id: %w", err))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.ensurePoliciesSynthesized()

	if _, ok := a.Conf.RecordingPolicies[id.String()]; !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("recording policy not found"))
		return
	}

	// Reject deletion if any Camera still references this policy per
	// ADR 0009 §D5 Recording-policies "Rejects deletion if any
	// Camera.recording_policy_id still references this policy."
	for _, p := range a.Conf.Paths {
		if p != nil && p.RecordingPolicyID == id.String() {
			a.writeError(ctx, http.StatusConflict,
				fmt.Errorf("cannot delete policy %s: still referenced by camera %s", id.String(), p.Name))
			return
		}
	}

	delete(a.Conf.RecordingPolicies, id.String())
	a.writeOK(ctx)
}
