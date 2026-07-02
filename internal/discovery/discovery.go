// Package discovery caches WS-Discovery results for the camera-adopt
// flow (SP2). One goroutine owns the probe cadence; API reads take a
// mutex-guarded snapshot. Nothing persists — a reboot re-probes.
package discovery

import (
	"context"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/store"
)

const (
	// probeInterval is the background probe cadence.
	probeInterval = 60 * time.Second
	// entryTTL drops entries not seen for this long: long enough to
	// ride out a few missed multicast rounds, short enough that
	// unplugged cameras leave the list within minutes.
	entryTTL = 10 * time.Minute
)

// Prober abstracts the WS-Discovery round for tests.
type Prober interface {
	Probe(ctx context.Context) ([]onvif.DiscoveredDevice, error)
}

// CameraLister abstracts the camera store for matching.
type CameraLister interface {
	List(ctx context.Context, f store.ListCamerasFilter) ([]*store.Camera, error)
}

// Entry is one discovered device plus cache bookkeeping.
type Entry struct {
	onvif.DiscoveredDevice
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	MatchedCameraID string    `json:"matched_camera_id,omitempty"`
}

// Service owns the discovery cache.
type Service struct {
	prober  Prober
	cameras CameraLister
	logger  logger.Writer
	clock   func() time.Time

	mu    sync.Mutex
	cache map[string]Entry // keyed by EndpointRef
}

// New wires a Service. logger may be nil.
func New(p Prober, c CameraLister, log logger.Writer) *Service {
	return &Service{
		prober:  p,
		cameras: c,
		logger:  log,
		clock:   time.Now,
		cache:   make(map[string]Entry),
	}
}

// Run probes immediately, then on the background cadence, until ctx
// cancels.
func (s *Service) Run(ctx context.Context) {
	if _, err := s.ProbeNow(ctx); err != nil {
		s.log("initial probe: %v", err)
	}
	t := time.NewTicker(probeInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.ProbeNow(ctx); err != nil {
				s.log("probe: %v", err)
			}
		}
	}
}

// ProbeNow runs one probe round, folds results into the cache, ages
// out stale entries, refreshes camera matching, and returns the
// snapshot. A probe error leaves the cache untouched.
func (s *Service) ProbeNow(ctx context.Context) ([]Entry, error) {
	devices, err := s.prober.Probe(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock()

	matches := s.matchTable(ctx)

	s.mu.Lock()
	for _, d := range devices {
		if d.EndpointRef == "" {
			continue
		}
		e, seen := s.cache[d.EndpointRef]
		if !seen {
			e = Entry{FirstSeenAt: now}
		}
		e.DiscoveredDevice = d
		e.LastSeenAt = now
		s.cache[d.EndpointRef] = e
	}
	for ref, e := range s.cache {
		if now.Sub(e.LastSeenAt) > entryTTL {
			delete(s.cache, ref)
			continue
		}
		e.MatchedCameraID = matches[hostOf(e.XAddr)]
		s.cache[ref] = e
	}
	s.mu.Unlock()

	return s.Snapshot(), nil
}

// Snapshot returns cache entries sorted newest-seen first.
func (s *Service) Snapshot() []Entry {
	s.mu.Lock()
	out := make([]Entry, 0, len(s.cache))
	for _, e := range s.cache {
		out = append(out, e)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeenAt.Equal(out[j].LastSeenAt) {
			return out[i].LastSeenAt.After(out[j].LastSeenAt)
		}
		return out[i].EndpointRef < out[j].EndpointRef
	})
	return out
}

// Lookup fetches one entry by endpoint reference.
func (s *Service) Lookup(endpointRef string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[endpointRef]
	return e, ok
}

// matchTable maps host → camera id for every managed camera, drawn
// from both source_url and onvif_xaddr hosts. Advisory only: a listing
// failure just means no matches this round.
func (s *Service) matchTable(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.cameras == nil {
		return out
	}
	cams, err := s.cameras.List(ctx, store.ListCamerasFilter{Limit: 500})
	if err != nil {
		s.log("camera list for matching: %v", err)
		return out
	}
	for _, c := range cams {
		if h := hostOf(c.SourceURL); h != "" {
			out[h] = c.ID
		}
		if h := hostOf(c.OnvifXAddr); h != "" {
			out[h] = c.ID
		}
	}
	return out
}

// hostOf extracts the bare hostname (no port) from a URL, "" when
// unparseable.
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (s *Service) log(format string, args ...any) {
	if s.logger != nil {
		s.logger.Log(logger.Warn, "[discovery] "+format, args...)
	}
}
