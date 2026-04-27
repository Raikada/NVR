package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/recordstore"
	"github.com/bluenviron/mediamtx/internal/test"
)

// newV1Server returns a gin handler with just the new /v1 recording-
// related routes mounted. Used in lieu of API.Initialize because Phase
// 2C does not modify api.go's route table; integration of the new
// routes into the main router is handled separately. Tests exercise the
// handlers via this lightweight harness so behavior can be verified
// independently.
func newV1Server(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/recordings", a.onV1RecordingsList)
	r.GET("/v1/recordings/:id", a.onV1RecordingsGet)
	r.GET("/v1/recordings/:id/playback", a.onV1RecordingsPlayback)
	r.GET("/v1/recording-segments", a.onV1RecordingSegmentsList)
	r.GET("/v1/recording-segments/:id", a.onV1RecordingSegmentsGet)
	r.DELETE("/v1/recording-segments/:id", a.onV1RecordingSegmentsDelete)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func newTestAPI(t *testing.T, dir string) *API {
	t.Helper()
	cnf := tempConf(t, "pathDefaults:\n"+
		"  recordPath: "+filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")+"\n"+
		"paths:\n"+
		"  all_others:\n")

	a := &API{
		Conf:        cnf,
		AuthManager: test.NilAuthManager,
		Parent:      &testParent{},
	}
	t.Cleanup(func() {
		// Reset the registry so leftover state from a prior test
		// doesn't leak (the registry map is package-global keyed by
		// *API; cleanup keeps the map from accumulating).
		recordingRegistriesMu.Lock()
		delete(recordingRegistries, a)
		recordingRegistriesMu.Unlock()
	})
	return a
}

func writeSegmentFile(t *testing.T, dir, pathName, ts string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, pathName), 0o755))
	p := filepath.Join(dir, pathName, ts+".mp4")
	require.NoError(t, os.WriteFile(p, []byte("placeholder"), 0o644))
	return p
}

func TestV1RecordingsListSynthesizes(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	// Two segments on cam1 close in time → one Recording.
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")
	// One segment on cam2 → another Recording.
	writeSegmentFile(t, dir, "cam2", "2009-02-01_00-00-00-000000")
	// One segment on cam1 far in the future → distinct Recording.
	writeSegmentFile(t, dir, "cam1", "2025-06-01_00-00-00-000000")

	resp, err := http.Get(srv.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		ItemCount int `json:"item_count"`
		PageCount int `json:"page_count"`
		Items     []struct {
			ID           string    `json:"id"`
			CameraID     string    `json:"camera_id"`
			StartedAt    time.Time `json:"started_at"`
			SegmentCount int       `json:"segment_count"`
			State        string    `json:"state"`
			Segments     []any     `json:"segments,omitempty"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	require.Equal(t, 3, got.ItemCount)
	require.Equal(t, 1, got.PageCount)
	for _, it := range got.Items {
		require.Empty(t, it.Segments,
			"list response must NOT embed segments per ADR 0009 §D5")
		require.Equal(t, "sealed", it.State,
			"synthesized historical Recordings default to sealed")
		_, perr := uuid.Parse(it.ID)
		require.NoError(t, perr, "Recording id must be a UUID")
	}
}

func TestV1RecordingsListCameraFilter(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam2", "2008-11-07_11-22-00-000000")

	cam1 := cameraIDFromPathName("cam1")

	resp, err := http.Get(srv.URL + "/v1/recordings?camera_id=" + cam1)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		ItemCount int `json:"item_count"`
		Items     []struct {
			CameraID string `json:"camera_id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, cam1, got.Items[0].CameraID)
}

func TestV1RecordingsListTimeRangeFilters(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam2", "2025-01-01_00-00-00-000000")

	mid := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	resp, err := http.Get(srv.URL + "/v1/recordings?started_after=" + mid)
	require.NoError(t, err)
	defer resp.Body.Close()
	var got struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)

	resp2, err := http.Get(srv.URL + "/v1/recordings?started_before=" + mid)
	require.NoError(t, err)
	defer resp2.Body.Close()
	var got2 struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got2))
	require.Equal(t, 1, got2.ItemCount)
}

func TestV1RecordingsGetEmbedsSegments(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")

	// Discover the recording id via list.
	resp, err := http.Get(srv.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp.Body.Close()
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	require.Len(t, listed.Items, 1)
	id := listed.Items[0].ID

	// GET by id must embed segments and must NOT leak the on-disk path.
	resp2, err := http.Get(srv.URL + "/v1/recordings/" + id)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var got struct {
		ID       string `json:"id"`
		Segments []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"segments"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got))
	require.Equal(t, id, got.ID)
	require.Len(t, got.Segments, 2)
	for _, s := range got.Segments {
		require.Empty(t, s.Path, "on-disk path must not leak in API response")
	}
}

