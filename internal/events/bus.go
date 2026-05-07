package events

import "sync"

// Bus is an in-process fan-out for newly-inserted events. Subscribers
// receive every event via their channel; a slow subscriber backpressures
// only itself (channel buffer = 256, beyond which events are dropped for
// that subscriber and a counter is incremented).
type Bus struct {
	mu     sync.Mutex
	subs   []*subscriber
	nextID int
}

type subscriber struct {
	id    int
	ch    chan Event
	drops int64
}

// NewBus returns a fresh in-process event bus.
func NewBus() *Bus { return &Bus{} }

// Subscribe returns a receive channel and an unsubscribe func. Buffer
// is 256; lossless for any sane subscriber, lossy for stuck ones.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscriber{id: b.nextID, ch: make(chan Event, 256)}
	b.nextID++
	b.subs = append(b.subs, s)
	id := s.id
	return s.ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, x := range b.subs {
			if x.id == id {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(x.ch)
				return
			}
		}
	}
}

// Publish fans ev to every subscriber. Non-blocking: a full subscriber
// channel drops the event for that subscriber and increments drops.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			s.drops++
		}
	}
}
