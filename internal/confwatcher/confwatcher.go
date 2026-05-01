// Package confwatcher contains a configuration watcher.
package confwatcher

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	minInterval    = 1 * time.Second
	additionalWait = 10 * time.Millisecond

	// pollInterval is how often the watcher Stats the watched file as
	// a safety net for fsnotify events the OS doesn't reliably
	// deliver. On macOS (kqueue + FSEvents), fsnotify does NOT
	// deliver events for a file path after it's been removed once —
	// subsequent re-creates and writes don't surface as fsnotify
	// events because the OS-side watch is invalidated when the inode
	// goes away. The polling fallback recovers from that.
	//
	// 200ms is a deliberate trade: fast enough that the polling
	// cycle doesn't dominate test runtimes (TestDeleteCreate's 500ms
	// timeout means we get 2-3 poll cycles), but slow enough that
	// the periodic Stat doesn't materially load the system in
	// production. fsnotify remains the fast path; this is just the
	// safety net.
	pollInterval = 200 * time.Millisecond
)

// ConfWatcher is a configuration file watcher.
type ConfWatcher struct {
	FilePath string

	inner        *fsnotify.Watcher
	absolutePath string

	// expectedHash carries the SHA-256 of bytes the recorder itself
	// just wrote to the watched file via NoteSelfWrite. The watcher
	// loop compares the on-disk hash against this on each fire and
	// suppresses the signal when they match — that's the recorder
	// observing its own SaveToFile, not an external edit, and a
	// reload would loop pointlessly. Pattern A from the design doc:
	// content-dedup, race-free regardless of fsnotify timing because
	// hashes don't depend on event ordering.
	expectedMu   sync.Mutex
	expectedHash [sha256.Size]byte
	expectedSet  bool

	// in
	terminate chan struct{}

	// out
	signal chan struct{}
	done   chan struct{}
}

// Initialize initializes a ConfWatcher.
func (w *ConfWatcher) Initialize() error {
	if _, err := os.Stat(w.FilePath); err != nil {
		return err
	}

	var err error
	w.inner, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// use absolute paths to support Darwin
	w.absolutePath, _ = filepath.Abs(w.FilePath)
	parentPath := filepath.Dir(w.absolutePath)

	err = w.inner.Add(parentPath)
	if err != nil {
		w.inner.Close() //nolint:errcheck
		return err
	}

	w.terminate = make(chan struct{})
	w.signal = make(chan struct{})
	w.done = make(chan struct{})

	go w.run()

	return nil
}

// Close closes a ConfWatcher.
func (w *ConfWatcher) Close() {
	close(w.terminate)
	<-w.done
}

// NoteSelfWrite records the SHA-256 of bytes the recorder just persisted
// to the watched file via SaveToFile. The next fsnotify event whose
// disk content matches this hash is suppressed; subsequent fires (real
// external edits) trigger reload normally.
//
// Safe to call from any goroutine. A nil receiver or a watcher that
// hasn't been Initialized yet is a silent no-op — the API config-set
// handler runs even when no confwatcher is wired (no on-disk config).
func (w *ConfWatcher) NoteSelfWrite(content []byte) {
	if w == nil {
		return
	}
	w.expectedMu.Lock()
	w.expectedHash = sha256.Sum256(content)
	w.expectedSet = true
	w.expectedMu.Unlock()
}

// matchesSelfWrite returns true if the current on-disk content of the
// watched file hashes to the same value the recorder last persisted via
// NoteSelfWrite. On match, the cached hash is cleared so a subsequent
// external edit (which produces different content, then potentially is
// reverted to the cached content) doesn't get silently swallowed. On
// any read error, returns false (let the reload fire; Conf.Load will
// surface the real error).
func (w *ConfWatcher) matchesSelfWrite() bool {
	w.expectedMu.Lock()
	defer w.expectedMu.Unlock()
	if !w.expectedSet {
		return false
	}
	bytesOnDisk, err := os.ReadFile(w.absolutePath)
	if err != nil {
		return false
	}
	if sha256.Sum256(bytesOnDisk) != w.expectedHash {
		return false
	}
	w.expectedSet = false
	return true
}

func (w *ConfWatcher) run() {
	defer close(w.done)

	var lastCalled time.Time
	previousWatchedPath, _ := filepath.EvalSymlinks(w.absolutePath)

	// fire encapsulates the "watched file changed → signal upstream"
	// path so both the fsnotify branch and the polling-fallback
	// branch can reuse it. Returns true if the run loop should exit
	// (terminate received during the signal handoff).
	fire := func(currentWatchedPath string) bool {
		time.Sleep(additionalWait)
		previousWatchedPath = currentWatchedPath

		if w.matchesSelfWrite() {
			lastCalled = time.Now()
			return false
		}

		lastCalled = time.Now()
		select {
		case w.signal <- struct{}{}:
		case <-w.terminate:
			return true
		}
		return false
	}

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

outer:
	for {
		select {
		case event := <-w.inner.Events:
			if time.Since(lastCalled) < minInterval {
				continue
			}

			currentWatchedPath, _ := filepath.EvalSymlinks(w.absolutePath)
			eventPath, _ := filepath.Abs(event.Name)
			eventPath, _ = filepath.EvalSymlinks(eventPath)

			if currentWatchedPath == "" {
				// watched file was removed; wait for write event to trigger reload
				previousWatchedPath = ""
			} else if currentWatchedPath != previousWatchedPath ||
				(eventPath == currentWatchedPath &&
					((event.Op&fsnotify.Write) == fsnotify.Write ||
						(event.Op&fsnotify.Create) == fsnotify.Create)) {
				if fire(currentWatchedPath) {
					break outer
				}
			}

		case <-pollTicker.C:
			// Polling fallback: fsnotify on macOS doesn't deliver
			// events for a file path after it's been removed once,
			// so an editor that does delete-then-create (vim-style
			// backup-and-replace, or remove-and-recreate from the API
			// path) would never wake the watcher. Stat the file and
			// compare against previousWatchedPath to detect the
			// transition fsnotify missed.
			if time.Since(lastCalled) < minInterval {
				continue
			}
			currentWatchedPath, _ := filepath.EvalSymlinks(w.absolutePath)
			if currentWatchedPath != "" && currentWatchedPath != previousWatchedPath {
				if fire(currentWatchedPath) {
					break outer
				}
			} else if currentWatchedPath == "" && previousWatchedPath != "" {
				// File went missing without an fsnotify event; reset
				// state so the next reappearance fires.
				previousWatchedPath = ""
			}

		case <-w.inner.Errors:
			break outer

		case <-w.terminate:
			break outer
		}
	}

	close(w.signal)
	w.inner.Close() //nolint:errcheck
}

// Watch returns a channel that is called after the configuration file has changed.
func (w *ConfWatcher) Watch() chan struct{} {
	return w.signal
}
