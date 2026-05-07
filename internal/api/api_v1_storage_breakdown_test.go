package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/recordingmeta"
)

// newV1BreakdownServer mounts the breakdown endpoint plus the
// underlying recording-segments route used to discover the synthesized
// segment population. Mirrors newV1Server but stripped down.
func newV1BreakdownServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/storage-volumes/breakdown", a.onV1StorageVolumesBreakdown)
	r.GET("/v1/recording-segments", a.onV1RecordingSegmentsList)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// TestContentTypeFromPolicyMode verifies the pure mapping. Locks in
// each of the five RecordingPolicyMode values.
func TestContentTypeFromPolicyMode(t *testing.T) {
	for _, c := range []struct {
		in   defs.RecordingPolicyMode
		want defs.RecordingSegmentContentType
	}{
		{defs.RecordingPolicyModeContinuous, defs.RecordingSegmentContentTypeContinuous},
		{defs.RecordingPolicyModeMotion, defs.RecordingSegmentContentTypeMotion},
		{defs.RecordingPolicyModeSchedule, defs.RecordingSegmentContentTypeScheduled},
		{defs.RecordingPolicyModeEventTriggered, defs.RecordingSegmentContentTypeEventTriggered},
		{defs.RecordingPolicyModeOff, defs.RecordingSegmentContentTypeOff},
		// Unknown / empty defaults to continuous per platform amendment.
		{defs.RecordingPolicyMode(""), defs.RecordingSegmentContentTypeContinuous},
		{defs.RecordingPolicyMode("garbage"), defs.RecordingSegmentContentTypeContinuous},
	} {
		got := defs.ContentTypeFromPolicyMode(c.in)
		require.Equal(t, c.want, got, "mode=%q", c.in)
	}
}

// TestSegmentFromRecordstoreFileEmptyContentTypeDefaultsContinuous
// locks in the platform amendment language: pre-amendment segments
// (which never carry content_type) surface as continuous.
func TestSegmentFromRecordstoreFileEmptyContentTypeDefaultsContinuous(t *testing.T) {
	seg := defs.SegmentFromRecordstoreFile(
		defs.RecordstoreSegmentInput{Path: "/tmp/x.mp4"},
		"00000000-0000-0000-0000-000000000001",
		"tenant-a",
		"site-a",
		"camera-a",
		"server-a",
		"vol-a",
		"policy-a",
		"rec-a",
		"", // content_type — empty
	)
	require.Equal(t, defs.RecordingSegmentContentTypeContinuous, seg.ContentType)
}