func TestV1RecordingsGetInvalidUUID(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	resp, err := http.Get(srv.URL + "/v1/recordings/not-a-uuid")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestV1RecordingsGetNotFound(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	resp, err := http.Get(srv.URL + "/v1/recordings/" + uuid.New().String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestV1RecordingsPlayback(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	a.Conf.PlaybackAddress = "localhost:9996"
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")

	resp, err := http.Get(srv.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp.Body.Close()

	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	require.Len(t, listed.Items, 1)
	id := listed.Items[0].ID

	pb, err := http.Get(srv.URL + "/v1/recordings/" + id + "/playback")
	require.NoError(t, err)
	defer pb.Body.Close()
	require.Equal(t, http.StatusOK, pb.StatusCode)

	var got struct {
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.NewDecoder(pb.Body).Decode(&got))
	require.True(t, strings.Contains(got.URL, "localhost:9996"),
		"playback URL must reference recorder's playback bind address: got %q", got.URL)
	require.True(t, strings.Contains(got.URL, "path=cam1"),
		"playback URL must reference path: got %q", got.URL)
	require.True(t, got.ExpiresAt.After(time.Now()),
		"expires_at must be in the future")
}

func TestV1RecordingsRecordingIDStability(t *testing.T) {
	// Synthesizing twice over the same on-disk state must yield the
	// same Recording UUID — required by ADR 0009 D5 / D8 closure.
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")

	a1 := newTestAPI(t, dir)
	srv1 := newV1Server(t, a1)
	resp, err := http.Get(srv1.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp.Body.Close()
	var first struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
	require.Len(t, first.Items, 1)

	a2 := newTestAPI(t, dir)
	srv2 := newV1Server(t, a2)
	resp2, err := http.Get(srv2.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var second struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&second))
	require.Len(t, second.Items, 1)

	require.Equal(t, first.Items[0].ID, second.Items[0].ID,
		"Recording UUID must be stable across restarts (UUIDv5)")
}

func TestGroupSegmentsByGap(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(start, end time.Time) defs.RecordingSegment {
		return defs.RecordingSegment{
			ID:        fmt.Sprintf("seg-%d", start.Unix()),
			StartedAt: start,
			EndedAt:   end,
			Duration:  end.Sub(start),
		}
	}
	segs := []defs.RecordingSegment{
		mk(now, now.Add(30*time.Second)),
		mk(now.Add(30*time.Second), now.Add(60*time.Second)),
		// 5-minute gap → distinct group.
		mk(now.Add(360*time.Second), now.Add(390*time.Second)),
	}
	groups := groupSegmentsByGap(segs, recordingGapThreshold)
	require.Len(t, groups, 2)
	require.Len(t, groups[0], 2)
	require.Len(t, groups[1], 1)
}

func TestGroupSegmentsByGapEmpty(t *testing.T) {
	groups := groupSegmentsByGap(nil, recordingGapThreshold)
	require.NotNil(t, groups)
	require.Len(t, groups, 0)
}

// TestV1RecordingsActiveStateNoLiveSegment confirms the pre-existing
// behavior: when no segment is registered as in-flight in the recordstore
// CurrentSegment registry, every synthesized Recording stays sealed. This
// is the regression guard for the active-state change.
func TestV1RecordingsActiveStateNoLiveSegment(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings-noactive")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")

	resp, err := http.Get(srv.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Items []struct {
			State   string     `json:"state"`
			EndedAt *time.Time `json:"ended_at"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 1)
	require.Equal(t, "sealed", got.Items[0].State)
	require.NotNil(t, got.Items[0].EndedAt)
}

// TestV1RecordingsActiveStateLatestSegmentLive confirms the new behavior:
// when the most-recent on-disk segment for a camera is registered as
// in-flight in the recordstore CurrentSegment registry, the Recording it
// belongs to is reported with state=active and ended_at=null. Older
// Recordings on the same camera (separated by gap) stay sealed.
func TestV1RecordingsActiveStateLatestSegmentLive(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-recordings-active")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	// Older Recording — two close-together segments on cam1.
	oldA := writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	oldB := writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")
	// Latest Recording — distinct gap, single segment that we will
	// register as in-flight.
	live := writeSegmentFile(t, dir, "cam1", "2025-06-01_00-00-00-000000")

	recordstore.RegisterCurrentSegment(live)
	t.Cleanup(func() {
		recordstore.UnregisterCurrentSegment(live)
	})

	// Sanity: the older segments are not registered.
	require.False(t, recordstore.IsCurrentSegment(oldA))
	require.False(t, recordstore.IsCurrentSegment(oldB))
	require.True(t, recordstore.IsCurrentSegment(live))

	resp, err := http.Get(srv.URL + "/v1/recordings")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Items []struct {
			ID        string     `json:"id"`
			State     string     `json:"state"`
			StartedAt time.Time  `json:"started_at"`
			EndedAt   *time.Time `json:"ended_at"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 2)

	// Items are sorted started_at desc — index 0 is the live one.
	require.Equal(t, "active", got.Items[0].State,
		"latest Recording should be active when its segment is in-flight")
	require.Nil(t, got.Items[0].EndedAt,
		"active Recordings have ended_at=null per ADR 0009")

	require.Equal(t, "sealed", got.Items[1].State,
		"older Recording on the same camera stays sealed")
	require.NotNil(t, got.Items[1].EndedAt)
}
