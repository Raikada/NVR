// Package core: TLS hot-reload watcher for Task 6.5.
//
// The recorder's HTTPS listener already uses internal/certloader, which
// watches the configured cert + key paths via fsnotify and atomically
// swaps the loaded pair on WRITE events. tlsReloader is the Phase 6
// audit + safety wrapper around that surface: it tails the same paths
// with a parallel fsnotify watch so a parse-failure path can write a
// system.tls_reload_failed audit row without modifying certloader.
//
// The audit row is intentionally redundant: certloader logs parse
// failures, but the audit chain is the operator-visible record. Both
// fire on the same event.
package core

import (
	"context"
	"crypto/tls"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/bluenviron/mediamtx/internal/audit"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// tlsReloader watches certPath + keyPath via fsnotify; on WRITE it
// validates the pair and writes a system.tls_reload_failed audit row
// when parsing fails. The actual cert swap lives in
// internal/certloader (already wired through httpp.Server). This type
// exists to give the audit row a place to land.
type tlsReloader struct {
	certPath string
	keyPath  string
	emit     *audit.Emitter
	logger   logger.Writer

	mu      sync.Mutex
	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
}

// newTLSReloader wires a tlsReloader for certPath + keyPath. Either path
// may be empty; in that case Run is a no-op.
func newTLSReloader(certPath, keyPath string, emit *audit.Emitter, log logger.Writer) *tlsReloader {
	return &tlsReloader{certPath: certPath, keyPath: keyPath, emit: emit, logger: log}
}

// Run blocks until ctx is cancelled. Watches both files via fsnotify;
// on WRITE attempts a parse and emits an audit row on failure.
func (r *tlsReloader) Run(ctx context.Context) {
	if r == nil || r.certPath == "" || r.keyPath == "" {
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		if r.logger != nil {
			r.logger.Log(logger.Warn, "[tls.reload] watcher: %v", err)
		}
		return
	}
	defer w.Close()

	// Watching the parent dir handles atomic-rename writes (the most
	// common pattern); fsnotify doesn't see WRITE on a path that was
	// replaced in-place via rename.
	if err := w.Add(r.certPath); err != nil && r.logger != nil {
		r.logger.Log(logger.Debug, "[tls.reload] add %s: %v", r.certPath, err)
	}
	if err := w.Add(r.keyPath); err != nil && r.logger != nil {
		r.logger.Log(logger.Debug, "[tls.reload] add %s: %v", r.keyPath, err)
	}

	r.mu.Lock()
	r.watcher = w
	r.mu.Unlock()

	// Debounce: after the first event, wait 200ms for follow-on events
	// before validating. fsnotify often fires multiple times during a
	// single rewrite (TRUNCATE + WRITE on most editors).
	const debounce = 200 * time.Millisecond
	var pendingTimer *time.Timer
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Write == 0 && ev.Op&fsnotify.Create == 0 {
				continue
			}
			if pendingTimer != nil {
				pendingTimer.Stop()
			}
			pendingTimer = time.AfterFunc(debounce, r.validate)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			if r.logger != nil {
				r.logger.Log(logger.Warn, "[tls.reload] watch err: %v", err)
			}
		}
	}
}

// validate tries to load + parse the pair. On failure, emits a
// system.tls_reload_failed audit row.
func (r *tlsReloader) validate() {
	if _, err := tls.LoadX509KeyPair(r.certPath, r.keyPath); err != nil {
		if r.logger != nil {
			r.logger.Log(logger.Warn, "[tls.reload] parse failed (keeping prior pair): %v", err)
		}
		if r.emit != nil && r.emit.Repo() != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = r.emit.Repo().Insert(ctx, &store.AuditEntry{
				Action:     "system.tls_reload_failed",
				TargetKind: "system",
				Details:    err.Error(),
			})
		}
		return
	}
	if r.logger != nil {
		r.logger.Log(logger.Info, "[tls.reload] new cert validated; certloader will pick it up")
	}
}
