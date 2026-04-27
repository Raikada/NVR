package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

func invokePolicyHandler(
	api *API,
	handler func(*gin.Context),
	method string,
	rawQuery string,
	idParam string,
	body []byte,
) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()

	target := "/v1/recording-policies"
	if idParam != "" {
		target = "/v1/recording-policies/" + idParam
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	var bodyR io.Reader
	if body != nil {
		bodyR = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, bodyR)

	c, _ := gin.CreateTestContext(w)
	c.Request = req
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}

	handler(c)
	return w.Code, w.Body.Bytes()
}

// TestV1RecordingPoliciesListSynthesizesOnFirstAccess: when the
// in-memory map is empty, the first list call walks conf.Paths and
// synthesizes one policy per unique recording-config tuple.
func TestV1RecordingPoliciesListSynthesizesOnFirstAccess(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  cam_a:\n"+
		"    source: rtsp://192.0.2.1:554/stream\n"+
		"    record: yes\n"+
		"    recordSegmentDuration: 1h\n"+
		"  cam_b:\n"+
		"    source: rtsp://192.0.2.2:554/stream\n"+
		"    record: yes\n"+
		"    recordSegmentDuration: 1h\n"+
		"  cam_c:\n"+
		"    source: rtsp://192.0.2.3:554/stream\n"+
		"    record: yes\n"+
		"    recordSegmentDuration: 30m\n")

	api := &API{Conf: cnf}

	// Pre-list: in-memory map empty.
	require.Empty(t, api.Conf.RecordingPolicies)

	code, body := invokePolicyHandler(api, api.onV1RecordingPoliciesList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                    `json:"item_count"`
		PageCount int                    `json:"page_count"`
		Items     []defs.RecordingPolicy `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))

	// cam_a + cam_b share recording config → 1 policy; cam_c → 1 policy.
	require.Equal(t, 2, resp.ItemCount)
	for _, p := range resp.Items {
		require.NotEmpty(t, p.ID)
		_, err := uuid.Parse(p.ID)
		require.NoError(t, err, "policy id is a UUID")
		require.Equal(t, "00000000-0000-0000-0000-000000000000", p.TenantID)
	}

	// In-memory map populated after first access.
	require.Len(t, api.Conf.RecordingPolicies, 2)
}

func TestV1RecordingPoliciesListPagination(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  c1:\n"+
		"    source: rtsp://192.0.2.1:554/s\n"+
		"    record: yes\n"+
		"    recordSegmentDuration: 1h\n"+
		"  c2:\n"+
		"    source: rtsp://192.0.2.2:554/s\n"+
		"    record: yes\n"+
		"    recordSegmentDuration: 30m\n"+
		"  c3:\n"+
		"    source: rtsp://192.0.2.3:554/s\n"+
		"    record: yes\n"+
		"    recordSegmentDuration: 15m\n")
	api := &API{Conf: cnf}

	code, body := invokePolicyHandler(api, api.onV1RecordingPoliciesList, http.MethodGet, "items_per_page=2&page=0", "", nil)
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                    `json:"item_count"`
		PageCount int                    `json:"page_count"`
		Items     []defs.RecordingPolicy `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, 3, resp.ItemCount)
	require.Equal(t, 2, resp.PageCount)
	require.Len(t, resp.Items, 2)
}

func TestV1RecordingPoliciesGet(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  cam:\n"+
		"    source: rtsp://192.0.2.1:554/s\n"+
		"    record: yes\n")
	api := &API{Conf: cnf}

	// Trigger synthesis via the list handler.
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	var someID string
	for id := range api.Conf.RecordingPolicies {
		someID = id
		break
	}
	require.NotEmpty(t, someID)

	code, body := invokePolicyHandler(api, api.onV1RecordingPoliciesGet, http.MethodGet, "", someID, nil)
	require.Equal(t, http.StatusOK, code)

	var p defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(body, &p))
	require.Equal(t, someID, p.ID)
}

func TestV1RecordingPoliciesGetInvalidUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesGet, http.MethodGet, "", "not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestV1RecordingPoliciesGetNotFound(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	missing := uuid.New().String()
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesGet, http.MethodGet, "", missing, nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestV1RecordingPoliciesPostCreates(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	body, _ := json.Marshal(map[string]any{
		"name": "MyPolicy",
		"mode": "continuous",
	})
	code, respBody := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusCreated, code)

	var p defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(respBody, &p))
	require.NotEmpty(t, p.ID)
	_, err := uuid.Parse(p.ID)
	require.NoError(t, err)
	require.Equal(t, "MyPolicy", p.Name)
	require.Equal(t, "00000000-0000-0000-0000-000000000000", p.TenantID)
}

func TestV1RecordingPoliciesPostTenantMismatch(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	body, _ := json.Marshal(map[string]any{
		"name":      "Forbidden",
		"mode":      "continuous",
		"tenant_id": "11111111-1111-1111-1111-111111111111",
	})
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
	require.Equal(t, http.StatusForbidden, code)
}

