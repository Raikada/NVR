// Package api: in-memory Event ring buffer per ADR 0009 §D5 Events.
//
// The recorder emits canonical Events for camera state changes,
// segment-write failures, storage failures, and auth decisions today
// only as logger.Writer calls; there is no structured Event store. ADR
// 0009 §D5 exposes Event over /v1/events, so this file provides a
// minimal in-memory ring buffer the API can serve from.
//
// Phase 2D scope: ship the buffer + handler. The buffer starts empty.
// Phase-2-followup wires producers (camera-state hooks, segment-write
// error paths, auth decisions) to publish into this buffer via
// EventStore.Publish.
//
// The buffer is intentionally coarse: a fixed-size slice guarded by a
// mutex, oldest-first eviction. Cross-restart persistence is out of
// scope; canonical Event durability lives at the Cloud projection per
// service-boundaries.md.
package api //nolint:revive

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// defaultEventStoreCapacity is the in-memory ring buffer's fixed
// capacity. 1024 is a deliberate compromise: enough to cover a normal
// operating window of camera-online/offline transitions plus error
// bursts, small enough that the buffer's footprint stays bounded.
const defaultEventStoreCapacity = 1024

// EventStore is the recorder's in-memory ring buffer of canonical
// Events. The buffer holds the most recent N events and evicts older
// entries on overflow.
//
// All exported methods are safe for concurrent use.
type EventStore struct {
	mu       sync.RWMutex
	capacity int
	// items is kept newest-last. Get-by-id is linear, which is fine for
	// a 1024-entry ring; if we grow the capacity past a few thousand a
	// secondary id index becomes worth it.
	items []defs.Event
}

// NewEventStore builds an EventStore with the given capacity. Capacity
// <= 0 falls back to defaultEventStoreCapacity.
func NewEventStore(capacity int) *EventStore {
	if capacity <= 0 {
		capacity = defaultEventStoreCapacity
	}
	return &EventStore{
		capacity: capacity,
		items:    make([]defs.Event, 0, capacity),
	}
}

// Publish appends an event to the buffer, evicting the oldest entry
// when at capacity. Mirrors the contract a future producer-side wiring
// will call (see Phase-2-followup notes in event_store.go header).
func (s *EventStore) Publish(in defs.EventInput, recordingServerID, tenantID, siteID string) defs.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New().String()
	e := defs.BuildEvent(in, id, recordingServerID, tenantID, siteID)
	if len(s.items) >= s.capacity {
		// Evict oldest. Copy is O(n) but n is bounded by capacity (1024
		// by default); this is fine for the expected event rate.
		copy(s.items, s.items[1:])
		s.items = s.items[:len(s.items)-1]
	}
	s.items = append(s.items, e)
	return e
}

// Snapshot returns a copy of the buffer's current contents in oldest-
// first order. Callers may filter, sort, and paginate the snapshot
// without holding the store's lock.
func (s *EventStore) Snapshot() []defs.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]defs.Event, len(s.items))
	copy(out, s.items)
	return out
}

// GetByID returns the event with the given id, or false if absent.
func (s *EventStore) GetByID(id string) (defs.Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.items {
		if s.items[i].ID == id {
			return s.items[i], true
		}
	}
	return defs.Event{}, false
}

// Len returns the current number of buffered events.
func (s *EventStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// Clear empties the buffer. Useful in tests.
func (s *EventStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = s.items[:0]
}

// eventStoreSingleton is the default process-wide store used when an
// API caller doesn't inject one. The orchestrator may replace it via
// the API.EventStore field; the handlers check API.EventStore first
// and fall back to this singleton.
var (
	eventStoreSingletonOnce sync.Once
	eventStoreSingleton     *EventStore
)

// defaultEventStore returns the process-wide EventStore, lazily
// constructed on first use. The singleton lets handlers serve a
// coherent (empty) list even when the API host hasn't injected its
// own store yet.
func defaultEventStore() *EventStore {
	eventStoreSingletonOnce.Do(func() {
		eventStoreSingleton = NewEventStore(defaultEventStoreCapacity)
	})
	return eventStoreSingleton
}

// nowUTC is a swappable time source for tests. Production uses
// time.Now().UTC(); tests can override.
var nowUTC = func() time.Time { return time.Now().UTC() }
