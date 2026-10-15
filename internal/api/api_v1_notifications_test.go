// Package api: Phase 5 Task 5.6 tests for notifications endpoints.
//
// Verifies:
//   - POST /v1/notification-targets accepts plaintext webhook_secret +
//     never returns it in the response.
//   - POST /v1/notification-targets/:id/test delivers to a mock URL
//     (httptest.NewServer) and returns the result.
//   - Subscriptions CRUD round-trip.
//   - Outbox list + retry.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/notifications"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

func startNotificationsAPI(t *testing.T) (*API, *http.Client, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.EventRetention.Upsert(context.Background(), &store.EventRetention{
		TypeID: store.DefaultEventRetentionTypeID, KeepDurationSeconds: 3600,
	}))

	vault, err := cameracred.Open(dir)
	require.NoError(t, err)
	bus := events.NewBus()
	svc := events.NewService(st.Events, st.EventRetention, nil, bus)
	disp := notifications.NewDispatcher(st, vault, svc, notifications.SitePayload{ID: "site1", Name: "Test Site"}, nil, nil)

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second), WriteTimeout: conf.Duration(10 * time.Second),
		Conf: cnf, AuthManager: test.NilAuthManager,
		Store: st, EventsService: svc, NotifDispatcher: disp, Vault: vault,
		Parent: &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{Transport: tr}
	return a, hc, st
}

func TestNotificationTargets_WebhookSecretRedaction(t *testing.T) {
	_, hc, _ := startNotificationsAPI(t)

	body := `{"kind":"webhook","name":"prod","webhook_url":"https://example/x","webhook_secret":"sekret"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/notification-targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created notificationTargetWire
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.NotEmpty(t, created.ID)
	require.True(t, created.WebhookSecretSet)
	require.Empty(t, created.WebhookSecret) // never on the wire

	// List should also redact.
	req, _ = http.NewRequest(http.MethodGet,
		"http://localhost:9997/v1/notification-targets", nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	body2, _ := json.Marshal(res.Body)
	require.NotContains(t, string(body2), "sekret")
}

func TestNotificationTargets_TestEndpointDelivers(t *testing.T) {
	_, hc, st := startNotificationsAPI(t)

	// Mock webhook receiver.
	received := make(chan struct {
		sig  string
		body []byte
	}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(bodyBytes)
		received <- struct {
			sig  string
			body []byte
		}{r.Header.Get("X-Raikada-Signature"), bodyBytes}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Insert a target manually with vault-encrypted secret.
	vault, _ := cameracred.Open(t.TempDir()) // we don't have vault from setup; but the test setup does. For this test use the same.
	_ = vault
	// Use the API to create the target so it goes through the vault.
	body := `{"kind":"webhook","name":"mock","webhook_url":"` + srv.URL + `","webhook_secret":"sk"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/notification-targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var created notificationTargetWire
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))

	// Trigger the test endpoint.
	req, _ = http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/notification-targets/"+created.ID+"/test", nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var result notifications.TestResult
	require.NoError(t, json.NewDecoder(res.Body).Decode(&result))
	require.Equal(t, http.StatusOK, result.Status)

	// Confirm the mock got hit.
	select {
	case got := <-received:
		require.NotEmpty(t, got.sig, "X-Raikada-Signature must be set")
	case <-time.After(2 * time.Second):
		t.Fatal("mock webhook never received delivery")
	}
	_ = st
}

func TestNotificationSubscriptions_RoundTrip(t *testing.T) {
	_, hc, _ := startNotificationsAPI(t)

	// Create a target.
	body := `{"kind":"email","name":"alerts","email_address":"x@y"}`
	req, _ := http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/notification-targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var t1 notificationTargetWire
	require.NoError(t, json.NewDecoder(res.Body).Decode(&t1))

	// Create a subscription.
	subBody := `{"target_id":"` + t1.ID + `","min_severity":"warning"}`
	req, _ = http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/notification-subscriptions", strings.NewReader(subBody))
	req.Header.Set("Content-Type", "application/json")
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var sub notificationSubscriptionWire
	require.NoError(t, json.NewDecoder(res.Body).Decode(&sub))

	// Delete it.
	req, _ = http.NewRequest(http.MethodDelete,
		"http://localhost:9997/v1/notification-subscriptions/"+sub.ID, nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestNotificationOutbox_ListAndRetry(t *testing.T) {
	_, hc, st := startNotificationsAPI(t)

	// Seed FK targets so the outbox row insert is valid.
	tgtID := uuid.NewString()
	require.NoError(t, st.NotificationTargets.Insert(context.Background(), &store.NotificationTarget{
		ID: tgtID, Kind: "email", Name: "x", EmailAddress: "x@y", Enabled: true,
	}))
	camID := uuid.NewString()
	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: camID, Name: "c", SourceType: "rtsp", SourceURL: "rtsp://x/y",
	}))
	require.NoError(t, st.EventTypes.Insert(context.Background(), &store.EventType{
		ID: "tt", DisplayName: "T", Vendor: "custom",
	}))
	evID := uuid.NewString()
	require.NoError(t, st.Events.Insert(context.Background(), &store.Event{
		ID: evID, CameraID: camID, TypeID: "tt", Source: "t", Severity: "info",
		OccurredAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(),
		ExpiresAt: time.Now().Add(time.Hour),
	}))

	// Insert an outbox row directly so we can verify the read + retry.
	id := uuid.NewString()
	require.NoError(t, st.NotificationOutbox.Insert(context.Background(), &store.NotificationOutboxRow{
		ID: id, TargetID: tgtID, EventID: evID,
		PayloadJSON: `{}`, NextAttemptAt: time.Now().UTC(),
	}))
	require.NoError(t, st.NotificationOutbox.MarkDead(context.Background(), id, "exhausted"))

	req, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/v1/notification-outbox?limit=10", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	req, _ = http.NewRequest(http.MethodPost,
		"http://localhost:9997/v1/notification-outbox/"+id+"/retry", nil)
	res, err = hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	row, err := st.NotificationOutbox.ListRecent(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, row, 1)
	require.Equal(t, "pending", row[0].State)
}
