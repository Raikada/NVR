package camerasync

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// fakeApplier records calls and exposes a settable current cache.
// Used by all apply tests; the recorder's real Applier (in
// internal/api) gets exercised by integration tests in the api
// package.
type fakeApplier struct {
	current map[string]LocalCamera

	addCalls    []DesiredStateItem
	updateCalls []DesiredStateItem
	deleteCalls []string

	addErr    error
	updateErr error
	deleteErr error

	engageCalls int
}

func (f *fakeApplier) CurrentCameras() map[string]LocalCamera {
	out := make(map[string]LocalCamera, len(f.current))
	for k, v := range f.current {
		out[k] = v
	}
	return out
}

func (f *fakeApplier) AddCamera(_ context.Context, item DesiredStateItem) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.addCalls = append(f.addCalls, item)
	if f.current == nil {
		f.current = map[string]LocalCamera{}
	}
	f.current[item.ID] = LocalCamera{ID: item.ID, Name: item.Name, Version: item.Version}
	return nil
}

func (f *fakeApplier) UpdateCamera(_ context.Context, item DesiredStateItem) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updateCalls = append(f.updateCalls, item)
	f.current[item.ID] = LocalCamera{ID: item.ID, Name: item.Name, Version: item.Version}
	return nil
}

func (f *fakeApplier) DeleteCamera(_ context.Context, id string) error {
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
	applied   []map[string]string
	reverts   []struct {
		CameraID                            string
		PriorLocalVersion, RestoredMSVersion int64
	}
}

func (e *fakeAuditEmitter) EmitCameraConfigApplied(cameraID, verb, source string, version int64, attrs map[string]string) {
	cp := map[string]string{
		"camera_id": cameraID,
		"verb":      verb,
		"source":    source,
	}
	for k, v := range attrs {
		cp[k] = v
	}
	e.applied = append(e.applied, cp)
}

func (e *fakeAuditEmitter) EmitCameraLocalOverrideReverted(cameraID string, prior, restored int64) {
	e.reverts = append(e.reverts, struct {
		CameraID                            string
		PriorLocalVersion, RestoredMSVersion int64
	}{cameraID, prior, restored})
}

func mkItem(id, name string, version int64) DesiredStateItem {
	return DesiredStateItem{
		Camera: defs.Camera{
			ID:         id,
			Name:       name,
			SourceType: defs.CameraSourceTypeRTSP,
			SourceURL:  "rtsp://cam.local/" + name,
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
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 1)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.addCalls, 1)
	require.Len(t, app.updateCalls, 0)
	require.Len(t, app.deleteCalls, 0)
	require.Equal(t, 1, app.engageCalls)
	require.Len(t, report.Items, 1)
	require.Equal(t, OutcomeAdded, report.Items[0].Outcome)
	require.Equal(t, int64(1), report.Items[0].ToVersion)

	require.Len(t, emit.applied, 1)
	require.Equal(t, "create", emit.applied[0]["verb"])
	require.Equal(t, "poll", emit.applied[0]["source"])
}

func TestApply_UpdateExisting(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalCamera{
			"cam-1": {ID: "cam-1", Name: "front", Version: 1},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        2,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 2)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.updateCalls, 1)
	require.Len(t, app.addCalls, 0)
	require.Len(t, app.deleteCalls, 0)
	require.Len(t, report.Items, 1)
	require.Equal(t, OutcomeUpdated, report.Items[0].Outcome)
	require.Equal(t, int64(1), report.Items[0].FromVersion)
	require.Equal(t, int64(2), report.Items[0].ToVersion)
	require.Len(t, emit.applied, 1)
	require.Equal(t, "update", emit.applied[0]["verb"])
}

func TestApply_DeleteMissing(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalCamera{
			"cam-1": {ID: "cam-1", Name: "front", Version: 1},
			"cam-2": {ID: "cam-2", Name: "back", Version: 1},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	// Desired-state retains cam-1 only — cam-2 should be deleted.
	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 1)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.deleteCalls, 1)
	require.Equal(t, "cam-2", app.deleteCalls[0])
	require.Equal(t, 1, app.engageCalls)

	// One unchanged + one deleted.
	require.Len(t, report.Items, 2)
	var deleted, unchanged int
	for _, it := range report.Items {
		switch it.Outcome {
		case OutcomeDeleted:
			deleted++
		case OutcomeUnchanged:
			unchanged++
		}
	}
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, unchanged)

	// The delete audit emit carries source=poll + verb=delete + the
	// prior_version attribute.
	var deleteEmit map[string]string
	for _, e := range emit.applied {
		if e["verb"] == "delete" {
			deleteEmit = e
			break
		}
	}
	require.NotNil(t, deleteEmit)
	require.Equal(t, "1", deleteEmit["prior_version"])
}

func TestApply_NoOpOnEqualVersion(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalCamera{
			"cam-1": {ID: "cam-1", Name: "front", Version: 5},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        5,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 5)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.addCalls, 0)
	require.Len(t, app.updateCalls, 0)
	require.Len(t, app.deleteCalls, 0)
	// No mutation => no engage call.
	require.Equal(t, 0, app.engageCalls)
	require.Len(t, emit.applied, 0)

	require.Len(t, report.Items, 1)
	require.Equal(t, OutcomeUnchanged, report.Items[0].Outcome)
}

