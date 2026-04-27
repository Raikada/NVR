package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// findEventByKind returns the first event in the store whose Kind
// matches; t.Fatalf if none. Mirrors the small helpers above
// resetEventStoreSingleton in api_v1_events_test.go.
func findEventByKind(t *testing.T, kind string) (string, string, string) {
	t.Helper()
	for _, e := range defaultEventStore().Snapshot() {
		if string(e.Kind) == kind {
			return e.ID, e.SubjectID, string(e.Severity)
		}
	}
	t.Fatalf("no event with kind=%q in store; have %d events", kind, defaultEventStore().Len())
	return "", "", ""
}

// TestPublishEvent_CameraPOSTEmitsConfigApplied verifies that the
// /v1/cameras POST handler publishes a config.applied event into the
// default EventStore on success. The event's subject_id should be the
// canonical Camera UUID derived from the path-name; severity info.
func TestPublishEvent_CameraPOSTEmitsConfigApplied(t *testing.T) {
	resetEventStoreSingleton(t)
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"name":        "evtcam",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	require.NoError(t, err)

	code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code)

	_, subjectID, sev := findEventByKind(t, "config.applied")
	require.Equal(t, cameraIDFromPathName("evtcam"), subjectID)
	require.Equal(t, "info", sev)
}

// TestPublishCameraOnline_ResolvesAgainstPipelineTarget exercises the
// pipeline-side helper. Sets a pipeline target, publishes, and asserts
// the event landed in the configured store with the expected camera-id
// derivation and tenant id from the resolver.
func TestPublishCameraOnline_ResolvesAgainstPipelineTarget(t *testing.T) {
	resetEventStoreSingleton(t)

	store := NewEventStore(0)
	const tenantID = "11111111-2222-3333-4444-555555555555"
	SetPipelineEventTarget(store, func() string { return tenantID })
	t.Cleanup(func() { SetPipelineEventTarget(nil, nil) })

	PublishCameraOnline("evt_pipeline_cam")
	require.Equal(t, 1, store.Len())

	snap := store.Snapshot()
	got := snap[0]
	require.Equal(t, "camera.online", string(got.Kind))
	require.Equal(t, "info", string(got.Severity))
	require.Equal(t, cameraIDFromPathName("evt_pipeline_cam"), got.SubjectID)
	require.Equal(t, tenantID, got.TenantID)
	require.Equal(t, "evt_pipeline_cam", got.Attributes["path_name"])
}

// TestPublishCameraOffline_FallsBackToDefaultStore confirms the helper
// falls back to defaultEventStore when no pipeline target is set, and
// emits a warning-severity offline event.
func TestPublishCameraOffline_FallsBackToDefaultStore(t *testing.T) {
	resetEventStoreSingleton(t)
	SetPipelineEventTarget(nil, nil)
	t.Cleanup(func() { SetPipelineEventTarget(nil, nil) })

	PublishCameraOffline("evt_offline_cam")

	_, subjectID, sev := findEventByKind(t, "camera.offline")
	require.Equal(t, cameraIDFromPathName("evt_offline_cam"), subjectID)
	require.Equal(t, "warning", sev)
}

// TestPublishEvent_RecordingPolicyPOSTEmitsPolicyApplied verifies the
// matching policy.applied emission on /v1/recording-policies POST.
func TestPublishEvent_RecordingPolicyPOSTEmitsPolicyApplied(t *testing.T) {
	resetEventStoreSingleton(t)
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	body, err := json.Marshal(map[string]any{
		"name": "evtpolicy",
		"mode": "continuous",
	})
	require.NoError(t, err)

	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code)

	_, _, sev := findEventByKind(t, "policy.applied")
	require.Equal(t, "info", sev)
}
