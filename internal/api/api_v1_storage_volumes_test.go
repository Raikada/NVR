package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

func newV1StorageVolumesServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/storage-volumes", a.onV1StorageVolumesList)
	r.GET("/v1/storage-volumes/:id", a.onV1StorageVolumesGet)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestVolumeRootForRecordPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"./recordings/%path/%Y-%m-%d_%H", "./recordings"},
		{"/var/lib/recorder/%path/%s.mp4", "/var/lib/recorder"},
		{"/mnt/disk1/cam-%path/%Y-%m-%d/%H.mp4", "/mnt/disk1"},
		{"", ""},
		{"/data/%path", "/data"},
		{"%path/foo", ""}, // first segment is a token → no static prefix
	} {
		require.Equal(t, c.want, volumeRootForRecordPath(c.in), "in=%q", c.in)
	}
}

func TestVolumeIDFromMountPathDeterministic(t *testing.T) {
	a := volumeIDFromMountPath("/mnt/disk1")
	b := volumeIDFromMountPath("/mnt/disk1")
	require.Equal(t, a, b, "UUID must be deterministic for the same mount path")
	_, err := uuid.Parse(a)
	require.NoError(t, err)

	c := volumeIDFromMountPath("/mnt/disk2")
	require.NotEqual(t, a, c, "different mount paths → different UUIDs")
}

func TestVolumeKindForMountPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want defs.StorageVolumeKind
	}{
		{"/tmp/foo", defs.StorageVolumeKindRamdisk},
		{"/dev/shm/x", defs.StorageVolumeKindRamdisk},
		{"/run/y", defs.StorageVolumeKindRamdisk},
		{"/mnt/nfs/share", defs.StorageVolumeKindNetworkShare},
		{"/net/share", defs.StorageVolumeKindNetworkShare},
		{"//host/share", defs.StorageVolumeKindNetworkShare},
		{"/mnt/disk1", defs.StorageVolumeKindInternalDisk},
		{"/var/lib/recorder", defs.StorageVolumeKindInternalDisk},
	} {
		require.Equal(t, c.want, volumeKindForMountPath(c.in), "in=%q", c.in)
	}
}

func TestStatfsVolumeOnRealDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "phase2d-statfs")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	cap, used, ok := statfsVolume(dir)
	require.True(t, ok)
	require.Greater(t, cap, int64(0))
	require.GreaterOrEqual(t, used, int64(0))
	require.LessOrEqual(t, used, cap)
}

func TestStatfsVolumeMissing(t *testing.T) {
	_, _, ok := statfsVolume("/this-path-definitely-does-not-exist-1234567890")
	require.False(t, ok)
}

func TestV1StorageVolumesListEmpty(t *testing.T) {
	a := &API{Conf: nil}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got storageVolumeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 0, got.ItemCount)
}

func TestV1StorageVolumesListSynthesizes(t *testing.T) {
	dir, err := os.MkdirTemp("", "phase2d-volumes")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	cnf := tempConf(t, "pathDefaults:\n"+
		"  recordPath: "+filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")+"\n"+
		"paths:\n"+
		"  cam1:\n"+
		"  cam2:\n")

	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got storageVolumeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	// Both cam1 and cam2 share the dir as their volume root, so the
	// list collapses to one volume.
	require.Equal(t, 1, got.ItemCount)
	require.Len(t, got.Items, 1)

	v := got.Items[0]
	_, perr := uuid.Parse(v.ID)
	require.NoError(t, perr)

	abs, _ := filepath.Abs(dir)
	require.Equal(t, abs, v.MountPath)
	require.Greater(t, v.CapacityBytes, int64(0), "real fs must report non-zero capacity")

	// Status should be one of the canonical values.
	switch v.Status {
	case defs.StorageVolumeStatusHealthy,
		defs.StorageVolumeStatusDegraded,
		defs.StorageVolumeStatusFull,
		defs.StorageVolumeStatusReadOnly,
		defs.StorageVolumeStatusMissing:
	default:
		t.Fatalf("invalid status %q", v.Status)
	}
}

func TestV1StorageVolumesGetRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "phase2d-volumes-get")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	cnf := tempConf(t, "pathDefaults:\n"+
		"  recordPath: "+filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f")+"\n"+
		"paths:\n"+
		"  cam1:\n")

	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	// First list to get the id.
	resp, err := http.Get(srv.URL + "/v1/storage-volumes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var listGot storageVolumeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listGot))
	require.Len(t, listGot.Items, 1)
	id := listGot.Items[0].ID

	// Now Get by id.
	resp2, err := http.Get(srv.URL + "/v1/storage-volumes/" + id)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var single defs.StorageVolume
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&single))
	require.Equal(t, id, single.ID)
	require.Equal(t, listGot.Items[0].MountPath, single.MountPath)
}

