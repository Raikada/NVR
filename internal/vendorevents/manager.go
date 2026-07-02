package vendorevents

import (
	"context"
	"sync"

	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// Resolver turns a store camera row into the ChannelCamera a factory
// consumes. Core wiring adds capabilities lookups + the credentials
// closure; tests use a plain field mapping.
type Resolver func(cam *store.Camera) ChannelCamera

// runningEntry tracks one live channel goroutine. gen disambiguates a
// stale goroutine's cleanup from a fresh channel started for the same
// camera after a restart.
type runningEntry struct {
	cancel context.CancelFunc
	gen    uint64
}

// Manager reconciles one supervisor per enabled camera with an active
// channel, off a bootstrap list + the cameras Bus.
type Manager struct {
	lister    CameraLister
	bus       *cameras.Bus
	factories map[string]AdapterFactory
	sink      EventSink
	touch     Toucher
	resolve   Resolver
	logger    logger.Writer

	mu      sync.Mutex
	running map[string]runningEntry // camera id → live channel
	nextGen uint64
}

// NewManager wires a Manager. touch and logger may be nil.
func NewManager(lister CameraLister, bus *cameras.Bus, factories map[string]AdapterFactory,
	sink EventSink, touch Toucher, resolve Resolver, log logger.Writer,
) *Manager {
	return &Manager{
		lister:    lister,
		bus:       bus,
		factories: factories,
		sink:      sink,
		touch:     touch,
		resolve:   resolve,
		logger:    log,
		running:   map[string]runningEntry{},
	}
}

// Run bootstraps channels for existing cameras, then reconciles off the
// bus until ctx cancels.
func (m *Manager) Run(ctx context.Context) {
	ch, unsub := m.bus.Subscribe()
	defer unsub()

	if cams, err := m.lister.List(ctx, store.ListCamerasFilter{Limit: 500}); err == nil {
		for _, cam := range cams {
			m.reconcile(ctx, cam, false)
		}
	} else {
		m.log("bootstrap list: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case change, ok := <-ch:
			if !ok {
				m.stopAll()
				return
			}
			switch change.Type {
			case cameras.ChangeDeleted:
				m.stop(change.CameraID)
			case cameras.ChangeCreated, cameras.ChangeUpdated:
				if change.Camera != nil {
					// Updates restart the channel: credentials or the
					// source may have changed.
					m.reconcile(ctx, change.Camera, change.Type == cameras.ChangeUpdated)
				}
			}
		}
	}
}

// reconcile ensures the camera's desired channel state. restart forces
// a stop-then-start even when a channel is already running.
func (m *Manager) reconcile(ctx context.Context, cam *store.Camera, restart bool) {
	if restart {
		m.stop(cam.ID)
	}

	cc := m.resolve(cam)
	channel := SelectChannel(cc.Channel, cc.Manufacturer, cc.OnvifXAddr)
	if !cam.Enabled || channel == "none" {
		m.stop(cam.ID)
		return
	}
	factory, ok := m.factories[channel]
	if !ok {
		m.log("camera %s: no factory for channel %q", cam.ID, channel)
		return
	}

	m.mu.Lock()
	if _, alreadyRunning := m.running[cam.ID]; alreadyRunning {
		m.mu.Unlock()
		return
	}
	subCtx, cancel := context.WithCancel(ctx)
	m.nextGen++
	gen := m.nextGen
	m.running[cam.ID] = runningEntry{cancel: cancel, gen: gen}
	m.mu.Unlock()

	go func() {
		defer m.clearEntry(cam.ID, gen, cancel)
		adapter, err := factory(subCtx, cc)
		if err != nil {
			m.log("camera %s channel %s: %v", cam.ID, channel, err)
			return
		}
		sup := newSupervisor(cam.ID, channel, adapter, m.sink, m.touch, m.logger)
		sup.run(subCtx)
	}()
}

func (m *Manager) stop(cameraID string) {
	m.mu.Lock()
	entry, ok := m.running[cameraID]
	if ok {
		delete(m.running, cameraID)
	}
	m.mu.Unlock()
	if ok {
		entry.cancel()
	}
}

// clearEntry removes the registry entry when a channel goroutine exits
// on its own (factory failure or supervisor return). The generation
// check keeps a stale goroutine from evicting a newer channel started
// for the same camera.
func (m *Manager) clearEntry(cameraID string, gen uint64, cancel context.CancelFunc) {
	cancel()
	m.mu.Lock()
	if entry, ok := m.running[cameraID]; ok && entry.gen == gen {
		delete(m.running, cameraID)
	}
	m.mu.Unlock()
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.running))
	for id, e := range m.running {
		cancels = append(cancels, e.cancel)
		delete(m.running, id)
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (m *Manager) log(format string, args ...any) {
	if m.logger != nil {
		m.logger.Log(logger.Warn, "[vendorevents] "+format, args...)
	}
}