func TestV1RecordingPoliciesPatch(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	// Create.
	body, _ := json.Marshal(map[string]any{"name": "Original", "mode": "continuous"})
	_, respBody := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
	var created defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(respBody, &created))

	// Patch.
	patch, _ := json.Marshal(map[string]any{"name": "Renamed"})
	code, patched := invokePolicyHandler(api, api.onV1RecordingPoliciesPatch, http.MethodPatch, "", created.ID, patch)
	require.Equal(t, http.StatusOK, code)

	var p defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(patched, &p))
	require.Equal(t, created.ID, p.ID, "id is route-pinned")
	require.Equal(t, "Renamed", p.Name)
}

// TestV1RecordingPoliciesPatchAppliesEnabledToConfPath: gap #12 closure.
// PATCHing a policy to flip enabled must propagate to every conf.Path
// whose RecordingPolicyID references this policy, so conf.Path.Record
// reflects the new state. Without the propagation, the canonical surface
// reports the new policy state but the recorder keeps recording (or
// fails to start).
func TestV1RecordingPoliciesPatchAppliesEnabledToConfPath(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  cam_a:\n"+
		"    source: rtsp://192.0.2.1:554/s\n"+
		"    record: yes\n"+
		"  cam_b:\n"+
		"    source: rtsp://192.0.2.2:554/s\n"+
		"    record: yes\n")
	api := &API{Conf: cnf}

	// Trigger synthesis: cam_a + cam_b share recording config so they
	// land on a single synthesized policy. Both paths get the same
	// RecordingPolicyID stamped.
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	require.True(t, api.Conf.Paths["cam_a"].Record, "pre-patch: cam_a recording")
	require.True(t, api.Conf.Paths["cam_b"].Record, "pre-patch: cam_b recording")

	policyID := api.Conf.Paths["cam_a"].RecordingPolicyID
	require.NotEmpty(t, policyID)
	require.Equal(t, policyID, api.Conf.Paths["cam_b"].RecordingPolicyID,
		"both cameras share the same synthesized policy")

	// Flip enabled to false.
	patch, _ := json.Marshal(map[string]any{"enabled": false})
	code, _ = invokePolicyHandler(api, api.onV1RecordingPoliciesPatch, http.MethodPatch, "", policyID, patch)
	require.Equal(t, http.StatusOK, code)

	// Both cameras must now have Record=false; the canonical policy
	// surface and the per-camera conf.Path stay in sync.
	require.False(t, api.Conf.Paths["cam_a"].Record,
		"post-patch: cam_a Record must reflect policy.Enabled=false")
	require.False(t, api.Conf.Paths["cam_b"].Record,
		"post-patch: cam_b Record must reflect policy.Enabled=false")

	// Flip back to true.
	patch, _ = json.Marshal(map[string]any{"enabled": true})
	code, _ = invokePolicyHandler(api, api.onV1RecordingPoliciesPatch, http.MethodPatch, "", policyID, patch)
	require.Equal(t, http.StatusOK, code)
	require.True(t, api.Conf.Paths["cam_a"].Record,
		"re-flipped: cam_a Record back to true")
	require.True(t, api.Conf.Paths["cam_b"].Record,
		"re-flipped: cam_b Record back to true")
}

func TestV1RecordingPoliciesDelete(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	body, _ := json.Marshal(map[string]any{"name": "ToDelete", "mode": "continuous"})
	_, respBody := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
	var created defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(respBody, &created))

	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesDelete, http.MethodDelete, "", created.ID, nil)
	require.Equal(t, http.StatusOK, code)

	// Subsequent GET → 404.
	code, _ = invokePolicyHandler(api, api.onV1RecordingPoliciesGet, http.MethodGet, "", created.ID, nil)
	require.Equal(t, http.StatusNotFound, code)
}

// TestV1RecordingPoliciesDeleteRejectedWhenReferenced: ADR 0009 §D5
// Recording-policies — deletion must reject if any Camera still
// references this policy.
func TestV1RecordingPoliciesDeleteRejectedWhenReferenced(t *testing.T) {
	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  cam:\n"+
		"    source: rtsp://192.0.2.1:554/s\n"+
		"    record: yes\n")
	api := &API{Conf: cnf}

	// Synthesize → cam gets a policy id stamped in conf.Path.RecordingPolicyID.
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	var policyID string
	for id := range api.Conf.RecordingPolicies {
		policyID = id
		break
	}
	require.NotEmpty(t, policyID)

	// Confirm the camera references it.
	require.Equal(t, policyID, api.Conf.Paths["cam"].RecordingPolicyID)

	// Attempt delete → 409.
	code, _ = invokePolicyHandler(api, api.onV1RecordingPoliciesDelete, http.MethodDelete, "", policyID, nil)
	require.Equal(t, http.StatusConflict, code)

	// Policy is still in the map.
	require.Contains(t, api.Conf.RecordingPolicies, policyID)
}

func TestV1RecordingPoliciesDeleteNotFound(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf}

	missing := uuid.New().String()
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesDelete, http.MethodDelete, "", missing, nil)
	require.Equal(t, http.StatusNotFound, code)
}
