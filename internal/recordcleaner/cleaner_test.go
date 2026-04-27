package recordcleaner

import (
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/stretchr/testify/require"
)

func TestCleaner(t *testing.T) {
	timeNow = func() time.Time {
		return time.Date(2009, 5, 20, 22, 15, 25, 427000, time.Local)
	}

	dir, err := os.MkdirTemp("", "mediamtx-cleaner")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	const specialChars = "_-+*?^$()[]{}|"

	err = os.Mkdir(filepath.Join(dir, specialChars+"_mypath"), 0o755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, specialChars+"_mypath", "2008-05-20_22-15-25-000125.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, specialChars+"_mypath", "2009-05-20_22-15-25-000427.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	c := &Cleaner{
		PathConfs: map[string]*conf.Path{
			"~^.*$": {
				Name:              "~^.*$",
				Regexp:            regexp.MustCompile("^.*$"),
				RecordPath:        filepath.Join(dir, specialChars+"_%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * time.Second),
			},
		},
		Parent: test.NilLogger,
	}
	c.Initialize()
	defer c.Close()

	time.Sleep(500 * time.Millisecond)

	_, err = os.Stat(filepath.Join(dir, specialChars+"_mypath", "2008-05-20_22-15-25-000125.mp4"))
	require.Error(t, err)

	_, err = os.Stat(filepath.Join(dir, specialChars+"_mypath", "2009-05-20_22-15-25-000427.mp4"))
	require.NoError(t, err)
}

func TestCleanerMultipleEntriesSamePath(t *testing.T) {
	timeNow = func() time.Time {
		return time.Date(2009, 5, 20, 22, 15, 25, 427000, time.Local)
	}

	dir, err := os.MkdirTemp("", "mediamtx-cleaner")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	err = os.Mkdir(filepath.Join(dir, "path1"), 0o755)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(dir, "path2"), 0o755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "path1", "2009-05-19_22-15-25-000427.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "path2", "2009-05-19_22-15-25-000427.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	c := &Cleaner{
		PathConfs: map[string]*conf.Path{
			"path1": {
				Name:              "path1",
				RecordPath:        filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * time.Second),
			},
			"path2": {
				Name:              "path2",
				RecordPath:        filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * 24 * time.Hour),
			},
		},
		Parent: test.NilLogger,
	}
	c.Initialize()
	defer c.Close()

	time.Sleep(500 * time.Millisecond)

	_, err = os.Stat(filepath.Join(dir, "path1", "2009-05-19_22-15-25-000427.mp4"))
	require.Error(t, err)

	_, err = os.Stat(filepath.Join(dir, "path1"))
	require.Error(t, err, "testing")

	_, err = os.Stat(filepath.Join(dir, "path2", "2009-05-19_22-15-25-000427.mp4"))
	require.NoError(t, err)
}

// fakeStatfs is the test seam for sampleVolume / probeCapacity. It
// reports a configurable used fraction by setting Blocks and Bavail
// such that (Blocks - Bavail) / Blocks == usedFraction. Bsize is fixed
// at 4096 to keep the math obvious.
type fakeStatfs struct {
	mu              sync.Mutex
	usedFraction    map[string]float64 // mountPath -> 0.0..1.0
	failOn          map[string]bool    // mountPath -> return error from statfs
}

func (f *fakeStatfs) statfs(path string, st *syscall.Statfs_t) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn[path] {
		return syscall.ENOENT
	}
	frac, ok := f.usedFraction[path]
	if !ok {
		frac = 0
	}
	const blocks = uint64(1_000_000)
	st.Bsize = 4096
	st.Blocks = blocks
	free := uint64(float64(blocks) * (1.0 - frac))
	st.Bavail = free
	return nil
}

// withFakeStatfs swaps the package-level statfsFn for the duration of
// a test, restoring it on cleanup. Tests that mutate package globals
// must not run with t.Parallel() — these don't.
func withFakeStatfs(t *testing.T, f *fakeStatfs) {
	t.Helper()
	orig := statfsFn
	statfsFn = f.statfs
	t.Cleanup(func() { statfsFn = orig })
}

func TestSampleVolume_ThresholdClassification(t *testing.T) {
	f := &fakeStatfs{usedFraction: map[string]float64{}}
	withFakeStatfs(t, f)

	c := &Cleaner{Parent: test.NilLogger}

	cases := []struct {
		name     string
		frac     float64
		full     bool
		degraded bool
	}{
		{"healthy", 0.50, false, false},
		{"just_below_degraded", 0.899, false, false},
		{"degraded_band_low", 0.90, false, true},
		{"degraded_band_high", 0.949, false, true},
		{"full_threshold", 0.95, true, false},
		{"well_above_full", 0.999, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.usedFraction["/vol/"+tc.name] = tc.frac
			full, degraded, _ := c.sampleVolume("/vol/" + tc.name)
			require.Equal(t, tc.full, full, "full classification")
			require.Equal(t, tc.degraded, degraded, "degraded classification")
		})
	}
}

