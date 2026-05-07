package onvif

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// memPersister is an in-memory Persister stub for tests. Mirrors the
// production internal/store implementation but trades durability for
// speed.
type memPersister struct {
	mu      sync.Mutex
	rows    map[string]PersistedSubscription
	insertN int
	updateN int
	deleteN int
	failOn  string // "insert" / "update" / "delete" / "" (none)
}

func newMemPersister() *memPersister {
	return &memPersister{rows: make(map[string]PersistedSubscription)}
}

func (m *memPersister) Insert(_ context.Context, row PersistedSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertN++
	if m.failOn == "insert" {
		return errors.New("simulated insert failure")
	}
	m.rows[row.ID] = row
	return nil
}

func (m *memPersister) UpdateState(_ context.Context, row PersistedSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateN++
	if m.failOn == "update" {
		return errors.New("simulated update failure")
	}
	if _, ok := m.rows[row.ID]; !ok {
		return errors.New("not found")
	}
	m.rows[row.ID] = row
	return nil
}

func (m *memPersister) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteN++
	if m.failOn == "delete" {
		return errors.New("simulated delete failure")
	}
	delete(m.rows, id)
	return nil
}

func (m *memPersister) ListActive(_ context.Context) ([]PersistedSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PersistedSubscription, 0, len(m.rows))
	for _, r := range m.rows {
		if r.State == "active" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memPersister) DeleteTerminated(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.rows {
		if r.State != "active" {
			delete(m.rows, id)
			n++
		}
	}
	return n, nil
}

func (m *memPersister) snapshot() map[string]PersistedSubscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]PersistedSubscription, len(m.rows))
	for k, v := range m.rows {
		out[k] = v
	}
	return out
}

// TestManagerPersistsOnAdd verifies AddSubscription writes a row and
// RemoveSubscription deletes it.
func TestManagerPersistsOnAdd(t *testing.T) {
	srv := fakeCameraServer(t)
	defer srv.Close()

	p := newMemPersister()
	mgr := NewManager(&recordingLogger{}, func(EventNotification) {}, srv.Client())
	mgr.SetPersister(p)
	mgr.PullTimeout = 200 * time.Millisecond
	defer mgr.Close()

	rec, err := mgr.AddSubscription(context.Background(), AddSubscriptionInput{
		CameraID: "cam-A",
		XAddr:    srv.URL,
		Username: "admin",
		Password: "pw",
	})
	require.NoError(t, err)

	rows := p.snapshot()
	require.Len(t, rows, 1)
	row := rows[rec.ID]
	require.Equal(t, "cam-A", row.CameraID)
	require.Equal(t, "admin", row.Username)
	require.Equal(t, "pw", row.Password)
	require.Equal(t, "active", row.State)
	require.NotEmpty(t, row.SubscriptionURL)

	require.NoError(t, mgr.RemoveSubscription(context.Background(), rec.ID))
	require.Empty(t, p.snapshot())
}

// TestManagerAddFailsOnPersistError ensures AddSubscription propagates a
// persistence failure rather than silently leaving an in-memory-only
// subscription.
func TestManagerAddFailsOnPersistError(t *testing.T) {
	srv := fakeCameraServer(t)
	defer srv.Close()

	p := newMemPersister()
	p.failOn = "insert"
	mgr := NewManager(nil, func(EventNotification) {}, srv.Client())
	mgr.SetPersister(p)
	mgr.PullTimeout = 200 * time.Millisecond
	defer mgr.Close()

	_, err := mgr.AddSubscription(context.Background(), AddSubscriptionInput{
		CameraID: "cam-B", XAddr: srv.URL,
	})
	require.Error(t, err)
	require.Empty(t, mgr.List())
}

// TestManagerRehydrateRestartsSubscriptions verifies that a freshly-
// constructed Manager loaded from a Persister with active rows starts
// goroutines for each row and resumes pulling events from the camera.
func TestManagerRehydrateRestartsSubscriptions(t *testing.T) {
	srv := fakeCameraServer(t)
	defer srv.Close()

	// Seed a persister with one active row whose subscription_url
	// points at the fake camera. The Pull endpoint will return one
	// motion event on its first call.
	p := newMemPersister()
	now := time.Now().UTC()
	require.NoError(t, p.Insert(context.Background(), PersistedSubscription{
		ID:              "rehydrated-1",
		CameraID:        "cam-rehydrate",
		XAddr:           srv.URL,
		SubscriptionURL: srv.URL + "/sub",
		TerminationTime: now.Add(5 * time.Minute),
		CreatedAt:       now,
		State:           "active",
	}))
	// Add a stale terminated row that should be pruned.
	require.NoError(t, p.Insert(context.Background(), PersistedSubscription{
		ID:        "stale-1",
		CameraID:  "cam-old",
		XAddr:     srv.URL,
		State:     "failed",
		CreatedAt: now,
	}))

	receivedMu := sync.Mutex{}
	received := []EventNotification{}
	sink := func(ev EventNotification) {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		received = append(received, ev)
	}

	mgr := NewManager(&recordingLogger{}, sink, srv.Client())
	mgr.SetPersister(p)
	mgr.PullTimeout = 200 * time.Millisecond
	mgr.SubscriptionDuration = 60 * time.Second
	defer mgr.Close()

	count, err := mgr.Rehydrate(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// The terminated row should be gone after rehydrate.
	rows := p.snapshot()
	_, hasStale := rows["stale-1"]
	require.False(t, hasStale, "DeleteTerminated should have pruned the failed row")

	// The active row should still be present and listed.
	require.Len(t, mgr.List(), 1)
	require.Equal(t, "rehydrated-1", mgr.List()[0].ID)
	require.Equal(t, "cam-rehydrate", mgr.List()[0].CameraID)

	// Wait for the goroutine to pull at least one event.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		receivedMu.Lock()
		got := len(received)
		receivedMu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	receivedMu.Lock()
	require.NotEmpty(t, received, "expected the rehydrated subscription to receive events")
	require.Equal(t, "cam-rehydrate", received[0].SourceCameraID)
	receivedMu.Unlock()
}

// TestManagerRehydrateNilPersisterIsNoOp covers the legacy in-memory
// path that tests still rely on.
func TestManagerRehydrateNilPersisterIsNoOp(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	defer mgr.Close()
	count, err := mgr.Rehydrate(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, count)
}
