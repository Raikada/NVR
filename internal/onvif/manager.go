// Subscription manager. Owns active PullPoint subscriptions: one
// goroutine per subscription that loops {pull, translate, emit, renew}
// until cancelled.
//
// Subscription state is persisted to a recorder-local store via the
// Persister interface (Wave A1; see internal/store/onvif_subscriptions.go
// for the production implementation). On startup the recorder calls
// Rehydrate to reattach goroutines to live cameras; the manager treats
// a nil Persister as "in-memory only" (legacy mode kept for tests).
//
// Persistence is fire-and-forget on the run-loop's hot path: a failed
// UpdateState logs and continues — the goroutine's in-memory state is
// authoritative and a transient SQLite hiccup must not crash the
// pull-loop. Insert (AddSubscription) and Delete (RemoveSubscription)
// errors propagate to the caller because those are operator-initiated
// and the operator should see persistence failures.
//
// Factory-wipe semantics: the recorder DB sits in the identity dir,
// which Wave 7's factory-wipe removes wholesale, so this table is
// auto-cleared on D8 — no separate teardown wiring is needed.

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

// Persister stores subscription rows so the manager can rehydrate
// active subscriptions after a recorder restart. The production
// implementation is internal/store.OnvifSubscriptionsRepo; tests can
// supply an in-memory stub.
//
// All methods take ctx for cancellation; implementations should use
// short-deadline contexts because the run-loop calls UpdateState on
// every event and a slow store would back-pressure the pull-loop.
type Persister interface {
	Insert(ctx context.Context, row PersistedSubscription) error
	UpdateState(ctx context.Context, row PersistedSubscription) error
	Delete(ctx context.Context, id string) error
	ListActive(ctx context.Context) ([]PersistedSubscription, error)
	DeleteTerminated(ctx context.Context) (int, error)
}

// PersistedSubscription is the wire shape passed across the Persister
// boundary. Mirrors SubscriptionRecord plus the subscription URL +
// credentials needed to resume after a restart.
type PersistedSubscription struct {
	ID              string
	CameraID        string
	XAddr           string
	Username        string
	Password        string
	SubscriptionURL string
	TerminationTime time.Time
	CreatedAt       time.Time
	State           string
	LastError       string
	LastEventAt     time.Time
	EventCount      int
}

