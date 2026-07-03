// Package snapshots captures a JPEG (full + thumbnail) for each event
// whose type opts in (event_types.capture_snapshot, SP4). Fetch ladder:
// Amcrest CGI → ONVIF snapshot URI → live frame grab from the
// recorder's own stream.
package snapshots

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

const (
	fetchTimeout = 5 * time.Second
	// burstWindow: events for the same camera arriving within this
	// window share one fetched JPEG (motion bursts must not hammer the
	// camera).
	burstWindow = 2 * time.Second
	// queueSize bounds the work queue; overflow drops the oldest so the
	// events bus is never back-pressured.
	queueSize = 256
	// flagsTTL caches the capture_snapshot flag table.
	flagsTTL = 60 * time.Second
	// thumbWidth is the thumbnail target width (aspect preserved).
	thumbWidth = 320
)

// CameraInfo is what the fetch ladder needs to reach a camera.
type CameraInfo struct {
	ID          string
	Name        string
	Host        string // vendor HTTP host (hostname, default port)
	SnapshotURI string // from the SP2 capability probe; may be empty
	Channel     string // resolved vendor channel ("amcrest" enables the CGI rung)
	Credentials func(ctx context.Context) (username, password string, err error)
}

// Resolver turns a camera id into CameraInfo (core wiring supplies it).
type Resolver func(ctx context.Context, cameraID string) (CameraInfo, error)

// FrameGrabber captures a JPEG from the recorder's own live stream —
// the ladder's last rung, no camera round-trip.
type FrameGrabber func(ctx context.Context, cameraName string) ([]byte, error)

// EventSource abstracts events.Service.Subscribe.
type EventSource interface {
	Subscribe() (<-chan events.Event, func())
}

// burstEntry is a recently-fetched JPEG for reuse inside burstWindow.
type burstEntry struct {
	at   time.Time
	data []byte
}

// Service consumes the events bus and captures snapshots.
type Service struct {
	store   *store.Store
	source  EventSource
	resolve Resolver
	grab    FrameGrabber
	root    func() string
	logger  logger.Writer
	clock   func() time.Time

	mu     sync.Mutex
	bursts map[string]burstEntry // camera id → last fetch

	flagsMu  sync.Mutex
	flags    map[string]bool
	flagsAt  time.Time
}

// New wires a Service. source may be nil when the caller drives handle
// directly (tests); logger may be nil.
func New(st *store.Store, source EventSource, resolve Resolver, grab FrameGrabber,
	root func() string, log logger.Writer,
) *Service {
	return &Service{
		store:   st,
		source:  source,
		resolve: resolve,
		grab:    grab,
		root:    root,
		logger:  log,
		clock:   time.Now,
		bursts:  map[string]burstEntry{},
	}
}

// Run consumes the events bus until ctx cancels. A bounded queue
// decouples capture latency from bus delivery; overflow drops the
// oldest queued event with a warning.
func (s *Service) Run(ctx context.Context) {
	if s.source == nil {
		return
	}
	ch, unsub := s.source.Subscribe()
	defer unsub()

	queue := make(chan events.Event, queueSize)
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(queue)
				return
			case ev, ok := <-ch:
				if !ok {
					close(queue)
					return
				}
				select {
				case queue <- ev:
				default:
					select {
					case dropped := <-queue:
						s.log("queue full: dropped snapshot for event %s", dropped.ID)
					default:
					}
					select {
					case queue <- ev:
					default:
					}
				}
			}
		}
	}()

	for ev := range queue {
		s.handle(ctx, ev)
	}
}

// handle captures the snapshot pair for one event. Failures log and
// return: no snapshot ≠ no event.
func (s *Service) handle(ctx context.Context, ev events.Event) {
	if ev.CameraID == "" || !s.shouldCapture(ctx, ev.TypeID) {
		return
	}
	info, err := s.resolve(ctx, ev.CameraID)
	if err != nil {
		s.log("resolve camera %s: %v", ev.CameraID, err)
		return
	}

	data, err := s.fetchWithBurstReuse(ctx, info)
	if err != nil {
		s.log("fetch snapshot camera %s event %s: %v", ev.CameraID, ev.ID, err)
		return
	}

	now := s.clock()
	dir := filepath.Join(s.root(), info.Name, now.UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.log("snapshot dir %s: %v", dir, err)
		return
	}

	fullPath := filepath.Join(dir, ev.ID+"-full.jpg")
	fullW, fullH, err := writeJPEG(fullPath, data)
	if err != nil {
		s.log("write %s: %v", fullPath, err)
		return
	}
	s.insertRow(ctx, ev.ID, "full", fullPath, fullW, fullH, int64(len(data)), now)

	thumb, tw, th, err := makeThumbnail(data, thumbWidth)
	if err != nil {
		s.log("thumbnail event %s: %v", ev.ID, err)
		return
	}
	thumbPath := filepath.Join(dir, ev.ID+"-thumb.jpg")
	if _, _, err := writeJPEG(thumbPath, thumb); err != nil {
		s.log("write %s: %v", thumbPath, err)
		return
	}
	s.insertRow(ctx, ev.ID, "thumb", thumbPath, tw, th, int64(len(thumb)), now)
}

func (s *Service) insertRow(ctx context.Context, eventID, kind, path string, w, h int, size int64, now time.Time) {
	err := s.store.EventSnapshots.Insert(ctx, &store.EventSnapshot{
		EventID: eventID, Kind: kind, Width: w, Height: h,
		Path: path, SizeBytes: size, FetchedAt: now,
	})
	if err != nil {
		s.log("event_snapshots insert %s/%s: %v", eventID, kind, err)
	}
}

// fetchWithBurstReuse returns a fresh or recently-fetched JPEG for the
// camera.
func (s *Service) fetchWithBurstReuse(ctx context.Context, info CameraInfo) ([]byte, error) {
	now := s.clock()
	s.mu.Lock()
	if e, ok := s.bursts[info.ID]; ok && now.Sub(e.at) <= burstWindow {
		data := e.data
		s.mu.Unlock()
		return data, nil
	}
	s.mu.Unlock()

	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	data, err := s.fetch(fctx, info)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.bursts[info.ID] = burstEntry{at: now, data: data}
	s.mu.Unlock()
	return data, nil
}

// shouldCapture consults the cached capture_snapshot flag table.
func (s *Service) shouldCapture(ctx context.Context, typeID string) bool {
	s.flagsMu.Lock()
	defer s.flagsMu.Unlock()
	if s.flags == nil || s.clock().Sub(s.flagsAt) > flagsTTL {
		types, err := s.store.EventTypes.List(ctx)
		if err != nil {
			s.log("event types list: %v", err)
			return false
		}
		s.flags = make(map[string]bool, len(types))
		for _, t := range types {
			s.flags[t.ID] = t.CaptureSnapshot
		}
		s.flagsAt = s.clock()
	}
	capture, known := s.flags[typeID]
	return known && capture
}

func (s *Service) log(format string, args ...any) {
	if s.logger != nil {
		s.logger.Log(logger.Warn, "[snapshots] "+format, args...)
	}
}

// writeJPEG persists data and returns its decoded dimensions.
func writeJPEG(path string, data []byte) (int, int, error) {
	w, h, err := jpegDims(data)
	if err != nil {
		return 0, 0, fmt.Errorf("not a decodable JPEG: %w", err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return 0, 0, err
	}
	return w, h, nil
}
