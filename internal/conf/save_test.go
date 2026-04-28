package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSaveToFileRoundTrips validates that SaveToFile produces YAML
// the matching Load can re-ingest, and the resulting Conf matches
// (modulo derive-on-load fields that are intentionally not persisted).
func TestSaveToFileRoundTrips(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")

	// Seed an initial conf with a camera that references the Default
	// recording policy. RecordingPolicyID must round-trip to disk per
	// the persistence work; ID is derive-on-load (not persisted).
	require.NoError(t, os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"+
			"paths:\n"+
			"  cam1:\n"+
			"    source: rtsp://example.com:554/stream\n"+
			"    sourceOnDemand: true\n"+
			"    recordingPolicyId: "+DefaultRecordingPolicyID+"\n"),
		0o644))

	original, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, original.Paths["cam1"])
	require.Equal(t, DefaultRecordingPolicyID, original.Paths["cam1"].RecordingPolicyID)

	// Save back to a fresh file and re-load. The persisted YAML must
	// preserve RecordingPolicyID; reloading must surface the linkage.
	savedPath := filepath.Join(tmpDir, "mediamtx-saved.yml")
	// touch first so SaveToFile takes the same-fs CreateTemp branch
	require.NoError(t, os.WriteFile(savedPath, []byte(""), 0o644))
	yamlBytes, err := original.SaveToFile(savedPath)
	require.NoError(t, err)
	require.Contains(t, string(yamlBytes), "recordingPolicyId")
	require.Contains(t, string(yamlBytes), DefaultRecordingPolicyID)

	reloaded, _, err := Load(savedPath, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, reloaded.Paths["cam1"])
	require.Equal(t, DefaultRecordingPolicyID, reloaded.Paths["cam1"].RecordingPolicyID)
	require.Equal(t, original.TenantID, reloaded.TenantID)
	require.Equal(t, original.Paths["cam1"].Source, reloaded.Paths["cam1"].Source)
}

// TestSaveToFilePreservesPermissions confirms the existing destination
// mode is preserved across an atomic rewrite; new files get 0o644.
func TestSaveToFilePreservesPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "existing.yml")
	require.NoError(t, os.WriteFile(existing, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"), 0o600))

	cnf, _, err := Load(existing, nil, nil)
	require.NoError(t, err)

	_, err = cnf.SaveToFile(existing)
	require.NoError(t, err)

	st, err := os.Stat(existing)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	// Fresh file gets 0o644.
	fresh := filepath.Join(tmpDir, "fresh.yml")
	_, err = cnf.SaveToFile(fresh)
	require.NoError(t, err)

	st, err = os.Stat(fresh)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), st.Mode().Perm())
}

// TestSaveToFileAtomicityNoLeftoverTemps confirms SaveToFile leaves
// no .tmp files behind after a successful save.
func TestSaveToFileAtomicityNoLeftoverTemps(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(path, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"), 0o644))

	cnf, _, err := Load(path, nil, nil)
	require.NoError(t, err)
	_, err = cnf.SaveToFile(path)
	require.NoError(t, err)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.Contains(e.Name(), ".tmp"),
			"unexpected leftover temp file: %s", e.Name())
	}
}

// TestSaveToFileFailureLeavesOriginalIntact confirms that a save
// targeted at an unwritable directory does not damage the destination
// file the original was loaded from.
func TestSaveToFileFailureLeavesOriginalIntact(t *testing.T) {
	tmpDir := t.TempDir()
	source := filepath.Join(tmpDir, "src.yml")
	originalContent := []byte("tenantId: 00000000-0000-0000-0000-000000000000\n" +
		"paths:\n" +
		"  cam1:\n" +
		"    source: rtsp://example.com/s\n" +
		"    sourceOnDemand: true\n")
	require.NoError(t, os.WriteFile(source, originalContent, 0o644))

	cnf, _, err := Load(source, nil, nil)
	require.NoError(t, err)

	// Try to save to a path inside a non-existent directory; CreateTemp
	// should fail, leaving the source file untouched.
	_, err = cnf.SaveToFile(filepath.Join(tmpDir, "no-such-dir", "out.yml"))
	require.Error(t, err)

	got, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, originalContent, got)
}

