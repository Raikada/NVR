// Package camerahealth polls the path manager and maintains
// camera_health rows plus camera_online/camera_offline events (SP2).
//
// State machine per camera:
//
//	connected    — path online, frames flowing
//	reconnecting — path present but not online, failures < threshold
//	failed       — ≥ threshold consecutive failed polls
//	idle         — camera disabled or no path registered
//
// camera_offline fires when a camera leaves connected and stays down
// past the debounce (credential-rotation restarts stay silent);
// camera_online fires on recovery after an offline was emitted.
package camerahealth

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

const (
	pollInterval  = 5 * time.Second
	debounce      = 30 * time.Second
	failThreshold = 5
	heartbeat     = 60 * time.Second
	listTimeout   = 2 * time.Second
)

// Event type ids seeded by migration 0012.
const (
	typeCameraOffline = "camera_offline"
	typeCameraOnline  = "camera_online"
)

// PathSnapshot is the per-path state the collector consumes each poll.
type PathSnapshot struct {
	Name        string
	Online      bool
	LastFrameAt time.Time
}

// PathLister abstracts the path manager.
type PathLister interface {
	ListPaths(ctx context.Context) ([]PathSnapshot, error)
}

// EventSink abstracts events.Service.
type EventSink interface {
	Insert(ctx context.Context, ev *events.Event) error
}

// camState is the collector's in-memory bookkeeping per camera.
type camState struct {
	state           string
	failures        int
	downSince       time.Time
	offlineEmitted  bool
	lastUpsert      time.Time
	lastError       string
	pendingEventAt  time.Time // from Touch; zero when none pending
	lastKeyframeAt  time.Time
}

// Collector polls and persists camera health.
type Collector struct {
	store  *store.Store
	paths  PathLister
	sink   EventSink
	logger logger.Writer
	clock  func() time.Time

	mu   sync.Mutex
	cams map[string]*camState
}

// New wires a Collector. logger may be nil.
func New(st *store.Store, paths PathLister, sink EventSink, log logger.Writer) *Collector {
	return &Collector{
		store:  st,
		paths:  paths,
		sink:   sink,
		logger: log,
		clock:  time.Now,
		cams:   map[string]*camState{},
	}
}

// Run polls until ctx cancels.
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.PollOnce(ctx)
		}
	}
}

// Touch stamps a camera's last_event_at on the next poll flush. SP3's
// vendor channels call this so event activity shows in health without
// them owning the row.
func (c *Collector) Touch(cameraID string, lastEventAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.cams[cameraID]
	if s == nil {
		s = &camState{}
		c.cams[cameraID] = s
	}
	if lastEventAt.After(s.pendingEventAt) {
		s.pendingEventAt = lastEventAt
	}
}

// PollOnce runs one poll round. Exported for tests; Run calls it on
// the ticker.
func (c *Collector) PollOnce(ctx context.Context) {
	now := c.clock()

	cams, err := c.store.Cameras.List(ctx, store.ListCamerasFilter{Limit: 500})
	if err != nil {
		c.log("camera list: %v", err)
		return
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	snaps, err := c.paths.ListPaths(listCtx)
	cancel()
	if err != nil {
		// A broken poll source is not camera failure: hold state.
		c.log("path list: %v", err)
		return
	}
	byName := map[string]PathSnapshot{}
	for _, s := range snaps {
		byName[s.Name] = s
	}

	for _, cam := range cams {
		snap, hasPath := byName[cam.Name]
		c.evaluate(ctx, cam, snap, hasPath, now)
	}
}

// evaluate advances one camera's state machine and persists as needed.
func (c *Collector) evaluate(ctx context.Context, cam *store.Camera, snap PathSnapshot, hasPath bool, now time.Time) {
	c.mu.Lock()
	s := c.cams[cam.ID]
	if s == nil {
		s = &camState{}
		c.cams[cam.ID] = s
	}

	prev := s.state
	switch {
	case !cam.Enabled || !hasPath:
		s.state = "idle"
		s.failures = 0
		s.downSince = time.Time{}
	case snap.Online:
		s.state = "connected"
		s.failures = 0
		s.downSince = time.Time{}
		s.lastError = ""
		if !snap.LastFrameAt.IsZero() {
			s.lastKeyframeAt = snap.LastFrameAt
		}
	default:
		s.failures++
		if s.downSince.IsZero() {
			s.downSince = now
		}
		if s.failures >= failThreshold {
			s.state = "failed"
			s.lastError = "rtsp source not online"
		} else {
			s.state = "reconnecting"
		}
	}

	var emit *events.Event
	switch {
	case s.state == "failed" && !s.offlineEmitted && now.Sub(s.downSince) >= debounce:
		s.offlineEmitted = true
		payload, _ := json.Marshal(map[string]string{
			"from": prev, "state": s.state, "last_error": s.lastError,
		})
		emit = &events.Event{
			CameraID: cam.ID, TypeID: typeCameraOffline,
			Source: "health", Severity: "warning", PayloadJSON: payload,
		}
	case s.state == "connected" && s.offlineEmitted:
		s.offlineEmitted = false
		payload, _ := json.Marshal(map[string]string{"state": "connected"})
		emit = &events.Event{
			CameraID: cam.ID, TypeID: typeCameraOnline,
			Source: "health", Severity: "info", PayloadJSON: payload,
		}
	}

	changed := s.state != prev
	due := now.Sub(s.lastUpsert) >= heartbeat
	pendingTouch := !s.pendingEventAt.IsZero()
	var row *store.CameraHealth
	if changed || due || pendingTouch {
		row = &store.CameraHealth{
			CameraID:            cam.ID,
			RTSPState:           s.state,
			LastKeyframeAt:      s.lastKeyframeAt,
			LastEventAt:         s.pendingEventAt,
			LastSeenAt:          now,
			ConsecutiveFailures: s.failures,
			LastError:           s.lastError,
			UpdatedAt:           now,
		}
		s.lastUpsert = now
		s.pendingEventAt = time.Time{}
	}
	c.mu.Unlock()

	if row != nil {
		if err := c.store.CameraHealth.Upsert(ctx, row); err != nil {
			c.log("health upsert %s: %v", cam.ID, err)
		}
	}
	if emit != nil && c.sink != nil {
		if err := c.sink.Insert(ctx, emit); err != nil {
			c.log("health event %s %s: %v", cam.ID, emit.TypeID, err)
		}
	}
}

func (c *Collector) log(format string, args ...any) {
	if c.logger != nil {
		c.logger.Log(logger.Warn, "[camerahealth] "+format, args...)
	}
}
