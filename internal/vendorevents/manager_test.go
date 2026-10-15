package vendorevents

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/store"
)

// trackingFactory records adapter lifecycles per camera. Liveness is a
// counter (not a bool): a stale goroutine's exit must not mask a fresh
// channel started for the same camera.
type trackingFactory struct {
	mu      sync.Mutex
	started map[string]int
	live    map[string]int
}

func newTrackingFactory() *trackingFactory {
	return &trackingFactory{
		started: map[string]int{},
		live:    map[string]int{},
	}
}

func (f *trackingFactory) factory() AdapterFactory {
	return func(_ context.Context, cam ChannelCamera) (Adapter, error) {
		return adapterFunc(func(ctx context.Context, _ func(NormalizedEvent)) error {
			f.mu.Lock()
			f.started[cam.ID]++
			f.live[cam.ID]++
			f.mu.Unlock()
			<-ctx.Done()
			f.mu.Lock()
			f.live[cam.ID]--
			f.mu.Unlock()
			return ctx.Err()
		}), nil
	}
}

func (f *trackingFactory) startCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[id]
}

func (f *trackingFactory) isLive(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[id] > 0
}

// adapterFunc adapts a func to Adapter.
type adapterFunc func(ctx context.Context, emit func(NormalizedEvent)) error

func (fn adapterFunc) Run(ctx context.Context, emit func(NormalizedEvent)) error {
	return fn(ctx, emit)
}

type staticLister struct {
	mu   sync.Mutex
	cams []*store.Camera
}

func (s *staticLister) List(_ context.Context, _ store.ListCamerasFilter) ([]*store.Camera, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cams, nil
}

func amcrestCam(id, name string) *store.Camera {
	return &store.Camera{
		ID: id, Name: name, SourceType: "rtsp",
		SourceURL:    "rtsp://192.0.2.9:554/s",
		Manufacturer: "Amcrest",
		Enabled:      true,
	}
}

func newTestManager(f *trackingFactory, lister CameraLister, bus *cameras.Bus) *Manager {
	return NewManager(lister, bus, map[string]AdapterFactory{
		"amcrest": f.factory(),
		"onvif":   f.factory(),
	}, &recordingSink{}, nil, defaultResolve, nil)
}

// defaultResolve builds a ChannelCamera straight off the store row —
// production wiring adds capabilities lookups.
func defaultResolve(cam *store.Camera) ChannelCamera {
	return ChannelCamera{
		ID:           cam.ID,
		Name:         cam.Name,
		Manufacturer: cam.Manufacturer,
		OnvifXAddr:   cam.OnvifXAddr,
		Channel:      cam.EventChannel,
	}
}

func TestManagerBootstrapStartsChannels(t *testing.T) {
	f := newTrackingFactory()
	lister := &staticLister{cams: []*store.Camera{amcrestCam("cam-1", "one")}}
	bus := cameras.NewBus()
	m := newTestManager(f, lister, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	waitFor(t, func() bool { return f.isLive("cam-1") })
	require.Equal(t, 1, f.startCount("cam-1"))
}

func TestManagerReconcileOnBusEvents(t *testing.T) {
	f := newTrackingFactory()
	lister := &staticLister{}
	bus := cameras.NewBus()
	m := newTestManager(f, lister, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	time.Sleep(20 * time.Millisecond) // let bootstrap finish (empty)

	// Create → channel starts.
	cam := amcrestCam("cam-2", "two")
	bus.Publish(cameras.Change{Type: cameras.ChangeCreated, Camera: cam, CameraID: cam.ID})
	waitFor(t, func() bool { return f.isLive("cam-2") })

	// Update → restart (fresh creds/url).
	bus.Publish(cameras.Change{Type: cameras.ChangeUpdated, Camera: cam, CameraID: cam.ID})
	waitFor(t, func() bool { return f.startCount("cam-2") >= 2 })

	// Channel off → stop.
	off := *cam
	off.EventChannel = "none"
	bus.Publish(cameras.Change{Type: cameras.ChangeUpdated, Camera: &off, CameraID: cam.ID})
	waitFor(t, func() bool { return !f.isLive("cam-2") })

	// Back on, then delete → stop.
	bus.Publish(cameras.Change{Type: cameras.ChangeUpdated, Camera: cam, CameraID: cam.ID})
	waitFor(t, func() bool { return f.isLive("cam-2") })
	bus.Publish(cameras.Change{Type: cameras.ChangeDeleted, CameraID: cam.ID})
	waitFor(t, func() bool { return !f.isLive("cam-2") })
}

func TestManagerDisabledCameraNotStarted(t *testing.T) {
	f := newTrackingFactory()
	disabled := amcrestCam("cam-3", "three")
	disabled.Enabled = false
	lister := &staticLister{cams: []*store.Camera{disabled}}
	bus := cameras.NewBus()
	m := newTestManager(f, lister, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, f.startCount("cam-3"))
}
