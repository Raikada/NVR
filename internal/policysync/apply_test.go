package policysync

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeApplier struct {
	current map[string]LocalPolicy

	addCalls    []DesiredStateItem
	updateCalls []DesiredStateItem
	deleteCalls []string

	addErr    error
	updateErr error
	deleteErr error

	engageCalls int
}

func (f *fakeApplier) CurrentPolicies() map[string]LocalPolicy {
	out := make(map[string]LocalPolicy, len(f.current))
	for k, v := range f.current {
		out[k] = v
	}
	return out
}

func (f *fakeApplier) AddPolicy(_ context.Context, item DesiredStateItem) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.addCalls = append(f.addCalls, item)
	if f.current == nil {
		f.current = map[string]LocalPolicy{}
	}
	f.current[item.ID] = LocalPolicy{ID: item.ID, Name: item.Name, Version: item.Version}
	return nil
}

func (f *fakeApplier) UpdatePolicy(_ context.Context, item DesiredStateItem) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updateCalls = append(f.updateCalls, item)
	f.current[item.ID] = LocalPolicy{ID: item.ID, Name: item.Name, Version: item.Version}
	return nil
}

func (f *fakeApplier) DeletePolicy(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, id)
	delete(f.current, id)
	return nil
}

func (f *fakeApplier) EngageLockdownIfNeeded() error {
	f.engageCalls++
	return nil
}

type fakeAuditEmitter struct {
	applied []map[string]string
	reverts []struct {
		PolicyID                             string
		PriorLocalVersion, RestoredMSVersion int64
	}
}

func (e *fakeAuditEmitter) EmitPolicyConfigApplied(policyID, verb, source string, version int64, attrs map[string]string) {
	cp := map[string]string{
		"policy_id": policyID,
		"verb":      verb,
		"source":    source,
	}
	for k, v := range attrs {
		cp[k] = v
	}
	e.applied = append(e.applied, cp)
}

func (e *fakeAuditEmitter) EmitPolicyLocalOverrideReverted(policyID string, prior, restored int64) {
	e.reverts = append(e.reverts, struct {
		PolicyID                             string
		PriorLocalVersion, RestoredMSVersion int64
	}{policyID, prior, restored})
}

func mkItem(id, name string, version int64) DesiredStateItem {
	return DesiredStateItem{
		PolicyShape: PolicyShape{
			ID:        id,
			Name:      name,
			Mode:      "continuous",
			Container: "fmp4",
			Enabled:   true,
		},
		Version: version,
	}
}

func TestApply_AddNew(t *testing.T) {
	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production", 1)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.addCalls, 1)
	require.Len(t, report.Items, 1)
	require.Equal(t, OutcomeAdded, report.Items[0].Outcome)
	require.Equal(t, 1, app.engageCalls)
}

func TestApply_NoOpOnSameVersion(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalPolicy{
			"p1": {ID: "p1", Name: "production", Version: 1},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production", 1)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Empty(t, app.addCalls)
	require.Empty(t, app.updateCalls)
	require.Equal(t, OutcomeUnchanged, report.Items[0].Outcome)
	// EngageLockdownIfNeeded only fires when something was applied.
	require.Equal(t, 0, app.engageCalls)
}

func TestApply_UpdateOnHigherVersion(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalPolicy{
			"p1": {ID: "p1", Name: "production", Version: 1},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        2,
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production-renamed", 2)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.updateCalls, 1)
	require.Equal(t, OutcomeUpdated, report.Items[0].Outcome)
}

func TestApply_VersionRegressionEmitsLoudAudit(t *testing.T) {
	// Local version is higher than MS version: implies prior break-glass.
	// MS still wins; we emit local_override_reverted + apply MS shape.
	app := &fakeApplier{
		current: map[string]LocalPolicy{
			"p1": {ID: "p1", Name: "production", Version: 5},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        2,
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production", 2)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Equal(t, OutcomeVersionRegression, report.Items[0].Outcome)
	require.Len(t, emit.reverts, 1)
	require.Equal(t, int64(5), emit.reverts[0].PriorLocalVersion)
	require.Equal(t, int64(2), emit.reverts[0].RestoredMSVersion)
}

func TestApply_TombstoneMissingPolicies(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalPolicy{
			"p1": {ID: "p1", Name: "production", Version: 1},
			"p2": {ID: "p2", Name: "side", Version: 1},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        2,
		// Only p1 in the response — p2 is tombstoned.
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production", 1)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.deleteCalls, 1)
	require.Equal(t, "p2", app.deleteCalls[0])
	// Report contains both entries (unchanged + deleted).
	require.Len(t, report.Items, 2)
	counts := report.Counts()
	require.Equal(t, 1, counts[OutcomeUnchanged])
	require.Equal(t, 1, counts[OutcomeDeleted])
}

func TestApply_AddFailureDoesNotEngage(t *testing.T) {
	app := &fakeApplier{addErr: errors.New("boom")}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production", 1)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Equal(t, OutcomeFailed, report.Items[0].Outcome)
	require.Equal(t, 0, app.engageCalls)
}

func TestApply_IdempotentReplay(t *testing.T) {
	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		RecordingPolicies: []DesiredStateItem{mkItem("p1", "production", 1)},
	}
	Apply(context.Background(), app, emit, mu, desired)
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.addCalls, 1)
	for _, it := range report.Items {
		require.Equal(t, OutcomeUnchanged, it.Outcome)
	}
}
