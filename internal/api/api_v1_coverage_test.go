// Package api: Phase A B4 — recording-coverage endpoint.
package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Coverage returns merged {start,duration} ranges with true durations
// parsed from the fmp4 parts — the timeline's footage bars and gaps.
func TestCameraCoverage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir, err := os.MkdirTemp("", "mediamtx-coverage")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	r := gin.New()
	r.GET("/v1/cameras/:id/coverage", a.onV1CameraCoverage)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Two back-to-back segments (fixture ≈1s each, 1s apart → one
	// merged range) plus a distant one (its own range).
	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))
	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-01-000000", fixtureMP4Bytes(t))
	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_13-00-00-000000", fixtureMP4Bytes(t))
	cam := cameraIDFromPathName("cam1")

	res, err := http.Get(srv.URL + "/v1/cameras/" + cam + "/coverage")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out struct {
		Items []struct {
			Start    time.Time `json:"start"`
			Duration float64   `json:"duration"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Len(t, out.Items, 2, "adjacent segments merge; the distant one is its own range")
	require.Greater(t, out.Items[0].Duration, 1.0, "merged range spans both segments")
	require.True(t, out.Items[1].Start.After(out.Items[0].Start))

	// Range filter: a window around only the later segment returns one range.
	from := time.Date(2008, 11, 7, 12, 30, 0, 0, time.Local).Format(time.RFC3339)
	res2, err := http.Get(srv.URL + "/v1/cameras/" + cam + "/coverage?from=" + from)
	require.NoError(t, err)
	defer res2.Body.Close()
	require.NoError(t, json.NewDecoder(res2.Body).Decode(&out))
	require.Len(t, out.Items, 1)

	// Unknown camera → 404.
	res3, err := http.Get(srv.URL + "/v1/cameras/ghost/coverage")
	require.NoError(t, err)
	res3.Body.Close()
	require.Equal(t, http.StatusNotFound, res3.StatusCode)
}