func TestApply_VersionRegressionLoudAudit(t *testing.T) {
	// Local has version 9 (post-break-glass), MS sends version 6.
	app := &fakeApplier{
		current: map[string]LocalCamera{
			"cam-1": {ID: "cam-1", Name: "front", Version: 9},
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        6,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 6)},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.updateCalls, 1)
	require.Len(t, report.Items, 1)
	require.Equal(t, OutcomeVersionRegression, report.Items[0].Outcome)
	require.Equal(t, int64(9), report.Items[0].FromVersion)
	require.Equal(t, int64(6), report.Items[0].ToVersion)

	// Loud audit: a local_override_reverted plus a config.applied.
	require.Len(t, emit.reverts, 1)
	require.Equal(t, "cam-1", emit.reverts[0].CameraID)
	require.Equal(t, int64(9), emit.reverts[0].PriorLocalVersion)
	require.Equal(t, int64(6), emit.reverts[0].RestoredMSVersion)

	// The applied entry carries the prior/restored attributes.
	require.Len(t, emit.applied, 1)
	require.Equal(t, "9", emit.applied[0]["prior_local_version"])
	require.Equal(t, "6", emit.applied[0]["restored_ms_version"])
}

func TestApply_MixedBatch(t *testing.T) {
	app := &fakeApplier{
		current: map[string]LocalCamera{
			"cam-1": {ID: "cam-1", Name: "front", Version: 1}, // unchanged
			"cam-2": {ID: "cam-2", Name: "back", Version: 1},  // updated to v3
			"cam-4": {ID: "cam-4", Name: "side", Version: 2},  // deleted
		},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        3,
		Cameras: []DesiredStateItem{
			mkItem("cam-1", "front", 1),
			mkItem("cam-2", "back", 3),
			mkItem("cam-3", "garage", 1),
		},
	}
	report := Apply(context.Background(), app, emit, mu, desired)

	require.Len(t, app.addCalls, 1)
	require.Equal(t, "cam-3", app.addCalls[0].ID)
	require.Len(t, app.updateCalls, 1)
	require.Equal(t, "cam-2", app.updateCalls[0].ID)
	require.Len(t, app.deleteCalls, 1)
	require.Equal(t, "cam-4", app.deleteCalls[0])

	counts := report.Counts()
	require.Equal(t, 1, counts[OutcomeAdded])
	require.Equal(t, 1, counts[OutcomeUpdated])
	require.Equal(t, 1, counts[OutcomeDeleted])
	require.Equal(t, 1, counts[OutcomeUnchanged])
	require.Equal(t, 1, app.engageCalls)
}

func TestApply_Idempotent(t *testing.T) {
	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 1)},
	}
	r1 := Apply(context.Background(), app, emit, mu, desired)
	require.Equal(t, OutcomeAdded, r1.Items[0].Outcome)

	r2 := Apply(context.Background(), app, emit, mu, desired)
	require.Len(t, r2.Items, 1)
	require.Equal(t, OutcomeUnchanged, r2.Items[0].Outcome)
	// No additional calls beyond the first add.
	require.Len(t, app.addCalls, 1)
	require.Len(t, app.updateCalls, 0)
	require.Len(t, app.deleteCalls, 0)
}

func TestApply_FailureIsReported_NotPropagated(t *testing.T) {
	app := &fakeApplier{addErr: errors.New("disk full")}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		Cameras: []DesiredStateItem{
			mkItem("cam-1", "front", 1),
			mkItem("cam-2", "back", 1),
		},
	}
	// Only cam-1 fails (addErr fires unconditionally — both will fail
	// in this fake; that's OK, the assertion is about reporting).
	report := Apply(context.Background(), app, emit, mu, desired)
	failed := 0
	for _, it := range report.Items {
		if it.Outcome == OutcomeFailed {
			failed++
			require.Error(t, it.Err)
		}
	}
	require.Equal(t, 2, failed)
	// No audit emits for failed adds.
	require.Len(t, emit.applied, 0)
}

func TestApply_MutexSerializesAccess(t *testing.T) {
	// Race-detector test: two concurrent Apply calls against the same
	// applier + mutex must not race. Any race surfaces under -race.
	app := &fakeApplier{
		current: map[string]LocalCamera{},
	}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	desired1 := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        1,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 1)},
	}
	desired2 := DesiredState{
		RecordingServerID: "rec-1",
		VersionSet:        2,
		Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 2)},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		Apply(context.Background(), app, emit, mu, desired1)
	}()
	go func() {
		defer wg.Done()
		Apply(context.Background(), app, emit, mu, desired2)
	}()
	wg.Wait()

	// At least one apply happened; the final cached version is one of
	// the two desired versions.
	require.NotEmpty(t, app.current)
	c := app.current["cam-1"]
	require.True(t, c.Version == 1 || c.Version == 2,
		"expected cached version 1 or 2, got %d", c.Version)
}
