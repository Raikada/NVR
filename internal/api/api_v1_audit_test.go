package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

func newV1AuditServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/audit", a.onV1AuditList)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func resetAuditBufferSingleton(t *testing.T) {
	t.Helper()
	defaultAuditBuffer().Clear()
	defaultAuditChain().resetForTests()
	t.Cleanup(func() {
		defaultAuditBuffer().Clear()
		defaultAuditChain().resetForTests()
	})
}

// TestV1AuditListReturnsChainedEntries verifies the GET /v1/audit
// endpoint round-trips entries the chain has appended, in newest-
// first order.
func TestV1AuditListReturnsChainedEntries(t *testing.T) {
	resetAuditBufferSingleton(t)
	cnf := tempConf(t, "api: yes\n")
	a := &API{Conf: cnf}
	srv := newV1AuditServer(t, a)

	a.emitAuthDecision(
		defs.AuditOutcomeFailure,
		defs.AuditActorKindUnauthenticated,
		"", "192.0.2.5", "", nil,
	)
	a.emitAuthDecision(
		defs.AuditOutcomeSuccess,
		defs.AuditActorKindServiceAccount,
		"", "192.0.2.6", "", nil,
	)

	resp, err := http.Get(srv.URL + "/v1/audit")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got auditListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 2, got.ItemCount)
	require.Len(t, got.Items, 2)

	// Chain integrity: walk the buffer (oldest-first) and verify
	// links. The endpoint sorts newest-first, but the underlying
	// buffer holds them in append order.
	snap := defaultAuditBuffer().Snapshot()
	require.Equal(t, defs.ZeroPrevHash, snap[0].PrevHash)
	require.Equal(t, snap[0].EntryHash, snap[1].PrevHash)
}

// TestV1AuditListFilters verifies query-string filters narrow results.
func TestV1AuditListFilters(t *testing.T) {
	resetAuditBufferSingleton(t)
	cnf := tempConf(t, "api: yes\n")
	a := &API{Conf: cnf}
	srv := newV1AuditServer(t, a)

	a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   "cam-1",
	})
	a.emitAuditLocked(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindUnauthenticated,
		Action:       "auth.failed_login",
		Outcome:      defs.AuditOutcomeFailure,
		ResourceKind: "session",
	})

	// Filter by outcome.
	resp, err := http.Get(srv.URL + "/v1/audit?outcome=failure")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got auditListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, defs.AuditOutcomeFailure, got.Items[0].Outcome)

	// Filter by kind (action).
	resp2, err := http.Get(srv.URL + "/v1/audit?kind=config.applied")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var got2 auditListResponse
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got2))
	require.Equal(t, 1, got2.ItemCount)
	require.Equal(t, "config.applied", got2.Items[0].Action)

	// Filter by resource_kind.
	resp3, err := http.Get(srv.URL + "/v1/audit?resource_kind=camera")
	require.NoError(t, err)
	defer resp3.Body.Close()
	var got3 auditListResponse
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&got3))
	require.Equal(t, 1, got3.ItemCount)
	require.Equal(t, "cam-1", got3.Items[0].ResourceID)

	// Invalid actor_kind returns 400.
	resp4, err := http.Get(srv.URL + "/v1/audit?actor_kind=bogus")
	require.NoError(t, err)
	defer resp4.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp4.StatusCode)
}

// TestCamerasPOST_EmitsAuditEntry verifies the camera POST handler
// writes a config.applied audit entry alongside the existing Event.
func TestCamerasPOST_EmitsAuditEntry(t *testing.T) {
	resetEventStoreSingleton(t)
	resetAuditBufferSingleton(t)
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"name":        "auditcam",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	require.NoError(t, err)

	code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code)

	snap := defaultAuditBuffer().Snapshot()
	require.Len(t, snap, 1)
	require.Equal(t, "config.applied", snap[0].Action)
	require.Equal(t, defs.AuditOutcomeSuccess, snap[0].Outcome)
	require.Equal(t, "camera", snap[0].ResourceKind)
	require.NotEmpty(t, snap[0].ResourceID)
	require.Equal(t, "create", snap[0].Attributes["verb"])
	require.Equal(t, defs.ZeroPrevHash, snap[0].PrevHash)
	require.NotEmpty(t, snap[0].EntryHash)
}

// TestCamerasPOST_DegradedModeReturns503 verifies the gating per
// ADR 0006 D6: when the audit buffer is at the high-water threshold,
// admin actions return 503 with the structured error message.
func TestCamerasPOST_DegradedModeReturns503(t *testing.T) {
	resetAuditBufferSingleton(t)

	// Fill the default buffer to the 80% threshold. The default
	// capacity is 4096; 80% is 3277 (we just over-fill to be sure).
	buf := defaultAuditBuffer()
	for i := 0; i < buf.Capacity()*4/5+1; i++ {
		buf.Append(defs.AuditLogEntry{ID: "x"})
	}
	require.True(t, buf.IsDegraded())

	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"name":        "shouldfail",
		"source_type": "rtsp",
		"source_url":  "rtsp://192.0.2.5:554/stream",
	})
	require.NoError(t, err)

	code, respBody := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusServiceUnavailable, code)

	var apiErr defs.APIError
	require.NoError(t, json.Unmarshal(respBody, &apiErr))
	require.Equal(t, defs.APIErrorStatusError, apiErr.Status)
	require.Contains(t, apiErr.Error, "audit log near capacity")
	require.Contains(t, apiErr.Error, "ADR 0006 D6")
}

// TestRecordingPolicyPOST_EmitsAuditEntry verifies the policy
// handler also wires the audit emit.
func TestRecordingPolicyPOST_EmitsAuditEntry(t *testing.T) {
	resetEventStoreSingleton(t)
	resetAuditBufferSingleton(t)

	cnf := tempConf(t, "api: yes\n")
	a := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{
		"name": "p1",
		"mode": "continuous",
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/recording-policies", bytes.NewReader(body))
	a.onV1RecordingPoliciesPost(c)
	require.Equal(t, http.StatusCreated, w.Code)

	snap := defaultAuditBuffer().Snapshot()
	require.Len(t, snap, 1)
	require.Equal(t, "config.applied", snap[0].Action)
	require.Equal(t, "recording_policy", snap[0].ResourceKind)
	require.Equal(t, "create", snap[0].Attributes["verb"])
}

