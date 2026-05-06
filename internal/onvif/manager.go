// Subscription manager. Owns active PullPoint subscriptions: one
// goroutine per subscription that loops {pull, translate, emit, renew}
// until cancelled.
//
// Subscription state lives in-memory only — recorder restart drops all
// active subscriptions. This is intentional for v1: persisting
// subscription URLs across restarts adds complexity (URL is camera-
// scoped state, must be revalidated on resume) and the operator can
// re-subscribe via the SPA after a restart. A persistent-subscription
// follow-up is documented in docs/web-ui.md as a future-slice item.

package onvif

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// SubscriptionRecord is the manager's view of one active subscription.
// Persisted in-memory only.
type SubscriptionRecord struct {
	ID              string    `json:"id"`
	CameraID        string    `json:"camera_id"`
	XAddr           string    `json:"xaddr"`
	CreatedAt       time.Time `json:"created_at"`
	TerminationTime time.Time `json:"termination_time"`
	State           string    `json:"state"` // active | terminated | failed
	LastError       string    `json:"last_error,omitempty"`
	LastEventAt     time.Time `json:"last_event_at,omitempty"`
	EventCount      int       `json:"event_count"`
}

// EventSink is the callback the manager invokes for each parsed
// notification. The recorder wires this to its canonical Event publish
// path.
type EventSink func(ev EventNotification)

// Manager keeps a registry of active subscriptions and runs one
// goroutine per subscription. Construction is cheap; subscriptions
// start when AddSubscription is called.
type Manager struct {
	logger    logger.Writer
	sink      EventSink
	httpClient *http.Client

	// PullTimeout overrides the default per-PullMessages long-poll
	// timeout. Zero = DefaultPullTimeout.
	PullTimeout time.Duration

	// SubscriptionDuration overrides the default subscription window
	// requested at Create / Renew. Zero = DefaultSubscriptionDuration.
	SubscriptionDuration time.Duration

	mu   sync.Mutex
	subs map[string]*subRunner
}

