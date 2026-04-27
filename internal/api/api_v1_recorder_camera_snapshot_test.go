package api //nolint:revive

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// withNoFFmpeg overrides the snapshot ffmpeg lookup to report "no
// ffmpeg available" for the duration of a test, regardless of the
// host's actual PATH. Existing snapshot tests assert raw-fragment
// fallback behavior, so they pin the no-ffmpeg branch deterministically.
func withNoFFmpeg(t *testing.T) {
	t.Helper()
	prevLookup := snapshotFFmpegLookup
	prevOnce := snapshotFFmpegOnce
	prevPath := snapshotFFmpegPath
	snapshotFFmpegLookup = func() (string, error) { return "", exec.ErrNotFound }
	snapshotFFmpegOnce = sync.Once{}
	snapshotFFmpegPath = ""
	t.Cleanup(func() {
		snapshotFFmpegLookup = prevLookup
		snapshotFFmpegOnce = prevOnce
		snapshotFFmpegPath = prevPath
	})
}

// withFFmpegFromLookup injects a custom lookup function and resets the
// cache. Used by JPEG-path tests that want to simulate ffmpeg present
// at a specific path (a temp script that fakes ffmpeg behavior, or a
// path that's intentionally broken to test conversion-failure
// fallback).
func withFFmpegFromLookup(t *testing.T, lookup func() (string, error)) {
	t.Helper()
	prevLookup := snapshotFFmpegLookup
	prevOnce := snapshotFFmpegOnce
	prevPath := snapshotFFmpegPath
	snapshotFFmpegLookup = lookup
	snapshotFFmpegOnce = sync.Once{}
	snapshotFFmpegPath = ""
	t.Cleanup(func() {
		snapshotFFmpegLookup = prevLookup
		snapshotFFmpegOnce = prevOnce
		snapshotFFmpegPath = prevPath
	})
}

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
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	target := "/v1/recorder/cameras/" + idParam + "/snapshot"
	req := httptest.NewRequest(http.MethodGet, target, nil)
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	api.onV1RecorderCameraSnapshot(c)
	return w, w.Body.Bytes()
}

// TestV1RecorderCameraSnapshotActive: happy path. Camera resolves via a
// configured path, the muxer returns segment bytes, the handler streams
// them through with the muxer's content type and a no-store cache
// header.
func TestV1RecorderCameraSnapshotActive(t *testing.T) {
	withNoFFmpeg(t)
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
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, []byte("\x00\x00\x00\x18ftypmp42fragment-bytes"), body)
}

// TestV1RecorderCameraSnapshotRuntimeActive: gap-#13 parity. A wildcard
// `all_others` config plus a runtime-active muxer that isn't in
// conf.Paths must still resolve via the runtime fallback, mirroring
// onV1RecorderHLSMuxersGet.
func TestV1RecorderCameraSnapshotRuntimeActive(t *testing.T) {
	withNoFFmpeg(t)
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

// TestV1RecorderCameraSnapshotJPEGFallbackOnFailure: ffmpeg is "on
// PATH" (lookup succeeds) but the binary at that path fails to run.
// The handler must log a warning and fall back to raw fragment bytes
// with X-Snapshot-Format: hls-fragment-fallback.
func TestV1RecorderCameraSnapshotJPEGFallbackOnFailure(t *testing.T) {
	// Point the lookup at an absurd path that exists but isn't ffmpeg.
	// /usr/bin/false exits 1 immediately; ffmpeg-style flags will
	// surface as a non-zero exit error from cmd.Output().
	withFFmpegFromLookup(t, func() (string, error) {
		return "/usr/bin/false", nil
	})

	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{"cam_a": {Path: "cam_a"}},
		},
		snapshotByPath: map[string]snapshotEntry{
			"cam_a": {data: []byte("\x00\x00\x00\x18ftypmp42fragment-bytes"), contentType: "video/mp4"},
		},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	w, body := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hls-fragment-fallback", w.Header().Get("X-Snapshot-Format"))
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	require.Equal(t, []byte("\x00\x00\x00\x18ftypmp42fragment-bytes"), body)
}

// TestV1RecorderCameraSnapshotJPEGNoFFmpeg: when ffmpeg is not on PATH
// at all, the response carries X-Snapshot-Format: hls-fragment (no
// "fallback" suffix — the no-conversion-attempt branch is signaled
// distinctly from "ffmpeg present but failed").
func TestV1RecorderCameraSnapshotJPEGNoFFmpeg(t *testing.T) {
	withNoFFmpeg(t)

	cnf := tempConf(t, "paths:\n  cam_a:\n    source: publisher\n")
	hlsSrv := &snapshotHLSServer{
		hlsMuxerOnlyServer: hlsMuxerOnlyServer{
			muxers: map[string]*defs.APIHLSMuxer{"cam_a": {Path: "cam_a"}},
		},
		snapshotByPath: map[string]snapshotEntry{
			"cam_a": {data: []byte("\x00\x00\x00\x18ftypmp42fragment-bytes"), contentType: "video/mp4"},
		},
	}
	api := &API{Conf: cnf, HLSServer: hlsSrv, Parent: &testParent{}}

	cameraID := cameraIDFromPathName("cam_a")
	w, _ := invokeSnapshotHandler(api, cameraID)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hls-fragment", w.Header().Get("X-Snapshot-Format"))
}
