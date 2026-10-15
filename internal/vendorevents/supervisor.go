package vendorevents

import (
	"context"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/logger"
)

const (
	backoffInitial = 1 * time.Second
	backoffMax     = 60 * time.Second
	// backoffResetAfter: a run that survives this long counts as stable
	// and resets the backoff ladder.
	backoffResetAfter = 10 * time.Minute
)

// supervisor runs one camera's adapter with backoff restarts and owns
// the event funnel (sink insert + health touch). Poison events are
// dropped: a sink failure never restarts the adapter.
type supervisor struct {
	cameraID string
	source   string // channel name; becomes Event.Source
	adapter  Adapter
	sink     EventSink
	touch    Toucher
	logger   logger.Writer

	// backoffSleep is swappable for tests. Returning an error aborts
	// the restart loop (context cancellation).
	backoffSleep func(ctx context.Context, d time.Duration) error
	// now is swappable for tests.
	now func() time.Time
}

func newSupervisor(cameraID, source string, adapter Adapter, sink EventSink, touch Toucher, log logger.Writer) *supervisor {
	return &supervisor{
		cameraID:     cameraID,
		source:       source,
		adapter:      adapter,
		sink:         sink,
		touch:        touch,
		logger:       log,
		backoffSleep: sleepCtx,
		now:          time.Now,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// run blocks until ctx cancels, restarting the adapter with exponential
// backoff on error.
func (s *supervisor) run(ctx context.Context) {
	backoff := backoffInitial
	for {
		started := s.now()
		err := s.adapter.Run(ctx, s.emit)
		if ctx.Err() != nil {
			return
		}
		if s.now().Sub(started) >= backoffResetAfter {
			backoff = backoffInitial
		}
		s.log("camera %s channel %s: %v — restarting in %s", s.cameraID, s.source, err, backoff)
		if s.backoffSleep(ctx, backoff) != nil {
			return
		}
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// emit funnels one normalized event: insert + touch. Insert failures
// (unknown type id, closed store) log and drop the single event.
func (s *supervisor) emit(ne NormalizedEvent) {
	occurred := ne.OccurredAt
	if occurred.IsZero() {
		occurred = s.now().UTC()
	}
	severity := ne.Severity
	if severity == "" {
		severity = "info"
	}
	ev := &events.Event{
		CameraID:    s.cameraID,
		TypeID:      ne.TypeID,
		Source:      s.source,
		OccurredAt:  occurred,
		Severity:    severity,
		PayloadJSON: ne.Payload,
	}
	if s.sink != nil {
		if err := s.sink.Insert(context.Background(), ev); err != nil {
			s.log("camera %s channel %s: drop %s event: %v", s.cameraID, s.source, ne.TypeID, err)
		}
	}
	if s.touch != nil {
		s.touch(s.cameraID, occurred)
	}
}

func (s *supervisor) log(format string, args ...any) {
	if s.logger != nil {
		s.logger.Log(logger.Warn, "[vendorevents] "+format, args...)
	}
}
