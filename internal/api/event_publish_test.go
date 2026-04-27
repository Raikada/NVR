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
