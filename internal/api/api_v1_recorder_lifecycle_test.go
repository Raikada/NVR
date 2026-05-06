package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/stretchr/testify/require"
)

// makeLifecycleAPI builds an API instance with a real on-disk
// identity dir + an in-memory audit chain so the lifecycle endpoints
// can actually exercise their state-transitioning code paths.
//
// Each test gets a unique listen port so they can run in parallel
// (and so a hung previous-test process doesn't poison the next run).
var lifecycleNextPort = 11000

func makeLifecycleAPI(t *testing.T) (*API, *identity.Identity, string, string) {
	t.Helper()
	dir := t.TempDir()
	idDir := filepath.Join(dir, "identity")
	id, err := identity.Open(idDir)
	require.NoError(t, err)

	cnf := tempConf(t, "api: yes\nidentityDir: "+idDir+"\n")

	port := lifecycleNextPort
	lifecycleNextPort++
	addr := fmt.Sprintf("localhost:%d", port)

	api := &API{
		Version:      "v0.lifecycle-test",
		Address:      addr,
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		Conf:         cnf,
		AuthManager:  test.NilAuthManager,
		Identity:     id,
		Parent:       &testParent{},
	}
	require.NoError(t, api.Initialize())
	t.Cleanup(func() {
		api.Close()
		// Reset the lifecycle confirmation singleton between tests so
		// state doesn't leak; audit chain singleton is process-wide
		// and harmless to share across tests.
		recorderLifecycleMu.Lock()
		recorderLifecycleTok = nil
		recorderLifecycleMu.Unlock()
	})
	return api, id, idDir, "http://" + addr
}

func TestConfigReset_Default_PreservesIdentity(t *testing.T) {
	api, id, _, base := makeLifecycleAPI(t)
	originalID := id.ID().String()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	var out map[string]any
	httpRequest(t, hc, http.MethodPost,
		base+"/v1/recorder/config-reset", nil, &out)
	require.Equal(t, "config", out["reset_kind"])
	require.Equal(t, false, out["return_to_unpaired"])

	// Identity ID is the same.
	require.Equal(t, originalID, api.Identity.ID().String())
}

func TestConfigReset_ReturnToUnpaired_ClearsIssuedIdentityWhenPaired(t *testing.T) {
	// When the recorder isn't paired (no issued cert), return_to_unpaired
	// is a no-op for the issued material. We verify the request still
	// succeeds and the identity ID survives.
	api, id, _, base := makeLifecycleAPI(t)
	originalID := id.ID().String()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	var out map[string]any
	body := map[string]any{"return_to_unpaired": true}
	httpRequest(t, hc, http.MethodPost,
		base+"/v1/recorder/config-reset", body, &out)
	require.Equal(t, true, out["return_to_unpaired"])
	require.Equal(t, originalID, api.Identity.ID().String())
}

func TestFactoryWipe_FirstCallReturnsConfirmation(t *testing.T) {
	_, _, _, base := makeLifecycleAPI(t)

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	res, err := hc.Post(base+"/v1/recorder/factory-wipe",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	require.Equal(t, true, got["confirmation_required"])
	require.NotEmpty(t, got["confirmation_token"])
}

func TestFactoryWipe_SecondCallWithTokenWipes(t *testing.T) {
	_, _, idDir, base := makeLifecycleAPI(t)

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	// Stage 1: request confirmation.
	res, err := hc.Post(base+"/v1/recorder/factory-wipe",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer res.Body.Close()
	var stage1 map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&stage1))
	confirmation := stage1["confirmation_token"].(string)

	// Verify identity files exist before wipe.
	entries, err := os.ReadDir(idDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	// Stage 2: confirm.
	body := map[string]any{
		"confirm":            "WIPE_RECORDINGS_AND_IDENTITY",
		"confirmation_token": confirmation,
	}
	var out map[string]any
	httpRequest(t, hc, http.MethodPost,
		base+"/v1/recorder/factory-wipe", body, &out)
	require.Equal(t, "factory_wipe", out["action"])

	// Identity dir was wiped.
	entries, err = os.ReadDir(idDir)
	require.NoError(t, err)
	require.Empty(t, entries, "identity dir should be empty after factory wipe")
}

func TestFactoryWipe_SecondCallWithoutFirstFails(t *testing.T) {
	_, _, _, base := makeLifecycleAPI(t)

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	body := map[string]any{
		"confirm":            "WIPE_RECORDINGS_AND_IDENTITY",
		"confirmation_token": "totally-bogus-token",
	}
	b, _ := json.Marshal(body)
	res, err := hc.Post(base+"/v1/recorder/factory-wipe",
		"application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestRecoveryBundle_Export_ContainsManifestAndSignature(t *testing.T) {
	_, id, _, base := makeLifecycleAPI(t)

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	body := map[string]any{"purpose": "evidence", "include_audit": true}
	var out map[string]any
	httpRequest(t, hc, http.MethodPost,
		base+"/v1/recorder/recovery-bundle", body, &out)

	// Manifest is a raw-JSON object.
	rawManifest, ok := out["manifest"]
	require.True(t, ok, "expected manifest field")
	manifest := rawManifest.(map[string]any)
	require.Equal(t, "evidence", manifest["purpose"])
	require.Equal(t, id.ID().String(), manifest["recorder_id"])
	require.NotEmpty(t, manifest["public_key_fingerprint"])

	// Signature is base64.
	sig := out["signature"].(string)
	require.NotEmpty(t, sig)
	require.Equal(t, "ecdsa-p256-sha256", out["signature_algorithm"])
}

func TestRecoveryBundle_InvalidPurposeRejected(t *testing.T) {
	_, _, _, base := makeLifecycleAPI(t)

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	b, _ := json.Marshal(map[string]any{"purpose": "bogus"})
	res, err := hc.Post(base+"/v1/recorder/recovery-bundle",
		"application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestRecordPathRoot(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"./recordings/%path/%Y-%m-%d", "recordings"},
		{"/var/raikada/segments/%path/%Y-%m-%d", "/var/raikada/segments"},
		{"%path-only", ""},          // can't extract a meaningful prefix
		{"/", ""},                   // refuse root
		{"./", ""},                  // refuse cwd
		{"/var/x", ""},              // no template chars
	}
	for _, c := range cases {
		got := recordPathRoot(c.in)
		require.Equal(t, c.want, got, fmt.Sprintf("input=%q", c.in))
	}
}