type subRunner struct {
	record *SubscriptionRecord
	sub    *Subscription
	client *PullPointClient
	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager constructs a Manager. Sink is invoked for each event
// (separate goroutine per subscription so a slow sink stalls only its
// own subscription).
func NewManager(log logger.Writer, sink EventSink, httpClient *http.Client) *Manager {
	return &Manager{
		logger:    log,
		sink:      sink,
		httpClient: httpClient,
		subs:      make(map[string]*subRunner),
	}
}

// AddSubscriptionInput carries the fields the API handler collects
// from the operator + camera record.
type AddSubscriptionInput struct {
	CameraID  string
	XAddr     string // event service URL (typically same as device service URL)
	Username  string
	Password  string
}

// AddSubscription creates a PullPoint subscription against the camera
// at XAddr and starts the pull goroutine. Returns the assigned
// subscription id (uuid v4) on success.
func (m *Manager) AddSubscription(ctx context.Context, in AddSubscriptionInput) (*SubscriptionRecord, error) {
	if in.CameraID == "" {
		return nil, fmt.Errorf("camera_id is required")
	}
	if in.XAddr == "" {
		return nil, fmt.Errorf("xaddr is required")
	}

	client := &PullPointClient{
		XAddr:                in.XAddr,
		Username:             in.Username,
		Password:             in.Password,
		HTTPClient:           m.httpClient,
		PullTimeout:          m.PullTimeout,
		SubscriptionDuration: m.SubscriptionDuration,
	}

	sub, err := client.Create(ctx)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	sub.CameraID = in.CameraID

	id := uuid.New().String()
	record := &SubscriptionRecord{
		ID:              id,
		CameraID:        in.CameraID,
		XAddr:           in.XAddr,
		CreatedAt:       time.Now().UTC(),
		TerminationTime: sub.TerminationTime,
		State:           "active",
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runner := &subRunner{
		record: record,
		sub:    sub,
		client: client,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	m.mu.Lock()
	m.subs[id] = runner
	// Snapshot the record under the lock so the caller's returned
	// value can't race with the run-loop's later mutations of the
	// same pointer (the run-loop holds the same lock when it mutates
	// via touch()).
	snap := *record
	m.mu.Unlock()

	go m.run(runCtx, runner)
	return &snap, nil
}

// RemoveSubscription cancels the subscription goroutine and best-effort
// Unsubscribes against the camera.
func (m *Manager) RemoveSubscription(ctx context.Context, id string) error {
	m.mu.Lock()
	runner, ok := m.subs[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("subscription not found")
	}
	delete(m.subs, id)
	m.mu.Unlock()

	runner.cancel()
	// Wait for the run-loop to exit before reading runner.sub — the
	// run-loop mutates runner.sub on the recreate-after-renew-failure
	// path (manager.go ~240), and racing the read here against that
	// write trips -race even though the resulting Unsubscribe call is
	// best-effort.
	<-runner.done
	// Best-effort unsubscribe with its own deadline so a misbehaving
	// camera doesn't block deletion.
	uctx, ucancel := context.WithTimeout(ctx, 10*time.Second)
	defer ucancel()
	_ = runner.client.Unsubscribe(uctx, runner.sub)
	return nil
}

// List returns a snapshot of currently-tracked subscriptions.
func (m *Manager) List() []SubscriptionRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SubscriptionRecord, 0, len(m.subs))
	for _, r := range m.subs {
		out = append(out, *r.record)
	}
	return out
}

// Get returns one subscription by id. ok=false when absent.
func (m *Manager) Get(id string) (SubscriptionRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.subs[id]
	if !ok {
		return SubscriptionRecord{}, false
	}
	return *r.record, true
}

// Close cancels every subscription goroutine. Best-effort
// Unsubscribes are skipped (caller is shutting down).
func (m *Manager) Close() {
	m.mu.Lock()
	for _, r := range m.subs {
		r.cancel()
	}
	runners := make([]*subRunner, 0, len(m.subs))
	for _, r := range m.subs {
		runners = append(runners, r)
	}
	m.subs = make(map[string]*subRunner)
	m.mu.Unlock()
	for _, r := range runners {
		<-r.done
	}
}

// run is the pull-loop goroutine for a single subscription. Loops
// until cancelled via runner.cancel.
func (m *Manager) run(ctx context.Context, runner *subRunner) {
	defer close(runner.done)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Renew if we're inside 30 seconds of expiry.
		if !runner.sub.TerminationTime.IsZero() &&
			time.Until(runner.sub.TerminationTime) < 30*time.Second {
			renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			renewed, err := runner.client.Renew(renewCtx, runner.sub)
			cancel()
			if err != nil {
				m.recordError(runner, fmt.Sprintf("renew failed: %v", err))
				m.log(logger.Warn, "[onvif] subscription %s renew failed: %v", runner.record.ID, err)
				// Try to recreate — if the camera dropped the subscription
				// (e.g., reboot), Create will give us a fresh URL.
				if newSub, err2 := runner.client.Create(ctx); err2 == nil {
					newSub.CameraID = runner.record.CameraID
					runner.sub = newSub
					m.touch(runner, func(r *SubscriptionRecord) {
						r.TerminationTime = newSub.TerminationTime
						r.State = "active"
					})
				} else {
					// Recreate failed too. Sleep and retry.
					select {
					case <-ctx.Done():
						return
					case <-time.After(10 * time.Second):
					}
					continue
				}
			} else {
				runner.sub = renewed
				m.touch(runner, func(r *SubscriptionRecord) {
					r.TerminationTime = renewed.TerminationTime
				})
			}
		}

		events, err := runner.client.Pull(ctx, runner.sub)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.recordError(runner, fmt.Sprintf("pull failed: %v", err))
			m.log(logger.Warn, "[onvif] subscription %s pull failed: %v", runner.record.ID, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, ev := range events {
			ev.SourceCameraID = runner.record.CameraID
			if ev.MessageID == "" {
				ev.MessageID = strings.ReplaceAll(uuid.New().String(), "-", "")
			}
			m.touch(runner, func(r *SubscriptionRecord) {
				r.EventCount++
				r.LastEventAt = time.Now().UTC()
				r.State = "active"
				r.LastError = ""
			})
			if m.sink != nil {
				m.sink(ev)
			}
		}
	}
}

func (m *Manager) recordError(runner *subRunner, msg string) {
	m.touch(runner, func(r *SubscriptionRecord) {
		r.State = "failed"
		r.LastError = msg
	})
}

func (m *Manager) touch(runner *subRunner, fn func(*SubscriptionRecord)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(runner.record)
}

func (m *Manager) log(level logger.Level, format string, args ...any) {
	if m.logger == nil {
		return
	}
	m.logger.Log(level, format, args...)
}