// TestSaveLoadCameraAddDeleteRoundTrip exercises the full add/delete
// path through Conf-level helpers (no API, no core). This is the
// closest pure-conf-package proxy for "POST camera → restart →
// camera still present" the user's gate scenario verifies end-to-end.
func TestSaveLoadCameraAddDeleteRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"), 0o644))

	cnf, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)

	// Hand-craft an OptionalPath the API handler would produce.
	op := &OptionalPath{Values: newOptionalPathValues()}
	require.NoError(t, op.UnmarshalJSON([]byte(`{
		"source": "rtsp://example.com:554/stream",
		"sourceOnDemand": true,
		"recordingPolicyId": "`+DefaultRecordingPolicyID+`"
	}`)))
	require.NoError(t, cnf.AddPath("cam1", op))
	require.NoError(t, cnf.Validate(nil))
	_, err = cnf.SaveToFile(confPath)
	require.NoError(t, err)

	// Reload from disk: camera and policy linkage must survive.
	reloaded, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, reloaded.Paths["cam1"])
	require.Equal(t, DefaultRecordingPolicyID, reloaded.Paths["cam1"].RecordingPolicyID)

	// Now delete and re-save; reload should drop the camera.
	require.NoError(t, reloaded.RemovePath("cam1"))
	require.NoError(t, reloaded.Validate(nil))
	_, err = reloaded.SaveToFile(confPath)
	require.NoError(t, err)

	final, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)
	_, present := final.Paths["cam1"]
	require.False(t, present)
}

// TestSaveLoadRecordingPolicyRoundTrip confirms a RecordingPolicy
// added through the conf-level map persists across a save/load round.
func TestSaveLoadRecordingPolicyRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"), 0o644))

	cnf, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)

	const customID = "11111111-2222-3333-4444-555555555555"
	if cnf.RecordingPolicies == nil {
		cnf.RecordingPolicies = make(map[string]*RecordingPolicyConfig)
	}
	cnf.RecordingPolicies[customID] = &RecordingPolicyConfig{
		Name:               "MyPolicy",
		Mode:               "continuous",
		Container:          "fmp4",
		Enabled:            true,
		RetentionDuration:  Duration(30 * 24 * 3_600_000_000_000), // 30d in ns
		MinSegmentDuration: Duration(60_000_000_000),
		MaxSegmentDuration: Duration(600_000_000_000),
		PartDuration:       Duration(1_000_000_000),
		MaxPartSize:        50 * 1024 * 1024,
	}
	require.NoError(t, cnf.Validate(nil))
	_, err = cnf.SaveToFile(confPath)
	require.NoError(t, err)

	reloaded, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)
	pol, ok := reloaded.RecordingPolicies[customID]
	require.True(t, ok, "custom policy lost on reload")
	require.Equal(t, "MyPolicy", pol.Name)
	require.Equal(t, "continuous", pol.Mode)
	require.True(t, pol.Enabled)

	// Default seeded policy is also present and uses its deterministic ID.
	def, ok := reloaded.RecordingPolicies[DefaultRecordingPolicyID]
	require.True(t, ok, "Default policy missing from reload")
	require.Equal(t, "Default", def.Name)
}

// TestBootFromEmptyYAMLSeedsDefaultPolicy is the Phase 2e Run-2-surprise
// regression: starting from an empty / minimal mediamtx.yml must surface
// the seeded Default RecordingPolicy with its deterministic UUID.
func TestBootFromEmptyYAMLSeedsDefaultPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	// Minimal: just tenant id, no paths, no policies.
	require.NoError(t, os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"), 0o644))

	cnf, _, err := Load(confPath, nil, nil)
	require.NoError(t, err)

	def, ok := cnf.RecordingPolicies[DefaultRecordingPolicyID]
	require.True(t, ok,
		"Default RecordingPolicy with deterministic ID %s missing on empty-conf boot",
		DefaultRecordingPolicyID)
	require.Equal(t, "Default", def.Name)
	require.Equal(t, "continuous", def.Mode)
	require.True(t, def.Enabled)
}
