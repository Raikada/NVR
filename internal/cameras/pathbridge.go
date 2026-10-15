package cameras

import (
	"context"
	"sort"
	"sync"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// PathManager is the seam this package uses to translate camera CRUD
// into path-manager state. The Phase 6 adapter wraps the existing
// internal/core.pathManager (which exposes ReloadPathConfs(map)) and
// satisfies this interface by translating the slice into a fresh map.
type PathManager interface {
	ReloadFromCameras(ctx context.Context, cameras []CameraPathSpec) error
}

// PathBridge keeps an in-memory map of cameraID -> CameraPathSpec and on
// each cameras.Change rebuilds the slice and calls
// PathManager.ReloadFromCameras with the full set. Disabled cameras are
// dropped from the slice so the path manager stops their paths.
type PathBridge struct {
	svc      *Service
	pm       PathManager
	logger   logger.Writer
	bus      <-chan Change
	unsub    func()
	mu       sync.Mutex
	registry map[string]CameraPathSpec
}

// NewPathBridge wires a PathBridge.
func NewPathBridge(svc *Service, pm PathManager, log logger.Writer) *PathBridge {
	bus, unsub := svc.Subscribe()
	return &PathBridge{
		svc:      svc,
		pm:       pm,
		logger:   log,
		bus:      bus,
		unsub:    unsub,
		registry: make(map[string]CameraPathSpec),
	}
}

// Bootstrap loads every existing enabled camera from the store and
// pushes the initial slice to the path manager. Call before Run.
func (b *PathBridge) Bootstrap(ctx context.Context) error {
	cams, err := b.svc.List(ctx, store.ListCamerasFilter{Limit: 500})
	if err != nil {
		return err
	}
	b.mu.Lock()
	for _, c := range cams {
		if !c.Enabled {
			continue
		}
		spec, err := b.specFor(ctx, c)
		if err != nil {
			if b.logger != nil {
				b.logger.Log(logger.Warn, "[cameras.bridge] specFor %s: %v", c.ID, err)
			}
			continue
		}
		b.registry[c.ID] = spec
	}
	b.mu.Unlock()
	return b.flush(ctx)
}

// Run blocks until ctx is cancelled, applying every Change from the bus.
func (b *PathBridge) Run(ctx context.Context) {
	defer b.unsub()
	for {
		select {
		case <-ctx.Done():
			return
		case ch, ok := <-b.bus:
			if !ok {
				return
			}
			b.apply(ctx, ch)
		}
	}
}

func (b *PathBridge) apply(ctx context.Context, ch Change) {
	b.mu.Lock()
	switch ch.Type {
	case ChangeDeleted:
		delete(b.registry, ch.CameraID)
	case ChangeCreated, ChangeUpdated:
		if ch.Camera == nil || !ch.Camera.Enabled {
			delete(b.registry, ch.CameraID)
		} else {
			spec, err := b.specFor(ctx, ch.Camera)
			if err != nil {
				if b.logger != nil {
					b.logger.Log(logger.Warn, "[cameras.bridge] specFor %s: %v", ch.CameraID, err)
				}
				b.mu.Unlock()
				return
			}
			b.registry[ch.CameraID] = spec
		}
	}
	b.mu.Unlock()
	if err := b.flush(ctx); err != nil && b.logger != nil {
		b.logger.Log(logger.Warn, "[cameras.bridge] flush: %v", err)
	}
}

func (b *PathBridge) specFor(ctx context.Context, cam *store.Camera) (CameraPathSpec, error) {
	url, err := b.svc.MaterializeRTSPURL(ctx, cam.ID)
	if err != nil {
		return CameraPathSpec{}, err
	}
	return CameraPathSpec{Name: cam.Name, SourceURL: url}, nil
}

func (b *PathBridge) flush(ctx context.Context) error {
	b.mu.Lock()
	specs := make([]CameraPathSpec, 0, len(b.registry))
	for _, s := range b.registry {
		specs = append(specs, s)
	}
	b.mu.Unlock()
	// Stable order so test diffs and path-manager diff logic are deterministic.
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	if b.pm == nil {
		return nil
	}
	return b.pm.ReloadFromCameras(ctx, specs)
}
