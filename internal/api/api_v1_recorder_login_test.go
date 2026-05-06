package api //nolint:revive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/localauth"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

func newTestLocalAuth(t *testing.T) (*localauth.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	sk, err := localauth.LoadOrCreateSigningKey(dir)
	require.NoError(t, err)
	la := localauth.New(s, sk, dir, uuid.NewString(), uuid.NewString())
	return la, dir
}

func startLoginAPI(t *testing.T, la *localauth.Manager) (*API, *http.Client) {
	t.Helper()
	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		Conf:         cnf,
		AuthManager:  test.NilAuthManager,
		LocalAuth:    la,
		Parent:       &testParent{},
	}
	err := api.Initialize()
	require.NoError(t, err)
	t.Cleanup(api.Close)

	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	return api, hc
}

func TestLogin_HappyPathWithBootstrapAdmin(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)

	_, hc := startLoginAPI(t, la)

	body, err := json.Marshal(map[string]string{
		"username": "admin",
		"password": pw,
	})
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/recorder/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		bd, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d body=%s", res.StatusCode, string(bd))
	}

	var out loginResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Token)
	require.True(t, out.MustChangePassword)
	require.True(t, out.IsAdmin)
	require.NotEmpty(t, out.Scope)
}

func TestLogin_BadCredentialsReturns401(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	_, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)
	_, hc := startLoginAPI(t, la)

	body := `{"username":"admin","password":"wrong"}`
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/recorder/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestLogin_MissingFieldReturns400(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	_, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)
	_, hc := startLoginAPI(t, la)

	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/recorder/login",
		strings.NewReader(`{"username":""}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestLogin_PreAuthBypass_AccessibleWithoutAuthHeader(t *testing.T) {
	// When the auth manager would normally reject every unauthenticated
	// /v1 request, the login endpoint should still be reachable via the
	// pre-auth bypass list. Use an auth manager that always rejects to
	// confirm.
	la, _ := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)

	cnf := tempConf(t, "api: yes\n")
	api := &API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		Conf:         cnf,
		AuthManager:  test.NilAuthManager,
		LocalAuth:    la,
		Parent:       &testParent{},
	}
	require.NoError(t, api.Initialize())
	defer api.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": pw})
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/recorder/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}
