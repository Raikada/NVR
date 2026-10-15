package vendorevents

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/events"
)

func TestSelectChannel(t *testing.T) {
	cases := []struct {
		name     string
		override string
		manuf    string
		xaddr    string
		want     string
	}{
		{"override wins over manufacturer", "onvif", "Amcrest", "http://x", "onvif"},
		{"override none disables", "none", "Amcrest", "http://x", "none"},
		{"amcrest by manufacturer", "", "Amcrest", "http://x", "amcrest"},
		{"amcrest case-insensitive", "auto", "AMCREST", "", "amcrest"},
		{"onvif by xaddr", "", "SomeVendor", "http://x", "onvif"},
		{"nothing available", "", "SomeVendor", "", "none"},
		{"explicit amcrest without manufacturer", "amcrest", "", "", "amcrest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectChannel(tc.override, tc.manuf, tc.xaddr)
			require.Equal(t, tc.want, got)
		})
	}
}

// scriptedAdapter emits a fixed set of events then behaves per script.
type scriptedAdapter struct {
	emitOnRun []NormalizedEvent
	runErr    error // returned after emitting (nil = block until ctx done)

	mu   sync.Mutex
	runs int
}

func (s *scriptedAdapter) Run(ctx context.Context, emit func(NormalizedEvent)) error {
	s.mu.Lock()
	s.runs++
	s.mu.Unlock()
	for _, ev := range s.emitOnRun {
		emit(ev)
	}
	if s.runErr != nil {
		return s.runErr
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *scriptedAdapter) runCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

type recordingSink struct {
	mu       sync.Mutex
	events   []*events.Event
	failNext bool
}

func (r *recordingSink) Insert(_ context.Context, ev *events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext {
		r.failNext = false
		return errors.New("sink exploded")
	}
	r.events = append(r.events, ev)
	return nil
}

func (r *recordingSink) all() []*events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*events.Event, len(r.events))
	copy(out, r.events)
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestSupervisorFunnelsEventsAndTouches(t *testing.T) {
	sink := &recordingSink{}
	var touchedMu sync.Mutex
	var touched []string
	occurred := time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)

	ad := &scriptedAdapter{emitOnRun: []NormalizedEvent{
		{TypeID: "motion", Severity: "info", OccurredAt: occurred, Payload: json.RawMessage(`{"code":"VideoMotion"}`)},
		{TypeID: "doorbell", Severity: "info", OccurredAt: occurred},
	}}

	sup := newSupervisor("cam-1", "amcrest", ad, sink, func(id string, at time.Time) {
		touchedMu.Lock()
		touched = append(touched, id)
		touchedMu.Unlock()
	}, nil)
	sup.backoffSleep = func(_ context.Context, _ time.Duration) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)

	waitFor(t, func() bool { return len(sink.all()) == 2 })
	got := sink.all()
	require.Equal(t, "motion", got[0].TypeID)
	require.Equal(t, "cam-1", got[0].CameraID)
	require.Equal(t, "amcrest", got[0].Source)
	require.Equal(t, occurred, got[0].OccurredAt)
	require.Equal(t, "doorbell", got[1].TypeID)
	touchedMu.Lock()
	require.GreaterOrEqual(t, len(touched), 2)
	require.Equal(t, "cam-1", touched[0])
	touchedMu.Unlock()
}

func TestSupervisorBackoffRestartsOnError(t *testing.T) {
	sink := &recordingSink{}
	ad := &scriptedAdapter{runErr: errors.New("stream died")}

	var sleepsMu sync.Mutex
	var sleeps []time.Duration
	sup := newSupervisor("cam-1", "amcrest", ad, sink, nil, nil)
	sup.backoffSleep = func(ctx context.Context, d time.Duration) error {
		sleepsMu.Lock()
		sleeps = append(sleeps, d)
		n := len(sleeps)
		sleepsMu.Unlock()
		if n >= 4 {
			<-ctx.Done() // stop the loop after capturing 4 backoffs
			return ctx.Err()
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { sup.run(ctx); close(done) }()

	waitFor(t, func() bool {
		sleepsMu.Lock()
		defer sleepsMu.Unlock()
		return len(sleeps) >= 4
	})
	cancel()
	<-done

	sleepsMu.Lock()
	defer sleepsMu.Unlock()
	require.Equal(t, []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}, sleeps[:4])
	require.GreaterOrEqual(t, ad.runCount(), 4)
}

func TestSupervisorPoisonEventDropped(t *testing.T) {
	sink := &recordingSink{failNext: true}
	ad := &scriptedAdapter{emitOnRun: []NormalizedEvent{
		{TypeID: "motion"}, {TypeID: "motion"},
	}}
	sup := newSupervisor("cam-1", "onvif", ad, sink, nil, nil)
	sup.backoffSleep = func(_ context.Context, _ time.Duration) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.run(ctx)

	// First insert fails (dropped), second lands; the adapter must not
	// have been restarted by the sink failure.
	waitFor(t, func() bool { return len(sink.all()) == 1 })
	require.Equal(t, 1, ad.runCount())
}
