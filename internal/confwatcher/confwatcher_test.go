package confwatcher

import (
	"os"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/stretchr/testify/require"
)

func TestNoFile(t *testing.T) {
	w := &ConfWatcher{FilePath: "/nonexistent"}
	err := w.Initialize()
	require.Error(t, err)
}

func TestWrite(t *testing.T) {
	fpath, err := test.CreateTempFile([]byte("{}"))
	require.NoError(t, err)

	w := &ConfWatcher{FilePath: fpath}
	err = w.Initialize()
	require.NoError(t, err)
	defer w.Close()

	func() {
		var f *os.File
		f, err = os.Create(fpath)
		require.NoError(t, err)
		defer f.Close()

		_, err = f.Write([]byte("{}"))
		require.NoError(t, err)
	}()

	select {
	case <-w.Watch():
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timed out")
		return
	}
}

func TestWriteMultipleTimes(t *testing.T) {
	fpath, err := test.CreateTempFile([]byte("{}"))
	require.NoError(t, err)

	w := &ConfWatcher{FilePath: fpath}
	err = w.Initialize()
	require.NoError(t, err)
	defer w.Close()

	func() {
		f, err2 := os.Create(fpath)
		require.NoError(t, err2)
		defer f.Close()

		_, err2 = f.Write([]byte("{}"))
		require.NoError(t, err2)
	}()

	time.Sleep(10 * time.Millisecond)

	func() {
		f, err2 := os.Create(fpath)
		require.NoError(t, err2)
		defer f.Close()

		_, err2 = f.Write([]byte("{}"))
		require.NoError(t, err2)
	}()

	select {
	case <-w.Watch():
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timed out")
		return
	}

	select {
	case <-time.After(500 * time.Millisecond):
	case <-w.Watch():
		t.Errorf("should not happen")
		return
	}
}

func TestDeleteCreate(t *testing.T) {
	fpath, err := test.CreateTempFile([]byte("{}"))
	require.NoError(t, err)

	w := &ConfWatcher{FilePath: fpath}
	err = w.Initialize()
	require.NoError(t, err)
	defer w.Close()

	os.Remove(fpath)
	time.Sleep(10 * time.Millisecond)

	func() {
		var f *os.File
		f, err = os.Create(fpath)
		require.NoError(t, err)
		defer f.Close()

		_, err = f.Write([]byte("{}"))
		require.NoError(t, err)
	}()

	select {
	case <-w.Watch():
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timed out")
		return
	}
}

// TestSelfWriteSuppressed: when the recorder persists its own conf via
// SaveToFile and informs the watcher via NoteSelfWrite, the matching
// fsnotify fire must NOT propagate as a reload signal — otherwise every
// API config-set would loop into "reload because file changed" → reload
// → save → reload → ... burning CPU and emitting bogus log entries.
func TestSelfWriteSuppressed(t *testing.T) {
	fpath, err := test.CreateTempFile([]byte("{}"))
	require.NoError(t, err)

	w := &ConfWatcher{FilePath: fpath}
	err = w.Initialize()
	require.NoError(t, err)
	defer w.Close()

	// Hash the bytes that are about to land on disk; tell the watcher
	// what to expect; then write them.
	content := []byte(`{"hello":"world"}`)
	w.NoteSelfWrite(content)

	func() {
		f, ferr := os.Create(fpath)
		require.NoError(t, ferr)
		defer f.Close()
		_, werr := f.Write(content)
		require.NoError(t, werr)
	}()

	// No reload signal should arrive within a reasonable window.
	select {
	case <-w.Watch():
		t.Errorf("self-write produced an unwanted reload signal")
	case <-time.After(300 * time.Millisecond):
		// expected: no signal
	}

	// A subsequent external edit (different content) MUST still
	// trigger a reload — the dedup is one-shot, not persistent.
	// minInterval is 1s, so wait past that before the external edit.
	time.Sleep(1100 * time.Millisecond)

	external := []byte(`{"changed":"yes"}`)
	func() {
		f, ferr := os.Create(fpath)
		require.NoError(t, ferr)
		defer f.Close()
		_, werr := f.Write(external)
		require.NoError(t, werr)
	}()

	select {
	case <-w.Watch():
		// expected
	case <-time.After(2 * time.Second):
		t.Errorf("external edit failed to trigger reload after self-write was cleared")
	}
}

// TestNoteSelfWriteNilReceiver: the API config-set path may run with no
// confwatcher (no on-disk config). NoteSelfWrite on a nil receiver
// must be a silent no-op, not panic.
func TestNoteSelfWriteNilReceiver(t *testing.T) {
	var w *ConfWatcher
	require.NotPanics(t, func() {
		w.NoteSelfWrite([]byte("anything"))
	})
}

func TestSymlinkDeleteCreate(t *testing.T) {
	fpath, err := test.CreateTempFile([]byte("{}"))
	require.NoError(t, err)

	err = os.Symlink(fpath, fpath+"-sym")
	require.NoError(t, err)

	// Both fpath and fpath+"-sym" must be removed at test end.
	// macOS's TempDir is /var/folders/.../T which is not auto-pruned
	// the way /tmp is on Linux, so dangling symlinks (real file goes
	// missing, symlink stays) accumulate across runs and trigger
	// spurious fsnotify events in unrelated tests that watch the
	// same temp directory (e.g. internal/core/path_test.go's
	// TestPathOverridePublisher). Cleaning up reliably here keeps
	// the test infrastructure hermetic.
	t.Cleanup(func() {
		_ = os.Remove(fpath + "-sym")
		_ = os.Remove(fpath)
	})

	w := &ConfWatcher{FilePath: fpath + "-sym"}
	err = w.Initialize()
	require.NoError(t, err)
	defer w.Close()

	os.Remove(fpath)

	func() {
		f, err2 := os.Create(fpath)
		require.NoError(t, err2)
		defer f.Close()

		_, err2 = f.Write([]byte("{}"))
		require.NoError(t, err2)
	}()

	select {
	case <-w.Watch():
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timed out")
		return
	}
}
