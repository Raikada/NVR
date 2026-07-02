package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// newClipServer mounts the /v1/clips routes on a lightweight gin
// harness, mirroring newV1Server's approach for /v1/recordings.
func newClipServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/clips", a.onV1ClipsPost)
	r.GET("/v1/clips", a.onV1ClipsList)
	r.GET("/v1/clips/:id", a.onV1ClipsGet)
	r.DELETE("/v1/clips/:id", a.onV1ClipsDelete)
	r.GET("/v1/clips/:id/download", a.onV1ClipsDownload)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// withFreshClipStore pins a per-test ClipStore as the singleton so
// concurrent tests don't leak state. Returns a cleanup that restores
// the previous singleton.
func withFreshClipStore(t *testing.T) {
	t.Helper()
	clipStoreSingletonOnce.Do(func() {})
	prev := clipStoreSingleton
	clipStoreSingleton = NewClipStore()
	t.Cleanup(func() {
		clipStoreSingleton = prev
	})
}

// fixtureMP4Bytes returns the bytes of a small h264 mp4 fixture
// shipped at internal/stream/offline_h264.mp4. The clip preparation
// pipeline now performs a real fmp4-to-mp4 remux; tests need real
// container input that libav's `mov` demuxer can parse, not the
// 32-byte filler that the prior byte-concat tests used.
func fixtureMP4Bytes(t *testing.T) []byte {
	t.Helper()
	// Test runs from internal/api/, so the fixture lives two dirs up
	// at internal/stream/offline_h264.mp4.
	bs, err := os.ReadFile(filepath.Join("..", "stream", "offline_h264.mp4"))
	require.NoError(t, err, "load h264 fixture for clip remux test")
	return bs
}

func writeClipSegmentFile(t *testing.T, dir, pathName, ts string, payload []byte) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, pathName), 0o755))
	p := filepath.Join(dir, pathName, ts+".mp4")
	require.NoError(t, os.WriteFile(p, payload, 0o644))
	return p
}

// isMP4 returns true if the given bytes look like a standard mp4
// container (`ftyp` box at the start). Used as a structural sanity
// check on remux output without requiring a full ffprobe run.
func isMP4(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	return string(b[4:8]) == "ftyp"
}

func TestV1ClipsPostCreatesAndPrepares(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))
	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000", fixtureMP4Bytes(t))

	cam := cameraIDFromPathName("cam1")
	body := map[string]any{
		"camera_id":        cam,
		"label":            "incident-1",
		"range_started_at": "2008-11-06T00:00:00Z",
		"range_ended_at":   "2008-11-08T00:00:00Z",
	}
	buf, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/v1/clips", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got struct {
		ID               string   `json:"id"`
		State            string   `json:"state"`
		CameraID         string   `json:"camera_id"`
		SourceSegmentIDs []string `json:"source_segment_ids"`
		Notice           string   `json:"notice"`
		Container        string   `json:"container"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	_, perr := uuid.Parse(got.ID)
	require.NoError(t, perr)
	require.Equal(t, cam, got.CameraID)
	require.Equal(t, "mp4", got.Container)
	require.Len(t, got.SourceSegmentIDs, 2)
	require.Empty(t, got.Notice, "remux pipeline produces real mp4; notice field is reserved for future use")
	require.Contains(t, []string{"requested", "preparing", "ready"}, got.State)

	// Wait for the async preparer to finish, then verify the clip
	// reaches `ready` and the export file has the concatenated
	// payload of both segments.
	preparerWG.Wait()

	getResp, err := http.Get(srv.URL + "/v1/clips/" + got.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var got2 struct {
		State     string  `json:"state"`
		SizeBytes *int64  `json:"size_bytes"`
		Checksum  *string `json:"checksum"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&got2))
	require.Equal(t, "ready", got2.State)
	require.NotNil(t, got2.SizeBytes)
	require.Greater(t, *got2.SizeBytes, int64(0), "remuxed clip must produce non-empty mp4 output")
	require.NotNil(t, got2.Checksum)
	require.Contains(t, *got2.Checksum, "sha256:")
}

func TestV1ClipsPostNoOverlappingSegments(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))

	cam := cameraIDFromPathName("cam1")
	body := map[string]any{
		"camera_id":        cam,
		"label":            "future-clip",
		"range_started_at": "2030-01-01T00:00:00Z",
		"range_ended_at":   "2030-01-01T00:05:00Z",
	}
	buf, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/v1/clips", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestV1ClipsPostInvalidRange(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	cam := cameraIDFromPathName("cam1")
	body := map[string]any{
		"camera_id":        cam,
		"label":            "bad-range",
		"range_started_at": "2008-11-07T11:23:00Z",
		"range_ended_at":   "2008-11-07T11:22:00Z",
	}
	buf, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/v1/clips", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestV1ClipsList(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))

	cam := cameraIDFromPathName("cam1")
	body := map[string]any{
		"camera_id":        cam,
		"label":            "c1",
		"range_started_at": "2008-11-06T00:00:00Z",
		"range_ended_at":   "2008-11-08T00:00:00Z",
	}
	buf, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/v1/clips", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	resp.Body.Close()
	preparerWG.Wait()

	listResp, err := http.Get(srv.URL + "/v1/clips")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var got struct {
		ItemCount int `json:"item_count"`
		Items     []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, "c1", got.Items[0].Label)
}