func TestV1StorageVolumesGetNotFound(t *testing.T) {
	cnf := tempConf(t, "")
	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes/" + uuid.New().String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestV1StorageVolumesPriorityFallback locks in the legacy behavior:
// with no recordingVolumes overrides configured, /v1/storage-volumes
// reports priorities equal to the deterministic mount-path-sort
// index. ADR 0009 §D5 follow-up.
func TestV1StorageVolumesPriorityFallback(t *testing.T) {
	dir1, err := os.MkdirTemp("", "volprio-fb-1")
	require.NoError(t, err)
	defer os.RemoveAll(dir1)
	dir2, err := os.MkdirTemp("", "volprio-fb-2")
	require.NoError(t, err)
	defer os.RemoveAll(dir2)

	cnf := tempConf(t, "paths:\n"+
		"  cam1:\n"+
		"    recordPath: "+filepath.Join(dir1, "%path/%Y-%m-%d_%H-%M-%S-%f")+"\n"+
		"  cam2:\n"+
		"    recordPath: "+filepath.Join(dir2, "%path/%Y-%m-%d_%H-%M-%S-%f")+"\n")

	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got storageVolumeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 2)

	// Items are sorted by mount path; priorities should be 0, 1.
	require.Equal(t, 0, got.Items[0].Priority)
	require.Equal(t, 1, got.Items[1].Priority)
}

// TestV1StorageVolumesPriorityOverride exercises the operator-set
// override path: with recordingVolumes entries for both volumes,
// /v1/storage-volumes reflects the configured priorities verbatim.
func TestV1StorageVolumesPriorityOverride(t *testing.T) {
	dir1, err := os.MkdirTemp("", "volprio-ov-1")
	require.NoError(t, err)
	defer os.RemoveAll(dir1)
	dir2, err := os.MkdirTemp("", "volprio-ov-2")
	require.NoError(t, err)
	defer os.RemoveAll(dir2)

	abs1, _ := filepath.Abs(dir1)
	abs2, _ := filepath.Abs(dir2)

	yml := "paths:\n" +
		"  cam1:\n" +
		"    recordPath: " + filepath.Join(dir1, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"  cam2:\n" +
		"    recordPath: " + filepath.Join(dir2, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"recordingVolumes:\n" +
		"  " + abs1 + ":\n" +
		"    priority: 100\n" +
		"  " + abs2 + ":\n" +
		"    priority: 50\n"
	cnf := tempConf(t, yml)

	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got storageVolumeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 2)

	byMount := map[string]int{}
	for _, v := range got.Items {
		byMount[v.MountPath] = v.Priority
	}
	require.Equal(t, 100, byMount[abs1])
	require.Equal(t, 50, byMount[abs2])
}

// TestV1StorageVolumesPriorityMixed verifies the per-volume
// fallback: when only some volumes have explicit overrides, the
// configured ones use their override and the others retain the
// sort-index default.
func TestV1StorageVolumesPriorityMixed(t *testing.T) {
	dir1, err := os.MkdirTemp("", "volprio-mx-1")
	require.NoError(t, err)
	defer os.RemoveAll(dir1)
	dir2, err := os.MkdirTemp("", "volprio-mx-2")
	require.NoError(t, err)
	defer os.RemoveAll(dir2)

	abs1, _ := filepath.Abs(dir1)
	abs2, _ := filepath.Abs(dir2)

	yml := "paths:\n" +
		"  cam1:\n" +
		"    recordPath: " + filepath.Join(dir1, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"  cam2:\n" +
		"    recordPath: " + filepath.Join(dir2, "%path/%Y-%m-%d_%H-%M-%S-%f") + "\n" +
		"recordingVolumes:\n" +
		"  " + abs2 + ":\n" +
		"    priority: 7\n"
	cnf := tempConf(t, yml)

	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got storageVolumeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Items, 2)

	byMount := map[string]int{}
	for _, v := range got.Items {
		byMount[v.MountPath] = v.Priority
	}
	// Items are deterministically sorted by mount path. abs1 is the
	// volume without an override, so it gets its sort-index priority
	// (which depends on lexicographic order of abs1 vs abs2). Compute
	// the expected sort index here so the test stays robust against
	// tempdir name variation.
	expectedFallback := 0
	if abs1 > abs2 {
		expectedFallback = 1
	}
	require.Equal(t, expectedFallback, byMount[abs1])
	require.Equal(t, 7, byMount[abs2])
}

func TestV1StorageVolumesGetBadID(t *testing.T) {
	cnf := tempConf(t, "")
	a := &API{Conf: cnf}
	srv := newV1StorageVolumesServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/storage-volumes/not-a-uuid")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
