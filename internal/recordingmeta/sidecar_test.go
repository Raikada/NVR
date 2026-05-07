package recordingmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSidecar_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "2026-05-07_00-00-00.mp4")
	require.NoError(t, os.WriteFile(seg, []byte("ts"), 0o644))

	SetResolver(func(pathName string) (Sidecar, bool) {
		require.Equal(t, "cam1", pathName)
		return Sidecar{PolicyID: "pol-1", Mode: "motion"}, true
	})
	t.Cleanup(func() { SetResolver(nil) })

	require.NoError(t, Write("cam1", seg))

	sc, err := Read(seg)
	require.NoError(t, err)
	require.NotNil(t, sc)
	require.Equal(t, "pol-1", sc.PolicyID)
	require.Equal(t, "motion", sc.Mode)
}

func TestSidecar_WriteSkipsWhenResolverMissing(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "x.mp4")
	require.NoError(t, os.WriteFile(seg, []byte("ts"), 0o644))

	SetResolver(nil)
	require.NoError(t, Write("cam1", seg))

	_, err := os.Stat(SidecarPath(seg))
	require.True(t, os.IsNotExist(err))
}

func TestSidecar_WriteSkipsWhenResolverFalse(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "x.mp4")
	require.NoError(t, os.WriteFile(seg, []byte("ts"), 0o644))

	SetResolver(func(string) (Sidecar, bool) { return Sidecar{}, false })
	t.Cleanup(func() { SetResolver(nil) })

	require.NoError(t, Write("cam1", seg))
	_, err := os.Stat(SidecarPath(seg))
	require.True(t, os.IsNotExist(err))
}

func TestSidecar_ReadMissingIsNilNil(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "missing.mp4")
	sc, err := Read(seg)
	require.NoError(t, err)
	require.Nil(t, sc)
}

func TestSidecar_ReadCorruptIsNilNil(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "x.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(SidecarPath(seg)), 0o755))
	require.NoError(t, os.WriteFile(SidecarPath(seg), []byte("not-json"), 0o644))

	sc, err := Read(seg)
	require.NoError(t, err)
	require.Nil(t, sc, "corrupt sidecar should read as absent")
}

func TestSidecar_ReadBlankModeIsNilNil(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "x.mp4")
	body, err := json.Marshal(Sidecar{PolicyID: "pol-1", Mode: ""})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(SidecarPath(seg)), 0o755))
	require.NoError(t, os.WriteFile(SidecarPath(seg), body, 0o644))

	sc, err := Read(seg)
	require.NoError(t, err)
	require.Nil(t, sc)
}

func TestSidecar_Remove(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "x.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(SidecarPath(seg)), 0o755))
	require.NoError(t, os.WriteFile(SidecarPath(seg), []byte(`{"policy_id":"p","mode":"continuous"}`), 0o644))

	require.NoError(t, Remove(seg))
	_, err := os.Stat(SidecarPath(seg))
	require.True(t, os.IsNotExist(err))

	// Idempotent — remove a non-existent sidecar.
	require.NoError(t, Remove(seg))
}
