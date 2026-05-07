package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
)

// TestCameraPostPersistsAcrossReload exercises the API → SaveToFile →
// fresh Conf.Load round trip the user's gate scenario depends on:
// adding a camera through /v1/cameras and restarting the recorder must
// surface the same camera with its RecordingPolicy linkage intact.
//
// Stops short of running a real Core (which needs ports); instead it
// invokes the handler, persists the resulting Conf via SaveToFile, and
// re-loads through conf.Load — the same call path the recorder boots
// through.
func TestCameraPostPersistsAcrossReload(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(confPath, []byte(
		"api: yes\n"), 0o644))

	cnf, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)
	api := &API{Conf: cnf, Parent: &testParent{}}

	// POST a camera; the seeded Default policy should be applied
	// because the body omits recording_policy_id.
	body, err := json.Marshal(map[string]any{
		"name":        "cam_persist",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	require.NoError(t, err)

	code, respBody := invokeCameraHandler(api, api.onV1CamerasPost,
		http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code, "POST returned %d: %s", code, respBody)

	var cam defs.Camera
	require.NoError(t, json.Unmarshal(respBody, &cam))
	require.NotNil(t, cam.RecordingPolicyID)
	require.Equal(t, conf.DefaultRecordingPolicyID, *cam.RecordingPolicyID)

	// Persist the post-POST conf to disk via SaveToFile.
	_, err = api.Conf.SaveToFile(confPath)
	require.NoError(t, err)

	// Reload from disk as if the recorder had restarted.
	reloaded, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)

	storedPath, ok := reloaded.Paths["cam_persist"]
	require.True(t, ok, "camera missing after reload")
	require.Equal(t, conf.DefaultRecordingPolicyID, storedPath.RecordingPolicyID,
		"RecordingPolicyID lost across reload")
	require.Equal(t, "rtsp://192.0.2.5:554/stream", storedPath.Source)
}

// TestCameraPatchRecordingPolicyIDPersistsAcrossReload covers the case
// where a PATCH changes the policy linkage and the new value must
// survive a restart.
func TestCameraPatchRecordingPolicyIDPersistsAcrossReload(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(confPath, []byte(
		"api: yes\n"), 0o644))

	cnf, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)
	api := &API{Conf: cnf, Parent: &testParent{}}

	// Create a target policy via the policy POST handler.
	polBody, _ := json.Marshal(map[string]any{"name": "Target", "mode": "continuous"})
	_, polResp := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", polBody)
	var target defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(polResp, &target))
	require.NotEmpty(t, target.ID)

	postBody, _ := json.Marshal(map[string]any{
		"name":        "cam_switcher",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5/s",
	})
	code, respBody := invokeCameraHandler(api, api.onV1CamerasPost,
		http.MethodPost, "", "", postBody)
	require.Equal(t, http.StatusCreated, code, "POST: %s", respBody)
	var created defs.Camera
	require.NoError(t, json.Unmarshal(respBody, &created))

	// PATCH the linkage to the custom policy.
	patchBody, _ := json.Marshal(map[string]any{
		"recording_policy_id": target.ID,
	})
	code, respBody = invokeCameraHandler(api, api.onV1CamerasPatch,
		http.MethodPatch, "", created.ID, patchBody)
	require.Equal(t, http.StatusOK, code, "PATCH: %s", respBody)

	// Persist + reload.
	_, err = api.Conf.SaveToFile(confPath)
	require.NoError(t, err)

	reloaded, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)

	storedPath, ok := reloaded.Paths["cam_switcher"]
	require.True(t, ok)
	require.Equal(t, target.ID, storedPath.RecordingPolicyID)

	_, ok = reloaded.RecordingPolicies[target.ID]
	require.True(t, ok, "target policy lost on reload")
}

// TestRecordingPolicyPostPersistsAcrossReload: a POSTed policy survives
// a restart and remains loadable.
func TestRecordingPolicyPostPersistsAcrossReload(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(confPath, []byte(
		""), 0o644))

	cnf, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, _ := json.Marshal(map[string]any{
		"name": "Custom",
		"mode": "continuous",
	})
	code, respBody := invokePolicyHandler(api, api.onV1RecordingPoliciesPost,
		http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code, "POST policy: %s", respBody)

	var created defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(respBody, &created))
	require.NotEmpty(t, created.ID)

	_, err = api.Conf.SaveToFile(confPath)
	require.NoError(t, err)

	reloaded, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)
	pol, ok := reloaded.RecordingPolicies[created.ID]
	require.True(t, ok, "created policy lost on reload")
	require.Equal(t, "Custom", pol.Name)
}

// TestRecordingPolicyDeletePersistsAcrossReload: deleting a policy
// removes it from disk too.
func TestRecordingPolicyDeletePersistsAcrossReload(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "mediamtx.yml")
	require.NoError(t, os.WriteFile(confPath, []byte(
		""), 0o644))

	cnf, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)
	api := &API{Conf: cnf, Parent: &testParent{}}

	// Create a custom policy via the API so the POST path runs end-to-end.
	body, _ := json.Marshal(map[string]any{"name": "Trash", "mode": "continuous"})
	_, polResp := invokePolicyHandler(api, api.onV1RecordingPoliciesPost,
		http.MethodPost, "", "", body)
	var trash defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(polResp, &trash))
	require.NotEmpty(t, trash.ID)

	code, respBody := invokePolicyHandler(api, api.onV1RecordingPoliciesDelete,
		http.MethodDelete, "", trash.ID, nil)
	require.Equal(t, http.StatusOK, code, "DELETE: %s", respBody)

	_, err = api.Conf.SaveToFile(confPath)
	require.NoError(t, err)

	reloaded, _, err := conf.Load(confPath, nil, nil)
	require.NoError(t, err)
	_, present := reloaded.RecordingPolicies[trash.ID]
	require.False(t, present, "deleted policy resurfaced after reload")

	// Default still present (Validate seeds it).
	_, defPresent := reloaded.RecordingPolicies[conf.DefaultRecordingPolicyID]
	require.True(t, defPresent)
}
