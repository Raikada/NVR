// Package core: mDNS TXT-record refresher for Task 6.6.
//
// The recorder advertises a setup-state TXT record so operator UIs on
// the LAN can render the right post-discovery flow ("setup wizard" vs
// "log in"). The state flips when the bootstrap admin completes the
// forced password rotation, so we poll system state every 30s and
// republish on change.
package core

import (
	"context"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/mdns"
	"github.com/bluenviron/mediamtx/internal/store"
)

// mdnsRefresher polls setup state every 30s and republishes the mDNS
// TXT records when the state changes. Run blocks until ctx is cancelled.
type mdnsRefresher struct {
	svc      *mdns.Service
	store    *store.Store
	version  string
	identity string
	logger   logger.Writer

	mu      sync.Mutex
	lastTXT map[string]string
}

// newMDNSRefresher wires a refresher pinned to svc. Either argument may
// be nil; in that case Run is a no-op.
func newMDNSRefresher(svc *mdns.Service, st *store.Store, version, identity string, log logger.Writer) *mdnsRefresher {
	return &mdnsRefresher{svc: svc, store: st, version: version, identity: identity, logger: log}
}

// Run blocks until ctx is cancelled. Polls every 30s; the first tick
// fires after a short warm-up so the broadcaster reflects setup-state
// from startup without racing the rest of Core's bring-up.
func (r *mdnsRefresher) Run(ctx context.Context) {
	if r == nil || r.svc == nil {
		return
	}
	// Warm-up wait: 5s gives the rest of createResources time to
	// finish so the first publish doesn't race the API listener.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	r.publish(ctx)

	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.publish(ctx)
		}
	}
}

func (r *mdnsRefresher) publish(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	txt := r.computeTXT(ctx)
	r.mu.Lock()
	if mapsEqual(r.lastTXT, txt) {
		r.mu.Unlock()
		return
	}
	r.lastTXT = txt
	r.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	r.svc.SetTXT(txt)
	if err := r.svc.Refresh(); err != nil && r.logger != nil {
		r.logger.Log(logger.Warn, "[mdns] refresh: %v", err)
	}
}

func (r *mdnsRefresher) computeTXT(ctx context.Context) map[string]string {
	setup := "complete"
	if r.store != nil {
		n, _ := r.store.LocalUsers.Count(ctx)
		if n == 0 {
			setup = "required"
		} else {
			users, err := r.store.LocalUsers.ListAll(ctx)
			if err == nil {
				for _, u := range users {
					if u.IsAdmin && u.MustChangePassword {
						setup = "required"
						break
					}
				}
			}
		}
	}
	return map[string]string{
		"v":     r.version,
		"id":    r.identity,
		"setup": setup,
	}
}

// mapsEqual reports whether a and b have identical key/value pairs.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