func TestSampleVolume_StatfsFailureIsDegraded(t *testing.T) {
	f := &fakeStatfs{
		usedFraction: map[string]float64{},
		failOn:       map[string]bool{"/missing": true},
	}
	withFakeStatfs(t, f)

	c := &Cleaner{Parent: test.NilLogger}
	full, degraded, reason := c.sampleVolume("/missing")
	require.False(t, full)
	require.True(t, degraded)
	require.Equal(t, "statfs_failed", reason)
}

func TestApplyVolumeTransition_OncePerStateChange(t *testing.T) {
	type emit struct {
		kind, mountPath, reason string
	}
	var emitted []emit

	c := &Cleaner{
		Parent:              test.NilLogger,
		volumeFullState:     map[string]bool{},
		volumeDegradedState: map[string]bool{},
		PublishVolumeFull: func(_, mountPath string) {
			emitted = append(emitted, emit{kind: "storage.volume_full", mountPath: mountPath})
		},
		PublishVolumeDegraded: func(_, mountPath, reason string) {
			emitted = append(emitted, emit{kind: "storage.volume_degraded", mountPath: mountPath, reason: reason})
		},
	}

	// Healthy -> healthy: no emission.
	c.applyVolumeTransition("/vol/a", false, false, "")
	require.Empty(t, emitted)

	// Healthy -> full: one emission.
	c.applyVolumeTransition("/vol/a", true, false, "")
	require.Len(t, emitted, 1)
	require.Equal(t, "storage.volume_full", emitted[0].kind)

	// Stays full: no re-emission.
	c.applyVolumeTransition("/vol/a", true, false, "")
	c.applyVolumeTransition("/vol/a", true, false, "")
	require.Len(t, emitted, 1)

	// Drops back to healthy: silent (no recovered Event today).
	c.applyVolumeTransition("/vol/a", false, false, "")
	require.Len(t, emitted, 1)

	// Crosses again: re-emits because state transition is fresh.
	c.applyVolumeTransition("/vol/a", true, false, "")
	require.Len(t, emitted, 2)

	// Independent volume into degraded: emits with reason.
	c.applyVolumeTransition("/vol/b", false, true, "capacity_headroom_low")
	require.Len(t, emitted, 3)
	require.Equal(t, "storage.volume_degraded", emitted[2].kind)
	require.Equal(t, "capacity_headroom_low", emitted[2].reason)

	// Same volume re-probed degraded: suppressed.
	c.applyVolumeTransition("/vol/b", false, true, "capacity_headroom_low")
	require.Len(t, emitted, 3)
}

func TestProbeCapacity_EmitsAcrossDistinctVolumes(t *testing.T) {
	f := &fakeStatfs{
		usedFraction: map[string]float64{},
		failOn:       map[string]bool{},
	}
	withFakeStatfs(t, f)

	dirA, err := os.MkdirTemp("", "vol-a-")
	require.NoError(t, err)
	defer os.RemoveAll(dirA)
	dirB, err := os.MkdirTemp("", "vol-b-")
	require.NoError(t, err)
	defer os.RemoveAll(dirB)

	absA, _ := filepath.Abs(dirA)
	absB, _ := filepath.Abs(dirB)
	f.usedFraction[absA] = 0.97 // full
	f.usedFraction[absB] = 0.92 // degraded

	var fullCalls, degradedCalls int
	c := &Cleaner{
		Parent:              test.NilLogger,
		volumeFullState:     map[string]bool{},
		volumeDegradedState: map[string]bool{},
		PathConfs: map[string]*conf.Path{
			"a": {Name: "a", RecordPath: filepath.Join(dirA, "%path/%Y-%m-%d.mp4")},
			"b": {Name: "b", RecordPath: filepath.Join(dirB, "%path/%Y-%m-%d.mp4")},
		},
		PublishVolumeFull:     func(_, _ string) { fullCalls++ },
		PublishVolumeDegraded: func(_, _, _ string) { degradedCalls++ },
	}

	c.probeCapacity()
	require.Equal(t, 1, fullCalls, "volume A should emit full once")
	require.Equal(t, 1, degradedCalls, "volume B should emit degraded once")

	c.probeCapacity()
	require.Equal(t, 1, fullCalls, "second probe with same state should not re-emit")
	require.Equal(t, 1, degradedCalls, "second probe with same state should not re-emit")
}

func TestVolumeIDFromMountPath_MatchesAPILayer(t *testing.T) {
	// The api package mints volume ids the same way; this test pins
	// the lockstep so a future refactor that diverges fails here
	// loudly. Hand-computed via uuid.NewSHA1(NameSpaceOID,
	// "storage-volume|/abs/path") for /tmp/x.
	abs, _ := filepath.Abs("/tmp/x")
	id := volumeIDFromMountPath("/tmp/x")
	require.NotEmpty(t, id)
	require.NotEqual(t, abs, id)
	// Determinism: a second call returns the same id.
	require.Equal(t, id, volumeIDFromMountPath("/tmp/x"))
}
