// Package api: /v1/recording-policies handlers per ADR 0009 §D5
// Recording-policies and §D8 RecordingPolicy persistence.
//
// RecordingPolicy is a canonical entity exposed for the first time by
// this ADR. Policies persist to mediamtx.yml as a top-level
// recordingPolicies: key (per ADR 0009 §D8), keyed by policy UUID.
// On startup, if the key is non-empty, persisted entries load
// directly; if empty, synthesis from per-camera recording config
// happens on first canonical-surface access (deflated through
// defs.SynthesizePoliciesFromPaths). PATCH/POST/DELETE through the
// canonical surface persist the change via Parent.APIConfigSet, the
// same path conf.Path mutations already use.
//
// The map field on conf.Conf is map[string]*conf.RecordingPolicyConfig.
// The conf-package mirror exists to break the defs↔conf import
// cycle (defs imports conf); conversion helpers in
// internal/defs/recording_policy_translate.go.
package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
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
// first access. Per ADR 0009 §D8 the map persists to mediamtx.yml as
// a top-level recordingPolicies: key; if non-empty on startup, the
// persisted entries are loaded directly. If empty, this function
// synthesizes from per-camera recording config and writes the
// synthesized result back to a.Conf.RecordingPolicies. Synthesis-on-
// load does NOT call APIConfigSet — persistence happens on the next
// canonical-surface mutation. Caller must hold a write lock on
// a.mutex when this might mutate.
//
// Returns the policies map keyed by UUID, with conf
// RecordingPolicyConfig values converted to canonical
// defs.RecordingPolicy.
func (a *API) ensurePoliciesSynthesized() map[string]*defs.RecordingPolicy {
	if a.Conf.RecordingPolicies != nil && len(a.Conf.RecordingPolicies) > 0 {
		return policiesAsTyped(a.Conf.RecordingPolicies)
	}

	policies, cameraNameToPolicyID := defs.SynthesizePoliciesFromPaths(
		a.Conf.Paths,
		a.Conf.TenantID,
		func() string { return uuid.New().String() },
	)

	a.Conf.RecordingPolicies = make(map[string]*conf.RecordingPolicyConfig, len(policies))
	for i := range policies {
		p := policies[i]
		a.Conf.RecordingPolicies[p.ID] = defs.RecordingPolicyToConfig(p)
	}
	for name, policyID := range cameraNameToPolicyID {
		if path, ok := a.Conf.Paths[name]; ok && path != nil {
			path.RecordingPolicyID = policyID
		}
	}
	return policiesAsTyped(a.Conf.RecordingPolicies)
}

// policiesAsTyped converts the conf-shape persistence map to the
// canonical defs.RecordingPolicy map the handlers serve from. Values
// in a.Conf.RecordingPolicies are conf.RecordingPolicyConfig; the wire
// surface needs defs.RecordingPolicy.
func policiesAsTyped(m map[string]*conf.RecordingPolicyConfig) map[string]*defs.RecordingPolicy {
	out := make(map[string]*defs.RecordingPolicy, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		p := defs.RecordingPolicyFromConfig(k, v)
		out[k] = &p
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
	if !a.guardAdminAction(ctx) {
		return
	}
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

	newConf := a.Conf.Clone()
	if newConf.RecordingPolicies == nil {
		newConf.RecordingPolicies = make(map[string]*conf.RecordingPolicyConfig)
	}
	newConf.RecordingPolicies[policy.ID] = defs.RecordingPolicyToConfig(policy)
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "policy.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		SubjectID:   policy.ID,
		Message:     "recording policy created",
		Attributes: map[string]string{
			"policy_id": policy.ID,
			"verb":      "create",
		},
	})
	a.emitConfigAppliedLocked("recording_policy", policy.ID, "create", map[string]string{"policy_id": policy.ID})

	ctx.JSON(http.StatusCreated, &policy)
}

func (a *API) onV1RecordingPoliciesPatch(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
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

	newConf := a.Conf.Clone()
	newConf.RecordingPolicies[id.String()] = defs.RecordingPolicyToConfig(merged)

	// Capture which path-names reference this policy BEFORE Validate, so
	// we can re-stamp RecordingPolicyID and apply the translator AFTER
	// Validate rebuilds newConf.Paths. (Validate rebuilds Paths from
	// OptionalPaths every call; RecordingPolicyID is a json:"-" linkage
	// field that gets blanked in the rebuild — same situation cameras
	// POST handles by stamping ID/RecordingPolicyID after Validate.)
	pathsForPolicy := make([]string, 0)
	for name, p := range newConf.Paths {
		if p != nil && p.RecordingPolicyID == id.String() {
			pathsForPolicy = append(pathsForPolicy, name)
		}
	}

	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Re-stamp the RecordingPolicyID and apply the translator on the
	// freshly-validated paths. Without this step, a PATCH that flips
	// Enabled (or any other recording-related field on the policy)
	// would update only the canonical state — the per-camera
	// conf.Path.Record field would stay stale and the recorder would
	// keep recording (or fail to start). defs.ApplyPolicyToPath stamps
	// Record / RecordPath / RecordFormat / part / segment /
	// delete-after fields from the policy onto each path.
	for _, name := range pathsForPolicy {
		p := newConf.Paths[name]
		if p == nil {
			continue
		}
		p.RecordingPolicyID = id.String()
		defs.ApplyPolicyToPath(p, merged)
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "policy.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		SubjectID:   merged.ID,
		Message:     "recording policy updated",
		Attributes: map[string]string{
			"policy_id": merged.ID,
			"verb":      "update",
		},
	})
	a.emitConfigAppliedLocked("recording_policy", merged.ID, "update", map[string]string{"policy_id": merged.ID})

	ctx.JSON(http.StatusOK, &merged)
}

func (a *API) onV1RecordingPoliciesDelete(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
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

	newConf := a.Conf.Clone()
	delete(newConf.RecordingPolicies, id.String())
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "policy.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindServer,
		SubjectID:   id.String(),
		Message:     "recording policy deleted",
		Attributes: map[string]string{
			"policy_id": id.String(),
			"verb":      "delete",
		},
	})
	a.emitConfigAppliedLocked("recording_policy", id.String(), "delete", map[string]string{"policy_id": id.String()})

	a.writeOK(ctx)
}
