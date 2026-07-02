package amcrestchannel

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bluenviron/mediamtx/internal/vendorevents"
)

const (
	// defaultLiveness: a stream with no traffic (heartbeats included)
	// for this long is dead — return an error and let the supervisor
	// backoff-restart.
	defaultLiveness = 90 * time.Second
	// heartbeatSeconds asks the camera to emit keepalives on this
	// cadence.
	heartbeatSeconds = 5
)

// Adapter is one camera's Amcrest CGI event channel.
type Adapter struct {
	Host        string // host[:port] of the camera's HTTP CGI service
	Credentials func(ctx context.Context) (username, password string, err error)

	// test seams; zero values mean production defaults.
	liveness time.Duration
	scheme   string
}

// New builds the adapter from a ChannelCamera (vendorevents.AdapterFactory shape).
func New(_ context.Context, cam vendorevents.ChannelCamera) (vendorevents.Adapter, error) {
	if cam.Host == "" {
		return nil, fmt.Errorf("amcrest channel: camera %s has no host: %w", cam.ID, vendorevents.ErrUnsupported)
	}
	if cam.Credentials == nil {
		return nil, fmt.Errorf("amcrest channel: camera %s has no credentials: %w", cam.ID, vendorevents.ErrUnsupported)
	}
	return &Adapter{Host: cam.Host, Credentials: cam.Credentials}, nil
}

// Run implements vendorevents.Adapter: attach, parse blocks, emit
// mapped events, die loudly when the stream goes quiet.
func (a *Adapter) Run(ctx context.Context, emit func(vendorevents.NormalizedEvent)) error {
	username, password, err := a.Credentials(ctx)
	if err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	liveness := a.liveness
	if liveness <= 0 {
		liveness = defaultLiveness
	}
	scheme := a.scheme
	if scheme == "" {
		scheme = "http"
	}

	attachURL := url.URL{
		Scheme: scheme,
		Host:   a.Host,
		Path:   "/cgi-bin/eventManager.cgi",
		RawQuery: url.Values{
			"action":    {"attach"},
			"codes":     {"[All]"},
			"heartbeat": {fmt.Sprintf("%d", heartbeatSeconds)},
		}.Encode(),
	}

	// ResponseHeaderTimeout guards the attach handshake: a camera that
	// accepts TCP but never answers must not hang the channel forever.
	// The body itself is long-lived (liveness watchdog below).
	base := &http.Transport{ResponseHeaderTimeout: 15 * time.Second}
	client := &http.Client{
		Transport: newDigestTransport(username, password, base),
		// No overall timeout: the stream is long-lived. Liveness is
		// enforced by cancelling the request context below.
	}

	// The request context is cancellable by the liveness watchdog —
	// cancelling it is the reliable way to unblock a streaming body
	// read (Body.Close during a concurrent Read is not).
	reqCtx, reqCancel := context.WithCancel(ctx)
	defer reqCancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, attachURL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("attach: HTTP %d", resp.StatusCode)
	}

	// Liveness watchdog: every parsed block (heartbeats included)
	// pushes the deadline out.
	activity := make(chan struct{}, 1)
	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()
	dead := make(chan struct{})
	go func() {
		t := time.NewTimer(liveness)
		defer t.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-activity:
				if !t.Stop() {
					<-t.C
				}
				t.Reset(liveness)
			case <-t.C:
				close(dead)
				reqCancel() // aborts the stream read
				return
			}
		}
	}()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)
	var block []string
	inBlock := false
	flush := func() {
		if len(block) == 0 {
			return
		}
		if ev := parseBlock(block); ev != nil {
			select {
			case activity <- struct{}{}:
			default:
			}
			if ne, ok := mapEvent(ev); ok {
				ne.OccurredAt = time.Now().UTC()
				emit(ne)
			}
		}
		block = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "--") {
			flush()
			inBlock = true
			continue
		}
		if inBlock {
			block = append(block, line)
		}
	}
	flush()

	select {
	case <-dead:
		return fmt.Errorf("liveness: no events or heartbeats for %s", liveness)
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	return fmt.Errorf("stream closed by camera")
}
