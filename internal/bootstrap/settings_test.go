package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunSeedsSystemSettings verifies the timezone + snapshot_root +
// clip_root defaults are inserted when missing.
func TestRunSeedsSystemSettings(t *testing.T) {
	t.Setenv(envBootstrapPassword, "")
	st := openTestStore(t)
	idDir := t.TempDir()
	recordingsRoot := t.TempDir()

	res, err := Run(context.Background(), st, idDir, Options{RecordingsRoot: recordingsRoot})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.SettingsSeeded, 3)

	tz, err := st.SystemSettings.Get(context.Background(), "timezone")
	require.NoError(t, err)
	require.NotEmpty(t, tz.Value)

	snap, err := st.SystemSettings.Get(context.Background(), "snapshot_root")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(recordingsRoot, "snapshots"), snap.Value)

	clip, err := st.SystemSettings.Get(context.Background(), "clip_root")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(recordingsRoot, "clips"), clip.Value)
}

// TestRunRespectsExistingSettings verifies a setting that's already in
// place is NOT overwritten on the next Run.
func TestRunRespectsExistingSettings(t *testing.T) {
	t.Setenv(envBootstrapPassword, "")
	st := openTestStore(t)
	idDir := t.TempDir()
	recordingsRoot := t.TempDir()

	// updated_by must be a valid local_users.id (FK) or empty; pass "" so
	// the seed predates any admin row.
	require.NoError(t, st.SystemSettings.Upsert(context.Background(), "timezone", "America/Los_Angeles", ""))
	require.NoError(t, st.SystemSettings.Upsert(context.Background(), "snapshot_root", "/data/snaps", ""))

	_, err := Run(context.Background(), st, idDir, Options{RecordingsRoot: recordingsRoot})
	require.NoError(t, err)

	tz, err := st.SystemSettings.Get(context.Background(), "timezone")
	require.NoError(t, err)
	require.Equal(t, "America/Los_Angeles", tz.Value)
	snap, err := st.SystemSettings.Get(context.Background(), "snapshot_root")
	require.NoError(t, err)
	require.Equal(t, "/data/snaps", snap.Value)

	// clip_root was missing → it gets the default.
	clip, err := st.SystemSettings.Get(context.Background(), "clip_root")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(recordingsRoot, "clips"), clip.Value)
}