// TestSegmentSynthesisStampsContentTypeFromPolicy verifies that
// /v1/recording-segments surfaces content_type stamped from the
// policy.mode of the segment's owning camera. Two cameras with
// distinct policies → distinct content_types.
func TestSegmentSynthesisStampsContentTypeFromPolicy(t *testing.T) {
	dir, err := os.MkdirTemp("", "wave5-synthesis")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Build a config with two policies (continuous + motion) and two
	// cameras (each on a distinct policy). Use the seeded
	// DefaultRecordingPolicyID for the continuous one so we exercise
	// both the "explicit policy" and "default policy" lookup paths.
	yml := "pathDefaults:\n" +
		"  recordPath: " + filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"recordingPolicies:\n" +
		"  " + conf.DefaultRecordingPolicyID + ":\n" +
		"    name: Default\n" +
		"    mode: continuous\n" +
		"    container: fmp4\n" +
		"    enabled: true\n" +
		"    retentionDuration: 24h\n" +
		"    minSegmentDuration: 1m\n" +
		"    maxSegmentDuration: 10m\n" +
		"    partDuration: 1s\n" +
		"    maxPartSize: 50000000\n" +
		"  11111111-1111-1111-1111-111111111111:\n" +
		"    name: MotionOnly\n" +
		"    mode: motion\n" +
		"    container: fmp4\n" +
		"    enabled: true\n" +
		"    retentionDuration: 24h\n" +
		"    minSegmentDuration: 1m\n" +
		"    maxSegmentDuration: 10m\n" +
		"    partDuration: 1s\n" +
		"    maxPartSize: 50000000\n" +
		"paths:\n" +
		"  cam_continuous:\n" +
		"  cam_motion:\n" +
		"    recordingPolicyId: 11111111-1111-1111-1111-111111111111\n"

	cnf := tempConf(t, yml)
	a := newTestAPIWithConf(t, cnf)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam_continuous", "2026-01-01_10-00-00-000000")
	writeSegmentFile(t, dir, "cam_motion", "2026-01-01_10-00-00-000000")

	resp, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Items []struct {
			CameraID    string `json:"camera_id"`
			ContentType string `json:"content_type"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 2)

	byCam := map[string]string{}
	for _, it := range got.Items {
		byCam[it.CameraID] = it.ContentType
	}
	require.Equal(t, "continuous", byCam[cameraIDFromPathName("cam_continuous")])
	require.Equal(t, "motion", byCam[cameraIDFromPathName("cam_motion")])
}

// TestStorageVolumesBreakdownEmpty verifies the rollup endpoint with
// no segments returns zero totals and empty groupings (well-formed).
func TestStorageVolumesBreakdownEmpty(t *testing.T) {
	dir, err := os.MkdirTemp("", "wave5-breakdown-empty")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1BreakdownServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes/breakdown")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got storageBreakdownResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, int64(0), got.TotalBytes)
	require.Equal(t, 0, got.SegmentCount)
	require.Empty(t, got.ByContentType)
	require.Empty(t, got.ByCamera)
	require.Empty(t, got.ByVolume)
}

// TestStorageVolumesBreakdownRollupMath verifies the rollup correctly
// sums byte_size + segment_count by content_type, by_camera, and
// by_volume across a fixture of segments with two distinct
// content_types.
func TestStorageVolumesBreakdownRollupMath(t *testing.T) {
	dir, err := os.MkdirTemp("", "wave5-breakdown-math")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	yml := "pathDefaults:\n" +
		"  recordPath: " + filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"recordingPolicies:\n" +
		"  " + conf.DefaultRecordingPolicyID + ":\n" +
		"    name: Default\n" +
		"    mode: continuous\n" +
		"    container: fmp4\n" +
		"    enabled: true\n" +
		"    retentionDuration: 24h\n" +
		"    minSegmentDuration: 1m\n" +
		"    maxSegmentDuration: 10m\n" +
		"    partDuration: 1s\n" +
		"    maxPartSize: 50000000\n" +
		"  11111111-1111-1111-1111-111111111111:\n" +
		"    name: MotionOnly\n" +
		"    mode: motion\n" +
		"    container: fmp4\n" +
		"    enabled: true\n" +
		"    retentionDuration: 24h\n" +
		"    minSegmentDuration: 1m\n" +
		"    maxSegmentDuration: 10m\n" +
		"    partDuration: 1s\n" +
		"    maxPartSize: 50000000\n" +
		"paths:\n" +
		"  cam_a:\n" +
		"  cam_b:\n" +
		"    recordingPolicyId: 11111111-1111-1111-1111-111111111111\n"

	cnf := tempConf(t, yml)
	a := newTestAPIWithConf(t, cnf)
	srv := newV1BreakdownServer(t, a)

	// cam_a (continuous): two segments with distinct timestamps so they
	// surface as one Recording each (well outside the 60s gap window),
	// each writing a fixed payload.
	pathA1 := writeSegmentFile(t, dir, "cam_a", "2026-01-01_10-00-00-000000")
	pathA2 := writeSegmentFile(t, dir, "cam_a", "2026-01-01_11-00-00-000000")
	// cam_b (motion): one segment.
	pathB1 := writeSegmentFile(t, dir, "cam_b", "2026-01-01_12-00-00-000000")

	// Overwrite each with a known-size payload so byte_size math is
	// deterministic.
	require.NoError(t, os.WriteFile(pathA1, make([]byte, 100), 0o644))
	require.NoError(t, os.WriteFile(pathA2, make([]byte, 200), 0o644))
	require.NoError(t, os.WriteFile(pathB1, make([]byte, 50), 0o644))

	resp, err := http.Get(srv.URL + "/v1/storage-volumes/breakdown")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got storageBreakdownResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	// Three segments total, 350 bytes total.
	require.Equal(t, 3, got.SegmentCount)
	require.Equal(t, int64(350), got.TotalBytes)

	// by_content_type: continuous has 2 segs/300 bytes; motion has 1 seg/50 bytes.
	byCT := map[string]breakdownEntry{}
	for _, e := range got.ByContentType {
		byCT[e.ContentType] = e
	}
	require.Len(t, got.ByContentType, 2)
	require.Equal(t, breakdownEntry{ContentType: "continuous", ByteSize: 300, SegmentCount: 2}, byCT["continuous"])
	require.Equal(t, breakdownEntry{ContentType: "motion", ByteSize: 50, SegmentCount: 1}, byCT["motion"])

	// by_camera: cam_a has continuous=300/2; cam_b has motion=50/1.
	byCam := map[string]breakdownByCamera{}
	for _, e := range got.ByCamera {
		byCam[e.CameraID] = e
	}
	require.Len(t, got.ByCamera, 2)

	camA := byCam[cameraIDFromPathName("cam_a")]
	require.Equal(t, int64(300), camA.ByteSize)
	require.Equal(t, 2, camA.SegmentCount)
	require.Len(t, camA.ByContentType, 1)
	require.Equal(t, "continuous", camA.ByContentType[0].ContentType)

	camB := byCam[cameraIDFromPathName("cam_b")]
	require.Equal(t, int64(50), camB.ByteSize)
	require.Equal(t, 1, camB.SegmentCount)
	require.Len(t, camB.ByContentType, 1)
	require.Equal(t, "motion", camB.ByContentType[0].ContentType)

	// by_volume: pre-Wave-5 the recorder doesn't stamp volume_id on
	// segments (D8 follow-up), so the rollup lands under volume_id="".
	// Verify the wire shape is well-formed regardless.
	require.NotEmpty(t, got.ByVolume, "by_volume should at least contain the unstamped bucket")
	for _, v := range got.ByVolume {
		require.NotEmpty(t, v.ByContentType)
	}
}

// TestSegmentSynthesisPrefersSidecarOverCurrentPolicy verifies the
// Wave A3 fidelity: if a sidecar is present next to the segment file,
// the synthesizer reads it instead of falling back to the current
// policy mode. Models the "policy was motion at write time, then
// the operator switched to continuous before the operator looked at
// the breakdown" scenario.
func TestSegmentSynthesisPrefersSidecarOverCurrentPolicy(t *testing.T) {
	dir, err := os.MkdirTemp("", "wavea3-sidecar")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Conf says the camera is on the default (continuous) policy now.
	yml := "pathDefaults:\n" +
		"  recordPath: " + filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"recordingPolicies:\n" +
		"  " + conf.DefaultRecordingPolicyID + ":\n" +
		"    name: Default\n" +
		"    mode: continuous\n" +
		"    container: fmp4\n" +
		"    enabled: true\n" +
		"    retentionDuration: 24h\n" +
		"    minSegmentDuration: 1m\n" +
		"    maxSegmentDuration: 10m\n" +
		"    partDuration: 1s\n" +
		"    maxPartSize: 50000000\n" +
		"paths:\n" +
		"  cam_a:\n"

	cnf := tempConf(t, yml)
	a := newTestAPIWithConf(t, cnf)
	srv := newV1Server(t, a)

	segPath := writeSegmentFile(t, dir, "cam_a", "2026-01-01_10-00-00-000000")

	// Write a sidecar that disagrees with the current policy: the
	// segment was sealed under a motion policy. The synthesizer must
	// surface motion, not continuous.
	body := []byte(`{"policy_id":"22222222-2222-2222-2222-222222222222","mode":"motion"}`)
	scPath := recordingmeta.SidecarPath(segPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(scPath), 0o755))
	require.NoError(t, os.WriteFile(scPath, body, 0o644))

	resp, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Items []struct {
			ContentType string `json:"content_type"`
			PolicyID    string `json:"policy_id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 1)
	require.Equal(t, "motion", got.Items[0].ContentType)
	require.Equal(t, "22222222-2222-2222-2222-222222222222", got.Items[0].PolicyID)
}

// TestSegmentSynthesisFallsBackWhenNoSidecar locks in the
// pre-amendment / pre-Wave-A3 behavior: a segment without a sidecar
// surfaces as the current policy's mode (matches Wave 5).
func TestSegmentSynthesisFallsBackWhenNoSidecar(t *testing.T) {
	dir, err := os.MkdirTemp("", "wavea3-fallback")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	yml := "pathDefaults:\n" +
		"  recordPath: " + filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"recordingPolicies:\n" +
		"  " + conf.DefaultRecordingPolicyID + ":\n" +
		"    name: Default\n" +
		"    mode: motion\n" +
		"    container: fmp4\n" +
		"    enabled: true\n" +
		"    retentionDuration: 24h\n" +
		"    minSegmentDuration: 1m\n" +
		"    maxSegmentDuration: 10m\n" +
		"    partDuration: 1s\n" +
		"    maxPartSize: 50000000\n" +
		"paths:\n" +
		"  cam_a:\n"

	cnf := tempConf(t, yml)
	a := newTestAPIWithConf(t, cnf)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam_a", "2026-01-01_10-00-00-000000")
	// No sidecar.

	resp, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Items []struct {
			ContentType string `json:"content_type"`
			PolicyID    string `json:"policy_id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 1)
	require.Equal(t, "motion", got.Items[0].ContentType)
	// No sidecar → policy_id stays empty (recorder doesn't yet stamp
	// it from the fallback resolver — only the sidecar carries
	// historical truth).
	require.Empty(t, got.Items[0].PolicyID)
}

// newTestAPIWithConf is a sister helper to newTestAPI, but takes a
// pre-built Conf so the test can set up RecordingPolicies + per-camera
// path config.
func newTestAPIWithConf(t *testing.T, cnf *conf.Conf) *API {
	t.Helper()
	a := newTestAPI(t, t.TempDir())
	a.Conf = cnf
	return a
}
