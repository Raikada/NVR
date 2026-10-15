package api //nolint:revive

import (
	ctxpkg "context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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
