package amcrestchannel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/vendorevents"
)

// streamHandler emits multipart blocks then holds the connection open
// until the client goes away.
func streamHandler(t *testing.T, blocks []string, hold time.Duration) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/cgi-bin/eventManager.cgi", r.URL.Path)
		require.Equal(t, "attach", r.URL.Query().Get("action"))
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=myboundary")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fl.Flush()
		for _, b := range blocks {
			fmt.Fprintf(w, "--myboundary\r\nContent-Type: text/plain\r\nContent-Length:%d\r\n\r\n%s\r\n", len(b), b)
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(hold):
		}
	}
}

func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u.Host
}

func TestAdapterEmitsMappedEvents(t *testing.T) {
	srv := httptest.NewServer(streamHandler(t, []string{
		"Code=VideoMotion;action=Start;index=0",
		"Code=VideoMotion;action=Stop;index=0",
		"Code=SmartMotionHuman;action=Start;index=0",
		"Heartbeat",
		"Code=CallNoAnswered;action=Pulse;index=0",
	}, 5*time.Second))
	t.Cleanup(srv.Close)

	a := &Adapter{
		Host: hostOf(t, srv),
		Credentials: func(context.Context) (string, string, error) {
			return "admin", "pw", nil
		},
		liveness: 3 * time.Second,
		scheme:   "http",
	}

	var mu sync.Mutex
	var got []vendorevents.NormalizedEvent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, func(ne vendorevents.NormalizedEvent) {
			mu.Lock()
			got = append(got, ne)
			mu.Unlock()
		})
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 3, "Stop + Heartbeat must not emit")
	require.Equal(t, "motion", got[0].TypeID)
	require.Equal(t, "person", got[1].TypeID)
	require.Equal(t, "doorbell", got[2].TypeID)
	require.Contains(t, string(got[0].Payload), "VideoMotion")
}

func TestAdapterDeadStreamTimesOut(t *testing.T) {
	// Server sends nothing at all — no events, no heartbeat.
	srv := httptest.NewServer(streamHandler(t, nil, 10*time.Second))
	t.Cleanup(srv.Close)

	a := &Adapter{
		Host: hostOf(t, srv),
		Credentials: func(context.Context) (string, string, error) {
			return "admin", "pw", nil
		},
		liveness: 200 * time.Millisecond,
		scheme:   "http",
	}

	start := time.Now()
	err := a.Run(context.Background(), func(vendorevents.NormalizedEvent) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "liveness")
	require.Less(t, time.Since(start), 3*time.Second)
}

func TestAdapterHeartbeatKeepsStreamAlive(t *testing.T) {
	// Heartbeats every ~50ms; liveness 300ms → stream survives 1s+.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=myboundary")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := 0; i < 20; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprint(w, "--myboundary\r\nContent-Type: text/plain\r\nContent-Length:9\r\n\r\nHeartbeat\r\n")
			fl.Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	a := &Adapter{
		Host: hostOf(t, srv),
		Credentials: func(context.Context) (string, string, error) {
			return "admin", "pw", nil
		},
		liveness: 300 * time.Millisecond,
		scheme:   "http",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := a.Run(ctx, func(vendorevents.NormalizedEvent) {})
	// The context deadline ends the run, NOT the liveness timeout.
	require.False(t, err != nil && strings.Contains(err.Error(), "liveness"),
		"heartbeats must refresh the liveness deadline, got: %v", err)
}
