package events

import (
	"testing"
	"time"
)

func TestBus_FanOut(t *testing.T) {
	b := NewBus()
	c1, u1 := b.Subscribe()
	defer u1()
	c2, u2 := b.Subscribe()
	defer u2()

	b.Publish(Event{ID: "x"})

	for _, c := range []<-chan Event{c1, c2} {
		select {
		case ev := <-c:
			if ev.ID != "x" {
				t.Errorf("got %q", ev.ID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timeout")
		}
	}
}

func TestBus_UnsubscribeDoesNotReceive(t *testing.T) {
	b := NewBus()
	c, u := b.Subscribe()
	u()
	select {
	case _, ok := <-c:
		if ok {
			t.Fatal("expected closed channel after unsub")
		}
	case <-time.After(50 * time.Millisecond):
	}
}