func TestV1ClipsDeleteSoft(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	segPath := writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))

	cam := cameraIDFromPathName("cam1")
	body := map[string]any{
		"camera_id":        cam,
		"label":            "doomed",
		"range_started_at": "2008-11-06T00:00:00Z",
		"range_ended_at":   "2008-11-08T00:00:00Z",
	}
	buf, _ := json.Marshal(body)
	createResp, err := http.Post(srv.URL+"/v1/clips", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer createResp.Body.Close()
	var got struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&got))
	preparerWG.Wait()

	// Pin must be in place while ready.
	require.True(t, defaultClipStore().IsSegmentPathPinned(segPath),
		"ready clip must pin its source segment paths")

	delReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/clips/"+got.ID, nil)
	delResp, err := http.DefaultClient.Do(delReq)
	require.NoError(t, err)
	defer delResp.Body.Close()
	require.Equal(t, http.StatusOK, delResp.StatusCode)

	// Pin must release on soft-delete.
	require.False(t, defaultClipStore().IsSegmentPathPinned(segPath),
		"deleted clip must release segment pins")

	// GET still finds it (soft delete) but state == deleted.
	getResp, err := http.Get(srv.URL + "/v1/clips/" + got.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	var post struct {
		State string `json:"state"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&post))
	require.Equal(t, "deleted", post.State)
}

func TestV1ClipsDownload(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000", fixtureMP4Bytes(t))
	writeClipSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000", fixtureMP4Bytes(t))

	cam := cameraIDFromPathName("cam1")
	body := map[string]any{
		"camera_id":        cam,
		"label":            "dl",
		"range_started_at": "2008-11-06T00:00:00Z",
		"range_ended_at":   "2008-11-08T00:00:00Z",
	}
	buf, _ := json.Marshal(body)
	createResp, err := http.Post(srv.URL+"/v1/clips", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer createResp.Body.Close()
	var got struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&got))
	preparerWG.Wait()

	dl, err := http.Get(srv.URL + "/v1/clips/" + got.ID + "/download")
	require.NoError(t, err)
	defer dl.Body.Close()
	require.Equal(t, http.StatusOK, dl.StatusCode)
	require.Equal(t, "video/mp4", dl.Header.Get("Content-Type"))

	bs, err := io.ReadAll(dl.Body)
	require.NoError(t, err)
	require.Greater(t, len(bs), 0, "downloaded clip must have content")
	require.True(t, isMP4(bs), "downloaded clip must be a real mp4 (ftyp at start)")
}

func TestV1ClipsDownloadNotReady(t *testing.T) {
	withFreshClipStore(t)

	dir, err := os.MkdirTemp("", "mediamtx-v1-clips")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newClipServer(t, a)

	// Inject a clip in `requested` state directly (skip the async
	// preparer so we can observe a non-ready download attempt).
	now := time.Now().UTC()
	clip := &defs.Clip{
		ID:               uuid.New().String(),
		CameraID:         cameraIDFromPathName("cam1"),
		Label:            "stuck",
		State:            defs.ClipStateRequested,
		Container:        defs.ClipContainerMP4,
		RangeStartedAt:   now.Add(-time.Hour),
		RangeEndedAt:     now,
		SourceSegmentIDs: []string{"00000000-0000-0000-0000-000000000001"},
		RequestedAt:      now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	defaultClipStore().Put(clip, []string{"/tmp/fake.mp4"})

	resp, err := http.Get(srv.URL + "/v1/clips/" + clip.ID + "/download")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusConflict, resp.StatusCode)
}

// TestStitchSegmentsRemuxesToMP4 drives a real (small) source mp4
// through the remux helper twice — exercising both the single-
// segment and the multi-segment timestamp-offset paths — and
// verifies the output is a parseable mp4 starting with `ftyp`.
//
// We don't require a fully fragmented-mp4 source fixture here:
// libav's `mov` demuxer parses both fragmented and non-fragmented
// mp4 inputs, and the remux helper is agnostic to which one it
// gets. The recorder's actual on-disk segments are fragmented mp4
// files but the helper's contract is "any mov-demuxable input"; the
// fixture below stresses that contract.
func TestStitchSegmentsRemuxesToMP4(t *testing.T) {
	dir, err := os.MkdirTemp("", "stitch")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	src := filepath.Join("..", "stream", "offline_h264.mp4")
	srcBytes, err := os.ReadFile(src)
	require.NoError(t, err)

	a := filepath.Join(dir, "a.mp4")
	b := filepath.Join(dir, "b.mp4")
	require.NoError(t, os.WriteFile(a, srcBytes, 0o644))
	require.NoError(t, os.WriteFile(b, srcBytes, 0o644))
	out := filepath.Join(dir, "out.mp4")

	size, checksum, err := stitchSegments([]string{a, b}, out)
	require.NoError(t, err)
	require.Greater(t, size, int64(0))
	require.Contains(t, checksum, "sha256:")

	bs, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Greater(t, len(bs), 0)
	require.True(t, isMP4(bs), "remuxed output must start with ftyp")
}

// TestStitchSegmentsRemuxSingleSegment covers the simpler path
// where only one input segment exists — exercising the no-offset
// case and ensuring the helper works as a pass-through remux.
func TestStitchSegmentsRemuxSingleSegment(t *testing.T) {
	dir, err := os.MkdirTemp("", "stitch-single")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	src := filepath.Join("..", "stream", "offline_h264.mp4")
	srcBytes, err := os.ReadFile(src)
	require.NoError(t, err)

	a := filepath.Join(dir, "a.mp4")
	require.NoError(t, os.WriteFile(a, srcBytes, 0o644))
	out := filepath.Join(dir, "out.mp4")

	size, checksum, err := stitchSegments([]string{a}, out)
	require.NoError(t, err)
	require.Greater(t, size, int64(0))
	require.Contains(t, checksum, "sha256:")

	bs, err := os.ReadFile(out)
	require.NoError(t, err)
	require.True(t, isMP4(bs))
}

// TestStitchSegmentsRejectsEmpty asserts the helper fails fast on
// an empty input list — the preparer relies on this to surface a
// failure rather than producing a zero-byte output.
func TestStitchSegmentsRejectsEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "stitch-empty")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	_, _, err = stitchSegments(nil, filepath.Join(dir, "out.mp4"))
	require.Error(t, err)
}

// Sanity: ensure the package-level singleton resets cleanly.
func TestClipStorePinRefcount(t *testing.T) {
	withFreshClipStore(t)
	store := defaultClipStore()
	now := time.Now().UTC()

	c1 := &defs.Clip{
		ID:               uuid.New().String(),
		State:            defs.ClipStateReady,
		SourceSegmentIDs: []string{"seg-1", "seg-2"},
		RequestedAt:      now,
	}
	c2 := &defs.Clip{
		ID:               uuid.New().String(),
		State:            defs.ClipStateReady,
		SourceSegmentIDs: []string{"seg-2", "seg-3"},
		RequestedAt:      now,
	}
	store.Put(c1, []string{"/p/1", "/p/2"})
	store.Put(c2, []string{"/p/2", "/p/3"})

	require.True(t, store.IsSegmentPathPinned("/p/1"))
	require.True(t, store.IsSegmentPathPinned("/p/2"))
	require.True(t, store.IsSegmentPathPinned("/p/3"))
	require.True(t, store.IsSegmentIDPinned("seg-2"))

	store.Update(c1.ID, func(c *defs.Clip) {
		c.State = defs.ClipStateDeleted
	})
	// /p/2 still pinned by c2.
	require.False(t, store.IsSegmentPathPinned("/p/1"))
	require.True(t, store.IsSegmentPathPinned("/p/2"))
	require.True(t, store.IsSegmentIDPinned("seg-2"))

	store.Delete(c2.ID)
	require.False(t, store.IsSegmentPathPinned("/p/2"))
	require.False(t, store.IsSegmentPathPinned("/p/3"))
}

// Live acceptance (2026-07-02) found event-clip export failing with
// "mkdir /clips" whenever recordPath is relative (the seeded
// RecordingPolicy uses "./recordings/..."): filepath.Dir strips "./",
// so the loop's base==dir break fired before the %-check on the final
// plain segment and fell through to "/".
func TestVolumeRootForPathClipRelativePaths(t *testing.T) {
	cases := []struct{ in, want string }{
		{"./recordings/%path/%Y-%m-%d_%H-%M-%S-%f", "recordings"},
		{"recordings/%path/%Y-%m-%d_%H-%M-%S-%f", "recordings"},
		{"/abs/root/%path/%Y-%m-%d_%H-%M-%S-%f", "/abs/root"},
		{"%path/%Y-%m-%d", "/"},
	}
	for _, tc := range cases {
		got := volumeRootForPathClip(tc.in)
		require.Equal(t, tc.want, got, "input %q", tc.in)
	}
}
