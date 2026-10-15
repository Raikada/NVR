// Package api: Phase 5 Task 5.4 tests for the per-policy schedule
// editor. Verifies HH:MM ↔ minutes round-trip and atomic replace-all.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/store"
)

func TestRecordingSchedules_RoundTripHHMM(t *testing.T) {
	_, hc, st := startCameraAPI(t)

	// Seed a recording policy.
	policyID := "policy_test"
	require.NoError(t, st.RecordingPolicies.Insert(context.Background(), &store.RecordingPolicy{
		ID: policyID, Name: "test", Mode: "scheduled",
		RetentionDurationSeconds: 86400,
		Container: "fmp4",
		MinSegmentDurationSeconds: 60, MaxSegmentDurationSeconds: 600,
		PartDurationMS: 1000, MaxPartSizeBytes: 1024 * 1024,
		Enabled: true,
	}))

	body := `{"schedules":[{"day_of_week":1,"start":"09:00","end":"17:30"}]}`
	req, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/recording-policies/"+policyID+"/schedules",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var got recordingSchedulesResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.Len(t, got.Schedules, 1)
	require.Equal(t, 1, got.Schedules[0].DayOfWeek)
	require.Equal(t, "09:00", got.Schedules[0].Start)
	require.Equal(t, "17:30", got.Schedules[0].End)

	// Replace with different windows; the previous window must be gone.
	body = `{"schedules":[{"day_of_week":2,"start":"00:00","end":"23:59"}]}`
	req, _ = http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/recording-policies/"+policyID+"/schedules",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.Len(t, got.Schedules, 1)
	require.Equal(t, 2, got.Schedules[0].DayOfWeek)
}

func TestRecordingSchedules_InvalidHHMM(t *testing.T) {
	_, hc, st := startCameraAPI(t)
	policyID := "policy_bad"
	require.NoError(t, st.RecordingPolicies.Insert(context.Background(), &store.RecordingPolicy{
		ID: policyID, Name: "bad", Mode: "scheduled",
		Container: "fmp4", Enabled: true,
	}))

	body := `{"schedules":[{"day_of_week":1,"start":"25:00","end":"24:00"}]}`
	req, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/recording-policies/"+policyID+"/schedules",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}
