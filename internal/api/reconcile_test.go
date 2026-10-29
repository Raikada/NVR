package api //nolint:revive

import (
	ctxpkg "context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/store"
)

// A camera row in the store but missing from the conf (the conf file
// was replaced — e.g. a container upgrade discarded the runtime-written
// config) must be folded back into the conf at boot, with the policy's
// record settings applied. Otherwise GET /v1/cameras (which lists conf
// paths) reports an empty fleet while the store still streams.
func TestReconcileStoreCamerasRestoresMissingPaths(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	require.NoError(t, st.Cameras.Insert(ctxpkg.Background(), &store.Camera{
		ID: "cam-rec-1", Name: "lost_cam", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.7:554/main", Enabled: true,
	}))

	cnf := tempConf(t, "api: yes\n")

	added, err := ReconcileStoreCameras(ctxpkg.Background(), cnf, st)
	require.NoError(t, err)
	require.Equal(t, 1, added)

	p, ok := cnf.Paths["lost_cam"]
	require.True(t, ok, "conf must regain the camera path")
	require.Equal(t, "rtsp://192.0.2.7:554/main", p.Source)
	require.Equal(t, "cam-rec-1", p.ID)
	// Default policy is continuous — record must be on again.
	require.True(t, p.Record)

	// Idempotent: second run adds nothing.
	added, err = ReconcileStoreCameras(ctxpkg.Background(), cnf, st)
	require.NoError(t, err)
	require.Equal(t, 0, added)
}

// A conf path whose policy says record but whose record flag is off —
// the damage left by the historical PATCH/PUT bug that rebuilt paths
// without their policy-derived record fields — must be healed at boot.
func TestReconcileStoreCamerasHealsRecordFlag(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  wounded_cam:\n"+
		"    source: rtsp://192.0.2.9:554/main\n"+
		"    recordingPolicyId: 00000000-0000-0000-0000-000000000001\n"+
		"    record: false\n")
	require.False(t, cnf.Paths["wounded_cam"].Record)
	require.Equal(t, "00000000-0000-0000-0000-000000000001", cnf.Paths["wounded_cam"].RecordingPolicyID,
		"policy linkage must survive conf load")
	pc := cnf.RecordingPolicies[cnf.Paths["wounded_cam"].RecordingPolicyID]
	require.NotNil(t, pc, "default policy must be seeded")
	pol := defs.RecordingPolicyFromConfig("00000000-0000-0000-0000-000000000001", pc)
	require.True(t, pol.Enabled)
	require.Equal(t, defs.RecordingPolicyMode("continuous"), pol.Mode)

	repaired, err := ReconcileStoreCameras(ctxpkg.Background(), cnf, st)
	require.NoError(t, err)
	require.Equal(t, 1, repaired)
	require.True(t, cnf.Paths["wounded_cam"].Record, "policy says record — heal must re-enable it")

	// Idempotent: second run repairs nothing.
	repaired, err = ReconcileStoreCameras(ctxpkg.Background(), cnf, st)
	require.NoError(t, err)
	require.Equal(t, 0, repaired)
}
