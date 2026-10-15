// Package api: Phase 5 Task 5.1 tests for the consumer-friendly auth
// endpoints. Reuses the bootstrap-admin LocalAuth fixture from
// api_v1_recorder_login_test.go.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthLogin_HappyPath_WithCookie(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)

	_, hc := startLoginAPI(t, la)

	body, err := json.Marshal(map[string]string{
		"username": "admin",
		"password": pw,
	})
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		bd, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d body=%s", res.StatusCode, string(bd))
	}

	var out authLoginResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.AccessToken)
	require.True(t, out.User.MustChangePassword)
	require.Equal(t, "admin", out.User.Username)
	require.Equal(t, "admin", out.User.Role)

	// Cookie must be present and contain the JWT.
	var found bool
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			found = true
			require.Equal(t, out.AccessToken, c.Value)
			require.True(t, c.HttpOnly)
			require.True(t, c.Secure)
		}
	}
	require.True(t, found, "raikada_session cookie must be set")
}

func TestAuthLogin_BadCredentialsReturns401(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	_, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)
	_, hc := startLoginAPI(t, la)

	body := `{"username":"admin","password":"wrong"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestAuthLogout_ClearsCookie(t *testing.T) {
	la, _ := newTestLocalAuth(t)
	_, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)
	_, hc := startLoginAPI(t, la)

	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v1/auth/logout", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	var found bool
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			found = true
			require.LessOrEqual(t, c.MaxAge, 0)
		}
	}
	require.True(t, found, "raikada_session cookie must be set to expire")
}
