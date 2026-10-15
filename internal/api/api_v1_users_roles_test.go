// Package api: Phase A B2 — five-role journey through create → login
// → claims → enforcement.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

// startRoleJourneyAPI wires localauth + store + users surface on one DB.
func startRoleJourneyAPI(t *testing.T) (*http.Client, string) {
	t.Helper()
	la, dir := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(context.Background())
	require.NoError(t, err)
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		Conf:         cnf,
		AuthManager:  test.NilAuthManager,
		LocalAuth:    la,
		Store:        st,
		Parent:       &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}, pw
}

func authedJSON(t *testing.T, hc *http.Client, method, url, token string, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var buf [8192]byte
	n, _ := res.Body.Read(buf[:])
	return res, buf[:n]
}

func TestFiveRoleJourney(t *testing.T) {
	hc, pw := startRoleJourneyAPI(t)
	adminToken := loginForToken(t, hc, pw)

	// Admin creates one user per new role.
	for _, role := range []string{"owner", "operator", "audit"} {
		res, body := authedJSON(t, hc, http.MethodPost, "http://localhost:9997/v1/users", adminToken,
			`{"username":"`+role+`1","password":"Longenough-12345","role":"`+role+`"}`)
		require.Equal(t, http.StatusCreated, res.StatusCode, "create %s: %s", role, body)
		var out struct {
			Role string `json:"role"`
		}
		require.NoError(t, json.Unmarshal(body, &out))
		require.Equal(t, role, out.Role, "wire role echoes back")
	}

	// Operator: login → me shows operator w/ ptz but no user.create;
	// enforcement denies a camera create.
	opRes, opBody := authedJSON(t, hc, http.MethodPost, "http://localhost:9997/v1/auth/login", "",
		`{"username":"operator1","password":"Longenough-12345"}`)
	require.Equal(t, http.StatusOK, opRes.StatusCode, "operator login: %s", opBody)
	var opLogin struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(opBody, &opLogin))

	meRes, meBody := authedJSON(t, hc, http.MethodGet, "http://localhost:9997/v1/auth/me", opLogin.AccessToken, "")
	require.Equal(t, http.StatusOK, meRes.StatusCode)
	var me struct {
		User struct {
			Role string `json:"role"`
		} `json:"user"`
		Permissions []string `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(meBody, &me))
	require.Equal(t, "operator", me.User.Role)
	require.Contains(t, me.Permissions, "camera.ptz")
	require.Contains(t, me.Permissions, "event.ack")
	require.NotContains(t, me.Permissions, "user.create")

	denyRes, _ := authedJSON(t, hc, http.MethodPost, "http://localhost:9997/v1/users", opLogin.AccessToken,
		`{"username":"sneaky","password":"Longenough-12345","role":"admin"}`)
	require.Equal(t, http.StatusForbidden, denyRes.StatusCode, "operator must not create users")

	// Owner: full admin surface — may create a user.
	owRes, owBody := authedJSON(t, hc, http.MethodPost, "http://localhost:9997/v1/auth/login", "",
		`{"username":"owner1","password":"Longenough-12345"}`)
	require.Equal(t, http.StatusOK, owRes.StatusCode)
	var owLogin struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(owBody, &owLogin))
	okRes, okBody := authedJSON(t, hc, http.MethodPost, "http://localhost:9997/v1/users", owLogin.AccessToken,
		`{"username":"fromowner","password":"Longenough-12345","role":"viewer"}`)
	require.Equal(t, http.StatusCreated, okRes.StatusCode, "owner creates user: %s", okBody)

	// Garbage role rejected.
	badRes, _ := authedJSON(t, hc, http.MethodPost, "http://localhost:9997/v1/users", adminToken,
		`{"username":"pigeon1","password":"Longenough-12345","role":"pigeon"}`)
	require.Equal(t, http.StatusBadRequest, badRes.StatusCode)
}
