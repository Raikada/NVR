package onvif

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type recordingLogger struct {
	mu  sync.Mutex
	out []string
}

func (r *recordingLogger) Log(level logger.Level, format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out = append(r.out, format)
}

// fakeCameraServer simulates an ONVIF event service. Returns
// CreatePullPointSubscriptionResponse on first POST (the call has
// "CreatePullPointSubscription" in the body); thereafter returns the
// configured pull-response on each PullMessages call.
func fakeCameraServer(t *testing.T) *httptest.Server {
	calls := 0
	mu := sync.Mutex{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		mu.Lock()
		calls++
		idx := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/soap+xml")
		switch {
		case strings.Contains(s, "CreatePullPointSubscription"):
			// Point the subscription back at this same server so
			// follow-up Pull/Renew/Unsubscribe calls reach the test.
			body := strings.Replace(createPullPointFixture,
				"http://camera.example.com/onvif/Subscription?Idx=42",
				r.Host+"/sub", 1)
			body = strings.Replace(body, r.Host+"/sub",
				"http://"+r.Host+"/sub", 1)
			_, _ = io.WriteString(w, body)
		case strings.Contains(s, "PullMessages"):
			if idx == 2 {
				_, _ = io.WriteString(w, pullMessagesMotionFixture)
			} else {
				_, _ = io.WriteString(w, pullMessagesEmptyFixture)
			}
		case strings.Contains(s, "Unsubscribe"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?><env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><wsnt:UnsubscribeResponse xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"/></env:Body></env:Envelope>`)
		case strings.Contains(s, "Renew"):
			_, _ = io.WriteString(w, renewResponseFixture)
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
		}
	}))
}

func TestManagerAddRemoveSubscription(t *testing.T) {
	srv := fakeCameraServer(t)
	defer srv.Close()

	receivedMu := sync.Mutex{}
	received := []EventNotification{}
	sink := func(ev EventNotification) {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		received = append(received, ev)
	}

	mgr := NewManager(&recordingLogger{}, sink, srv.Client())
	mgr.PullTimeout = 200 * time.Millisecond
	mgr.SubscriptionDuration = 60 * time.Second
	defer mgr.Close()

	rec, err := mgr.AddSubscription(context.Background(), AddSubscriptionInput{
		CameraID: "cam-1",
		XAddr:    srv.URL,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rec.ID)
	require.Equal(t, "cam-1", rec.CameraID)
	require.Equal(t, "active", rec.State)

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
	require.NotEmpty(t, received, "expected at least one event")
	require.Equal(t, "tns1:VideoSource/MotionAlarm", received[0].Topic)
	require.Equal(t, "cam-1", received[0].SourceCameraID)
	receivedMu.Unlock()

	// Subscription is listed.
	list := mgr.List()
	require.Len(t, list, 1)
	require.Equal(t, rec.ID, list[0].ID)

	// Remove + verify deletion.
	require.NoError(t, mgr.RemoveSubscription(context.Background(), rec.ID))
	require.Empty(t, mgr.List())
}

func TestManagerAddSubscription_RequiresCameraIDAndXAddr(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	defer mgr.Close()
	_, err := mgr.AddSubscription(context.Background(), AddSubscriptionInput{})
	require.Error(t, err)
	_, err = mgr.AddSubscription(context.Background(), AddSubscriptionInput{CameraID: "c"})
	require.Error(t, err)
}

func TestManagerRemove_UnknownIDIsError(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	defer mgr.Close()
	err := mgr.RemoveSubscription(context.Background(), "missing")
	require.Error(t, err)
}

func TestManagerGet(t *testing.T) {
	srv := fakeCameraServer(t)
	defer srv.Close()
	mgr := NewManager(nil, func(EventNotification) {}, srv.Client())
	mgr.PullTimeout = 200 * time.Millisecond
	defer mgr.Close()

	rec, err := mgr.AddSubscription(context.Background(), AddSubscriptionInput{
		CameraID: "cam-x", XAddr: srv.URL,
	})
	require.NoError(t, err)
	got, ok := mgr.Get(rec.ID)
	require.True(t, ok)
	require.Equal(t, "cam-x", got.CameraID)

	_, ok = mgr.Get("nonexistent")
	require.False(t, ok)
}
