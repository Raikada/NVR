// Package api: Phase 5 Task 5.7 tests for /v1/system/*.
package api //nolint:revive

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

func startSystemAPI(t *testing.T) (*API, *http.Client, *store.Store, *identity.Identity) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	id, err := identity.Open(filepath.Join(dir, "identity"))
	require.NoError(t, err)
	vault, err := cameracred.Open(dir)
	require.NoError(t, err)
	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second), WriteTimeout: conf.Duration(10 * time.Second),
		Conf: cnf, AuthManager: test.NilAuthManager,
		Store: st, Identity: id, Vault: vault,
		Parent: &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	return a, hc, st, id
}

func TestSystemSettings_AllowListRejection(t *testing.T) {
	_, hc, _, _ := startSystemAPI(t)
	body := `{"unknown_key":"x"}`
	req, _ := http.NewRequest(http.MethodPatch,
		"http://localhost:9997/v1/system/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSystemSettings_HappyPath(t *testing.T) {
	_, hc, _, _ := startSystemAPI(t)
	body := `{"site_name":"My Site","timezone":"UTC"}`
	req, _ := http.NewRequest(http.MethodPatch,
		"http://localhost:9997/v1/system/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestSystemSettings_SMTPPasswordEncryptedAtRest(t *testing.T) {
	_, hc, st, _ := startSystemAPI(t)
	body := `{"smtp_password":"my secret","smtp_host":"smtp.example"}`
	req, _ := http.NewRequest(http.MethodPatch,
		"http://localhost:9997/v1/system/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Inspect the persisted setting: must be hex-encoded ciphertext, not
	// the plaintext password.
	got, err := st.SystemSettings.Get(context.Background(), "smtp_password_ciphertext")
	require.NoError(t, err)
	require.NotEmpty(t, got.Value)
	require.NotContains(t, got.Value, "my secret")
}

func TestSystemSetupStatus_BootstrapNeeded(t *testing.T) {
	_, hc, _, _ := startSystemAPI(t)
	req, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/v1/system/setup-status", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, true, out["setup_required"])
}

func TestSystemSetupStatus_AfterRotatedAdmin(t *testing.T) {
	_, hc, st, _ := startSystemAPI(t)
	require.NoError(t, st.LocalUsers.Insert(context.Background(), &store.LocalUser{
		ID: uuid.NewString(), Username: "admin", PasswordHash: "x",
		IsAdmin: true, IsActive: true, MustChangePassword: false,
	}))
	req, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/v1/system/setup-status", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, false, out["setup_required"])
}

func TestSystemTLS_RejectsBadPEM(t *testing.T) {
	_, hc, _, _ := startSystemAPI(t)
	body := `{"cert_pem":"not a cert","key_pem":"not a key"}`
	req, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/system/tls", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSystemTLS_AcceptsValidPEM(t *testing.T) {
	_, hc, _, _ := startSystemAPI(t)

	// Generate a self-signed cert + key for the test.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	body := struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}{certPEM, keyPEM}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut,
		"http://localhost:9997/v1/system/tls", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}
