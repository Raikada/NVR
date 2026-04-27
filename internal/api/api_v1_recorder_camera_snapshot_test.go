package api //nolint:revive

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// snapshotHLSServer extends the hls-muxer fake (defined in
// api_v1_recorder_hls_muxers_test.go) by adding a programmable
// APIMuxerSnapshot. The handler test only needs to drive the
// resolver + transport contract; the in-process gohlslib drive lives
// in the hls package and is exercised by the real server end-to-end.
type snapshotHLSServer struct {
	hlsMuxerOnlyServer
	snapshotByPath map[string]snapshotEntry
}

type snapshotEntry struct {
	data        []byte
	contentType string
	err         error
}

func (s *snapshotHLSServer) APIMuxerSnapshot(name string) ([]byte, string, error) {
	entry, ok := s.snapshotByPath[name]
	if !ok {
		return nil, "", hls.ErrMuxerNotFound
	}
	return entry.data, entry.contentType, entry.err
}

func invokeSnapshotHandler(api *API, idParam string) (*httptest.ResponseRecorder, []byte) {
	return invokeSnapshotHandlerWithQuery(api, idParam, "")
}

func invokeSnapshotHandlerWithQuery(api *API, idParam, rawQuery string) (*httptest.ResponseRecorder, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	target := "/v1/recorder/cameras/" + idParam + "/snapshot"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	api.onV1RecorderCameraSnapshot(c)
	return w, w.Body.Bytes()
}

// TestV1RecorderCameraSnapshotActive: happy path. Camera resolves via a
// configured path, the muxer returns segment bytes, the handler runs
// them through the libav JPEG path. The fragment is a deliberately
// malformed mp4 stub (libav can't decode it), so the handler falls
// back to the raw bytes with X-Snapshot-Format: hls-fragment-fallback.
// We assert the resolver+headers, not the specific body shape — the
// real JPEG-success path is exercised by integration coverage in the
// hls package against a live muxer.
func TestV1RecorderCameraSnapshotActive(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{
				"cam_a": {Path: "cam_a"},
			},
		},
		snapshotByPath: map[string]snapshotEntry{
			"cam_a": {
				data:        []byte("\x00\x00\x00\x18ftypmp42fragment-bytes"),
				contentType: "video/mp4",
			},
		},
	}
	api := &API{
		Conf:      cnf,
		HLSServer: hlsSrv,
		Parent:    &testParent{},
	}

	cameraID := cameraIDFromPathName("cam_a")
	w, body := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	// Malformed fragment — libav can't decode it; handler falls back
	// to raw bytes with the fallback header.
	require.Equal(t, "hls-fragment-fallback", w.Header().Get("X-Snapshot-Format"))
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	require.Equal(t, []byte("\x00\x00\x00\x18ftypmp42fragment-bytes"), body)
}

// TestV1RecorderCameraSnapshotRuntimeActive: gap-#13 parity. A wildcard
// `all_others` config plus a runtime-active muxer that isn't in
// conf.Paths must still resolve via the runtime fallback, mirroring
// onV1RecorderHLSMuxersGet. The fragment is again a malformed stub so
// libav fails fast and the handler returns the raw bytes.
func TestV1RecorderCameraSnapshotRuntimeActive(t *testing.T) {
	cnf := tempConf(t, "paths:\n  all_others:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{
				"cam_runtime": {Path: "cam_runtime"},
			},
		},
		snapshotByPath: map[string]snapshotEntry{
			"cam_runtime": {
				data:        []byte("runtime-fragment"),
				contentType: "video/MP2T",
			},
		},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_runtime")
	w, body := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hls-fragment-fallback", w.Header().Get("X-Snapshot-Format"))
	require.Equal(t, "video/MP2T", w.Header().Get("Content-Type"))
	require.Equal(t, []byte("runtime-fragment"), body)
}

// TestV1RecorderCameraSnapshotInactive: camera resolves to a path but
// the HLS server reports no content (no instance, no segments, etc.).
// Surface as 503 per the snapshot scope decision.
func TestV1RecorderCameraSnapshotInactive(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{
				"cam_a": {Path: "cam_a"},
			},
		},
		snapshotByPath: map[string]snapshotEntry{
			"cam_a": {err: hls.ErrMuxerNoContent},
		},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	w, body := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, string(body), "not currently producing media")
}

// TestV1RecorderCameraSnapshotMuxerMissing: camera UUID resolves to a
// configured path-name, but the HLS server has no muxer for it. Same
// outcome as Inactive: 503, the camera isn't producing media right
// now. (404 is reserved for "the UUID resolves to nothing.")
func TestV1RecorderCameraSnapshotMuxerMissing(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{},
		},
		snapshotByPath: map[string]snapshotEntry{},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	w, _ := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestV1RecorderCameraSnapshotUnknownCamera: a UUID that doesn't match
// any configured path or runtime muxer is 404, distinguishing
// "unknown" from "known but inactive."
func TestV1RecorderCameraSnapshotUnknownCamera(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{muxers: map[string]*defs.APIHLSMuxer{}},
		snapshotByPath:     map[string]snapshotEntry{},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	w, body := invokeSnapshotHandler(api, uuid.New().String())

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, string(body), "camera not found")
}

