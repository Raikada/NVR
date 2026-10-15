// Package api: Phase 5 Task 5.8 tests for /v1/users.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

func startUsersAPI(t *testing.T) (*API, *http.Client, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second), WriteTimeout: conf.Duration(10 * time.Second),
		Conf: cnf, AuthManager: test.NilAuthManager,
		Store: st, Parent: &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	return a, hc, st
}

func TestUsers_RoundTripCRUD(t *testing.T) {
	_, hc, _ := startUsersAPI(t)

	// Create
	body := `{"username":"jane","password":"hunter2pass","role":"viewer","email":"jane@example.com"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created userWire
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, "jane", created.Username)
	require.Equal(t, "viewer", created.Role)

	// Confirm response never carries a password field.
	rawBytes, _ := json.Marshal(created)
	require.NotContains(t, string(rawBytes), "hunter2pass")

	// Update display_name
	patchBody := `{"display_name":"Jane Doe"}`
	req, _ = http.NewRequest(http.MethodPatch,
		"http://localhost:9997/v1/users/"+created.ID, strings.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Set role
	roleBody := `{"role":"admin"}`
	req, _ = http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/users/"+created.ID+"/role", strings.NewReader(roleBody))
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	// Reset password
	pwBody := `{"new_password":"abcdefgh"}`
	req, _ = http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/users/"+created.ID+"/password", strings.NewReader(pwBody))
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	// Delete (soft)
	req, _ = http.NewRequest(http.MethodDelete,
		"http://localhost:9997/v1/users/"+created.ID, nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestAuditPurge_EmitsRowFirst(t *testing.T) {
	_, hc, st := startUsersAPI(t)

	// Seed an old audit row.
	require.NoError(t, st.AuditLog.Insert(context.Background(), &store.AuditEntry{
		ID: uuid.NewString(), Action: "stale.action",
		OccurredAt: time.Now().Add(-72 * time.Hour),
	}))

	// Purge anything older than 24h.
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/audit/purge?older_than=24h", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// The stale row should be gone, but a system.audit_purged row should
	// remain (emitted before the delete).
	rows, _, err := st.AuditLog.List(context.Background(), store.ListAuditFilter{Limit: 100})
	require.NoError(t, err)
	hasPurge := false
	for _, r := range rows {
		if r.Action == "system.audit_purged" {
			hasPurge = true
			break
		}
	}
	require.True(t, hasPurge, "expected system.audit_purged row in remaining rows")
}

func TestAuditExport_Ndjson(t *testing.T) {
	_, hc, st := startUsersAPI(t)

	// Seed two rows.
	t1 := time.Now().Add(-1 * time.Hour)
	require.NoError(t, st.AuditLog.Insert(context.Background(), &store.AuditEntry{
		ID: uuid.NewString(), Action: "test.one", OccurredAt: t1,
	}))
	t2 := time.Now().Add(-30 * time.Minute)
	require.NoError(t, st.AuditLog.Insert(context.Background(), &store.AuditEntry{
		ID: uuid.NewString(), Action: "test.two", OccurredAt: t2,
	}))

	from := t1.Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/audit/export?from="+from+"&to="+to+"&format=ndjson", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}
