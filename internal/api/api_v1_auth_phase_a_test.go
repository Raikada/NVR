// Package api: Phase A B1 — SSE query-token auth + token refresh.
package api //nolint:revive

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

// login returns a valid bearer token from the test localauth stack.
func loginForToken(t *testing.T, hc *http.Client, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	res, err := hc.Post("http://localhost:9997/v1/auth/login", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("login HTTP %d: %s", res.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.AccessToken)
	return out.AccessToken
}

func TestAuthRefresh(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)
	_, hc := startLoginAPI(t, la)
	token := loginForToken(t, hc, pw)

	// Valid token → fresh token with a (re)extended expiry.
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out struct {
		AccessToken string    `json:"access_token"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.AccessToken)
	require.True(t, out.ExpiresAt.After(time.Now().Add(5*time.Minute)),
		"refreshed token must extend the session")

	// The refreshed token authenticates.
	req2, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/v1/auth/me", nil)
	req2.Header.Set("Authorization", "Bearer "+out.AccessToken)
	res2, err := hc.Do(req2)
	require.NoError(t, err)
	res2.Body.Close()
	require.Equal(t, http.StatusOK, res2.StatusCode)

	// No token → 401.
	req3, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/auth/refresh", nil)
	res3, err := hc.Do(req3)
	require.NoError(t, err)
	res3.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res3.StatusCode)
}

// bearerOnlyAuthManager accepts exactly one bearer token — unlike
// NilAuthManager it lets negative auth cases be asserted.
type bearerOnlyAuthManager struct{ token string }

func (m *bearerOnlyAuthManager) Authenticate(req *auth.Request) (string, *auth.Error) {
	if req.Credentials != nil && req.Credentials.Token == m.token {
		return "", nil
	}
	return "", &auth.Error{}
}

func (m *bearerOnlyAuthManager) AuthenticateWithClaims(req *auth.Request) (string, auth.Claims, *auth.Error) {
	if req.Credentials != nil && req.Credentials.Token == m.token {
		return "", auth.Claims{}, nil
	}
	return "", auth.Claims{}, &auth.Error{}
}

func TestEventsStreamQueryTokenAuth(t *testing.T) {
	la, dir := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)

	// The SSE handler needs the events service; wire the same store.
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	evs := events.NewService(st.Events, st.EventRetention, nil, events.NewBus())

	cnf := tempConf(t, "api: yes\n")
	res0, err := la.Login(context.Background(), "admin", pw, "127.0.0.1")
	require.NoError(t, err)
	token0 := res0.Token
	a := &API{
		Address:       "localhost:9997",
		ReadTimeout:   conf.Duration(10 * time.Second),
		WriteTimeout:  conf.Duration(10 * time.Second),
		Conf:          cnf,
		AuthManager:   &bearerOnlyAuthManager{token: token0},
		LocalAuth:     la,
		Store:         st,
		EventsService: evs,
		Parent:        &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	token := token0

	// EventSource semantics: GET with the token ONLY in the query.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://localhost:9997/v1/events/stream?access_token="+token, nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"query token must authenticate the SSE route")
	require.Contains(t, res.Header.Get("Content-Type"), "text/event-stream")
	// The handler must have flushed its SSE preamble promptly.
	br := bufio.NewReader(res.Body)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.NotEmpty(t, line)

	// A bad query token is rejected.
	req2, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/v1/events/stream?access_token=garbage", nil)
	res2, err := hc.Do(req2)
	require.NoError(t, err)
	res2.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res2.StatusCode)

	// The query token must NOT work on other routes (header-only there).
	res3, err := hc.Get("http://localhost:9997/v1/auth/me?access_token=" + token)
	require.NoError(t, err)
	res3.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res3.StatusCode,
		"query tokens are scoped to the SSE route only")
}
