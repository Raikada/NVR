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

	"github.com/bluenviron/mediamtx/internal/conf"
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

// TestV1RecordingPoliciesListReturnsSeededDefault: every Conf.Validate()
// seeds a deterministic "Default" RecordingPolicy under the well-known
// DefaultRecordingPolicyID UUID. The cameras handler defaults newly-
// created cameras to this policy. Listing on a fresh recorder returns
// exactly the seed.
func TestV1RecordingPoliciesListReturnsSeededDefault(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	// The seed is present immediately after Load+Validate, before any
	// canonical-surface access.
	require.Len(t, api.Conf.RecordingPolicies, 1)
	require.Contains(t, api.Conf.RecordingPolicies, conf.DefaultRecordingPolicyID)

	code, body := invokePolicyHandler(api, api.onV1RecordingPoliciesList, http.MethodGet, "", "", nil)
	require.Equal(t, http.StatusOK, code)

	var resp struct {
		ItemCount int                    `json:"item_count"`
		PageCount int                    `json:"page_count"`
		Items     []defs.RecordingPolicy `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))

	require.Equal(t, 1, resp.ItemCount)
	require.Equal(t, conf.DefaultRecordingPolicyID, resp.Items[0].ID)
	require.Equal(t, "Default", resp.Items[0].Name)
	require.Equal(t, defs.RecordingPolicyModeContinuous, resp.Items[0].Mode)
	require.True(t, resp.Items[0].Enabled)
	require.Equal(t, "", resp.Items[0].TenantID)
}

func TestV1RecordingPoliciesListPagination(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	// Seed Default + two operator-created policies → three items total.
	for _, name := range []string{"P1", "P2"} {
		body, _ := json.Marshal(map[string]any{"name": name, "mode": "continuous"})
		code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
		require.Equal(t, http.StatusCreated, code)
	}

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
	api := &API{Conf: cnf, Parent: &testParent{}}

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
	api := &API{Conf: cnf, Parent: &testParent{}}

	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesGet, http.MethodGet, "", "not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestV1RecordingPoliciesGetNotFound(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	missing := uuid.New().String()
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesGet, http.MethodGet, "", missing, nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestV1RecordingPoliciesPostCreates(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

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
	require.Equal(t, "", p.TenantID)
}

func TestV1RecordingPoliciesPostTenantMismatch(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

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
	api := &API{Conf: cnf, Parent: &testParent{}}

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
//
// With the seeded Default in place, two POSTed cameras attach to it by
// default and share its policy id; flipping the Default's enabled
// flips both cameras' Record.
func TestV1RecordingPoliciesPatchAppliesEnabledToConfPath(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	// POST two cameras; both attach to the seeded Default policy.
	for _, name := range []string{"cam_a", "cam_b"} {
		body, _ := json.Marshal(map[string]any{
			"name":        name,
			"source_type": "rtsp",
			"source_url":  "rtsp://192.0.2.1:554/" + name,
		})
		code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", body)
		require.Equal(t, http.StatusCreated, code)
	}

	require.True(t, api.Conf.Paths["cam_a"].Record, "pre-patch: cam_a recording (Default mode=continuous)")
	require.True(t, api.Conf.Paths["cam_b"].Record, "pre-patch: cam_b recording (Default mode=continuous)")
	require.Equal(t, conf.DefaultRecordingPolicyID, api.Conf.Paths["cam_a"].RecordingPolicyID)
	require.Equal(t, conf.DefaultRecordingPolicyID, api.Conf.Paths["cam_b"].RecordingPolicyID)

	// Flip Default.enabled to false.
	patch, _ := json.Marshal(map[string]any{"enabled": false})
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesPatch, http.MethodPatch, "",
		conf.DefaultRecordingPolicyID, patch)
	require.Equal(t, http.StatusOK, code)

	require.False(t, api.Conf.Paths["cam_a"].Record,
		"post-patch: cam_a Record must reflect Default.Enabled=false")
	require.False(t, api.Conf.Paths["cam_b"].Record,
		"post-patch: cam_b Record must reflect Default.Enabled=false")

	// Flip back to true.
	patch, _ = json.Marshal(map[string]any{"enabled": true})
	code, _ = invokePolicyHandler(api, api.onV1RecordingPoliciesPatch, http.MethodPatch, "",
		conf.DefaultRecordingPolicyID, patch)
	require.Equal(t, http.StatusOK, code)
	require.True(t, api.Conf.Paths["cam_a"].Record, "re-flipped: cam_a Record back to true")
	require.True(t, api.Conf.Paths["cam_b"].Record, "re-flipped: cam_b Record back to true")
}

func TestV1RecordingPoliciesDelete(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

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
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	// Create a custom policy and POST a camera attached to it (we don't
	// use Default here because the seeded Default isn't deletable from
	// the recorder anyway in practice, and using a created policy keeps
	// the test focused on the deletion-conflict semantic).
	body, _ := json.Marshal(map[string]any{"name": "InUse", "mode": "continuous"})
	_, respBody := invokePolicyHandler(api, api.onV1RecordingPoliciesPost, http.MethodPost, "", "", body)
	var created defs.RecordingPolicy
	require.NoError(t, json.Unmarshal(respBody, &created))

	camBody, _ := json.Marshal(map[string]any{
		"name":                "cam",
		"source_type":         "rtsp",
		"source_url":          "rtsp://192.0.2.1:554/s",
		"recording_policy_id": created.ID,
	})
	code, _ := invokeCameraHandler(api, api.onV1CamerasPost, http.MethodPost, "", "", camBody)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, created.ID, api.Conf.Paths["cam"].RecordingPolicyID)

	// Attempt delete → 409.
	code, _ = invokePolicyHandler(api, api.onV1RecordingPoliciesDelete, http.MethodDelete, "", created.ID, nil)
	require.Equal(t, http.StatusConflict, code)

	// Policy is still in the map.
	require.Contains(t, api.Conf.RecordingPolicies, created.ID)
}

func TestV1RecordingPoliciesDeleteNotFound(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	missing := uuid.New().String()
	code, _ := invokePolicyHandler(api, api.onV1RecordingPoliciesDelete, http.MethodDelete, "", missing, nil)
	require.Equal(t, http.StatusNotFound, code)
}