// Manager keeps a registry of active subscriptions and runs one
// goroutine per subscription. Construction is cheap; subscriptions
// start when AddSubscription is called.
type Manager struct {
	logger    logger.Writer
	sink      EventSink
	httpClient *http.Client
	persister Persister

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
	record   *SubscriptionRecord
	sub      *Subscription
	client   *PullPointClient
	cancel   context.CancelFunc
	done     chan struct{}
	username string
	password string
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

// SetPersister installs a Persister. Call once at construction time;
// switching persisters mid-run is not supported. A nil persister
// disables persistence (in-memory only, legacy mode for tests).
func (m *Manager) SetPersister(p Persister) {
	m.persister = p
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

	// Persist before starting the goroutine. Persistence failure on
	// AddSubscription is a hard error: if we can't durably record
	// the subscription, we shouldn't pretend it survives restart.
	// The camera-side Unsubscribe is best-effort cleanup.
	if m.persister != nil {
		row := PersistedSubscription{
			ID:              id,
			CameraID:        in.CameraID,
			XAddr:           in.XAddr,
			Username:        in.Username,
			Password:        in.Password,
			SubscriptionURL: sub.URL,
			TerminationTime: sub.TerminationTime,
			CreatedAt:       record.CreatedAt,
			State:           "active",
		}
		if err := m.persister.Insert(ctx, row); err != nil {
			uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = client.Unsubscribe(uctx, sub)
			ucancel()
			return nil, fmt.Errorf("persist subscription: %w", err)
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runner := &subRunner{
		record:   record,
		sub:      sub,
		client:   client,
		cancel:   cancel,
		done:     make(chan struct{}),
		username: in.Username,
		password: in.Password,
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

	// Persist the deletion. Logged-and-continued on error: the row
	// is now orphaned but Rehydrate's reattach-or-prune path will
	// clean it up on the next restart, and the operator's
	// in-memory removal succeeded.
	if m.persister != nil {
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := m.persister.Delete(dctx, id); err != nil {
			m.log(logger.Warn, "[onvif] persist delete subscription %s: %v", id, err)
		}
		dcancel()
	}
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
	fn(runner.record)
	snap := *runner.record
	url := runner.sub.URL
	m.mu.Unlock()

	// Persist outside the lock so a slow store doesn't block other
	// subscriptions' touches. Best-effort: the in-memory record is
	// authoritative for the running process; a missed UpdateState
	// just means the next-restart rehydrate uses slightly stale
	// counters. Errors are logged and swallowed.
	if m.persister != nil {
		row := PersistedSubscription{
			ID:              snap.ID,
			CameraID:        snap.CameraID,
			XAddr:           snap.XAddr,
			Username:        runner.username,
			Password:        runner.password,
			SubscriptionURL: url,
			TerminationTime: snap.TerminationTime,
			CreatedAt:       snap.CreatedAt,
			State:           snap.State,
			LastError:       snap.LastError,
			LastEventAt:     snap.LastEventAt,
			EventCount:      snap.EventCount,
		}
		uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := m.persister.UpdateState(uctx, row); err != nil {
			m.log(logger.Debug, "[onvif] persist update subscription %s: %v", snap.ID, err)
		}
		ucancel()
	}
}

// Rehydrate loads persisted subscriptions and restarts a goroutine for
// each one. Called once at recorder startup, after the manager has
// been wired into the API package's package-level handle.
//
// Each rehydrated subscription resumes against the camera-issued
// SubscriptionURL persisted at AddSubscription time. If the camera
// rebooted or the subscription expired during the recorder's downtime,
// the first PullMessages will fail and the existing renew-or-recreate
// fallback in run() will reissue a fresh subscription transparently
// (manager.go run-loop ~240). This is the same path a long-lived
// subscription already takes when a camera glitches.
//
// After loading active rows, terminated rows from prior runs are
// pruned so the table stays bounded. Returns the number of
// subscriptions rehydrated.
func (m *Manager) Rehydrate(ctx context.Context) (int, error) {
	if m.persister == nil {
		return 0, nil
	}
	rows, err := m.persister.ListActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("list active subscriptions: %w", err)
	}
	if _, err := m.persister.DeleteTerminated(ctx); err != nil {
		// Non-fatal: a stale terminated row is harmless until the
		// next prune attempt. Log and continue.
		m.log(logger.Warn, "[onvif] prune terminated subscriptions: %v", err)
	}

	count := 0
	for _, row := range rows {
		client := &PullPointClient{
			XAddr:                row.XAddr,
			Username:             row.Username,
			Password:             row.Password,
			HTTPClient:           m.httpClient,
			PullTimeout:          m.PullTimeout,
			SubscriptionDuration: m.SubscriptionDuration,
		}
		sub := &Subscription{
			URL:             row.SubscriptionURL,
			TerminationTime: row.TerminationTime,
			CameraID:        row.CameraID,
		}
		record := &SubscriptionRecord{
			ID:              row.ID,
			CameraID:        row.CameraID,
			XAddr:           row.XAddr,
			CreatedAt:       row.CreatedAt,
			TerminationTime: row.TerminationTime,
			State:           "active",
			LastError:       row.LastError,
			LastEventAt:     row.LastEventAt,
			EventCount:      row.EventCount,
		}
		runCtx, cancel := context.WithCancel(context.Background())
		runner := &subRunner{
			record:   record,
			sub:      sub,
			client:   client,
			cancel:   cancel,
			done:     make(chan struct{}),
			username: row.Username,
			password: row.Password,
		}
		m.mu.Lock()
		m.subs[row.ID] = runner
		m.mu.Unlock()
		go m.run(runCtx, runner)
		count++
	}
	if count > 0 {
		m.log(logger.Info, "[onvif] rehydrated %d ONVIF subscription(s) from store", count)
	}
	return count, nil
}

func (m *Manager) log(level logger.Level, format string, args ...any) {
	if m.logger == nil {
		return
	}
	m.logger.Log(level, format, args...)
}
