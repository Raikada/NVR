package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

func newV1HealthServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/health", a.onV1HealthGet)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestV1HealthGetSnapshotShape(t *testing.T) {
	cnf := tempConf(t, "")
	a := &API{
		Started: time.Now().Add(-2 * time.Minute),
		Conf:    cnf,
	}
	srv := newV1HealthServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got defs.HealthStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	// id must be a UUID.
	_, err = uuid.Parse(got.ID)
	require.NoError(t, err, "snapshot id must be a UUID")

	// tenant_id propagates from conf.
	require.Equal(t, cnf.TenantID, got.TenantID)

	// uptime > 0 (we set Started two minutes in the past).
	require.Greater(t, got.Uptime, time.Duration(0))

	// reported_at is recent.
	require.WithinDuration(t, time.Now().UTC(), got.ReportedAt, 5*time.Second)

	// overall is set to one of the canonical values.
	switch got.Overall {
	case defs.HealthStatusOverallHealthy,
		defs.HealthStatusOverallDegraded,
		defs.HealthStatusOverallUnhealthy:
	default:
		t.Fatalf("invalid overall classification: %q", got.Overall)
	}

	// cpu_pct is host-wide (per ADR 0009 amendment 2026-04-27-adr-
	// 0009-amendment-3): saturated reads ~100 regardless of core
	// count. A single /v1/health call after handler creation hits
	// the sampler's first-call-returns-zero path. Either way the
	// value must be a sane non-negative number bounded by 100.
	require.GreaterOrEqual(t, got.CPUPct, 0.0, "cpu_pct must be non-negative")
	require.LessOrEqual(t, got.CPUPct, 100.0, "cpu_pct must not exceed 100")
}

func TestV1HealthSnapshotIDStableAcrossCalls(t *testing.T) {
	a := &API{
		Started: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		Conf:    tempConf(t, ""),
	}
	srv := newV1HealthServer(t, a)

	resp1, err := http.Get(srv.URL + "/v1/health")
	require.NoError(t, err)
	defer resp1.Body.Close()
	var got1 defs.HealthStatus
	require.NoError(t, json.NewDecoder(resp1.Body).Decode(&got1))

	resp2, err := http.Get(srv.URL + "/v1/health")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var got2 defs.HealthStatus
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got2))

	// Same process / Started → same snapshot id.
	require.Equal(t, got1.ID, got2.ID, "snapshot id must be stable per-process")
}

func TestV1HealthZeroStartedFallback(t *testing.T) {
	a := &API{Conf: tempConf(t, "")}
	srv := newV1HealthServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got defs.HealthStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	// With zero Started we fall back to the sentinel UUID.
	require.Equal(t, "00000000-0000-0000-0000-000000000000", got.ID)
}

func TestVolumeStatusToHealthState(t *testing.T) {
	for _, c := range []struct {
		in   defs.StorageVolumeStatus
		want defs.HealthStatusVolumeState
	}{
		{defs.StorageVolumeStatusHealthy, defs.HealthStatusVolumeStateHealthy},
		{defs.StorageVolumeStatusDegraded, defs.HealthStatusVolumeStateDegraded},
		{defs.StorageVolumeStatusFull, defs.HealthStatusVolumeStateFull},
		{defs.StorageVolumeStatusReadOnly, defs.HealthStatusVolumeStateReadOnly},
		{defs.StorageVolumeStatusMissing, defs.HealthStatusVolumeStateDegraded},
	} {
		require.Equal(t, c.want, volumeStatusToHealthState(c.in), "in=%v", c.in)
	}
}

func TestHealthSnapshotIDDeterministic(t *testing.T) {
	t1 := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	id1 := healthSnapshotID(t1)
	id2 := healthSnapshotID(t1)
	require.Equal(t, id1, id2)
	_, err := uuid.Parse(id1)
	require.NoError(t, err)

	t2 := t1.Add(time.Second)
	id3 := healthSnapshotID(t2)
	require.NotEqual(t, id1, id3, "different started → different id")
}