// TestV1RecorderCameraSnapshotInvalidUUID: malformed id parameter
// short-circuits to 400.
func TestV1RecorderCameraSnapshotInvalidUUID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{muxers: map[string]*defs.APIHLSMuxer{}},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	w, body := invokeSnapshotHandler(api, "not-a-uuid")

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, string(body), "invalid camera id")
}

// TestV1RecorderCameraSnapshotJPEGFallbackOnFailure: a deliberately
// malformed fragment is driven through the real cgo path. libav's
// demuxer fails to open the input; the handler logs and falls back to
// raw fragment bytes with X-Snapshot-Format: hls-fragment-fallback.
// This is the same shape as the active-path tests above; isolated
// here so the failure path is exercised under an explicit name.
func TestV1RecorderCameraSnapshotJPEGFallbackOnFailure(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{"cam_a": {Path: "cam_a"}},
		},
		snapshotByPath: map[string]snapshotEntry{
			"cam_a": {data: []byte("not-a-real-fragment"), contentType: "video/mp4"},
		},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	w, body := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hls-fragment-fallback", w.Header().Get("X-Snapshot-Format"))
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	require.Equal(t, []byte("not-a-real-fragment"), body)
}

// TestV1RecorderCameraSnapshotAtRecorded: `?at=<rfc3339>` resolves
// to the recordstore segment covering the timestamp and runs its
// bytes through the libav decode/encode helper. The on-disk
// segment is a deliberately malformed mp4 stub so libav fails
// fast and the handler returns the raw bytes with the
// hls-fragment-fallback header — same shape as the live-path
// failure tests above. The point is to exercise the recorded-
// snapshot resolution + read path; the JPEG-success branch is
// covered by the same helper as the live path.
func TestV1RecorderCameraSnapshotAtRecorded(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-snapshot-at")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	// recordPath under tempdir; segment files named by unix-seconds
	// (`%s`) so the recordstore parser is timezone-agnostic in
	// tests. Two segments at known unix timestamps; `?at=` between
	// them must resolve to the earlier one.
	cnf := tempConf(t,
		"pathDefaults:\n"+
			"  recordPath: "+filepath.Join(dir, "%path/%s-%f")+"\n"+
			"paths:\n"+
			"  cam_at:\n"+
			"    source: publisher\n",
	)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cam_at"), 0o755))
	// 1717243200 = 2024-06-01T12:00:00Z; 1717243500 = 12:05:00Z.
	seg1 := filepath.Join(dir, "cam_at", "1717243200-000000.mp4")
	require.NoError(t, os.WriteFile(seg1,
		[]byte("\x00\x00\x00\x18ftypmp42recorded-stub-1"), 0o644))
	seg2 := filepath.Join(dir, "cam_at", "1717243500-000000.mp4")
	require.NoError(t, os.WriteFile(seg2,
		[]byte("\x00\x00\x00\x18ftypmp42recorded-stub-2"), 0o644))

	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{},
		},
		snapshotByPath: map[string]snapshotEntry{},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_at")
	at := url.QueryEscape("2024-06-01T12:02:00Z")
	w, body := invokeSnapshotHandlerWithQuery(api, cameraID, "at="+at)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	// Malformed mp4 stub → libav decode fails → fallback branch.
	require.Equal(t, "hls-fragment-fallback", w.Header().Get("X-Snapshot-Format"))
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	// The bytes returned are the contents of seg1 (the segment
	// covering 12:02:00), not seg2.
	require.Equal(t, []byte("\x00\x00\x00\x18ftypmp42recorded-stub-1"), body)
}

// TestV1RecorderCameraSnapshotAtNoCoverage: `?at=` set to a
// timestamp before any recorded segment surfaces 404 — distinct
// from "camera not producing live media" which is 503.
func TestV1RecorderCameraSnapshotAtNoCoverage(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-snapshot-at-nc")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	cnf := tempConf(t,
		"pathDefaults:\n"+
			"  recordPath: "+filepath.Join(dir, "%path/%s-%f")+"\n"+
			"paths:\n"+
			"  cam_at:\n"+
			"    source: publisher\n",
	)

	// Only one segment, at unix 1717243200 (2024-06-01T12:00:00Z).
	// Asking for 2020 must 404.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cam_at"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "cam_at", "1717243200-000000.mp4"),
		[]byte("stub"), 0o644))

	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{muxers: map[string]*defs.APIHLSMuxer{}},
		snapshotByPath:     map[string]snapshotEntry{},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_at")
	at := url.QueryEscape("2020-01-01T00:00:00Z")
	w, body := invokeSnapshotHandlerWithQuery(api, cameraID, "at="+at)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, string(body), "no recorded segment covers")
}

// TestV1RecorderCameraSnapshotAtMalformed: `?at=` that doesn't
// parse as RFC3339 surfaces 400 with a clear message; the live
// path is not consulted.
func TestV1RecorderCameraSnapshotAtMalformed(t *testing.T) {
	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{"cam_a": {Path: "cam_a"}},
		},
		snapshotByPath: map[string]snapshotEntry{},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	w, body := invokeSnapshotHandlerWithQuery(api, cameraID, "at=not-a-time")

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, string(body), "invalid 'at' parameter")
}
