package cameras

import "sync"

// Bus is the in-process fan-out for camera lifecycle events. Subscribers
// receive every Change via their channel; full channels drop the event
// for that subscriber and increment a drop counter (so a stuck consumer
// only backpressures itself).
type Bus struct {
	mu     sync.Mutex
	subs   []*subscriber
	nextID int
}

type subscriber struct {
	id    int
	ch    chan Change
	drops int64
}

// NewBus returns a fresh in-process camera bus.
func NewBus() *Bus { return &Bus{} }

// Subscribe returns a receive channel and an unsubscribe func. Buffer
// is 64; the path-manager bridge re-reads the full set on every change
// so even if events drop, eventual state is correct.
func (b *Bus) Subscribe() (<-chan Change, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscriber{id: b.nextID, ch: make(chan Change, 64)}
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

// Publish fans c to every subscriber. Non-blocking.
func (b *Bus) Publish(c Change) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		select {
		case s.ch <- c:
		default:
			s.drops++
		}
	}
}
