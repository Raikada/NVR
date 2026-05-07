// Package updatepoll polls the Management Server for software updates
// approved against this recorder, and surfaces the most-recent
// approved-but-not-yet-applied lifecycle row in process-local state.
//
// Wave 6 (recorder@6828c004) shipped MS-pushes-apply only — the
// recorder applies updates the MS pushes via
// /v1/recorder/software-updates/apply, but the operator on the
// recorder's local SPA sees nothing about pending updates. This
// package closes that gap: a goroutine GETs
// `/v1/recording-servers/{id}/software-updates/available` on a
// camerasync-mirroring cadence (default 30s; overridable via conf
// MSPollInterval), filters for state=approved, and exposes the
// pending update through a process-wide State accessor that the
// /v1/recorder/identity handler consumes for its
// `pending_software_update` field.
//
// The poller is dormant pre-pair. Start is a no-op when the recorder
// is unpaired or canonical_source is not yet set; the goroutine
// re-checks each tick so a post-pairing transition picks up without
// an external nudge.
//
// Auth: same posture as internal/camerasync — recorder presents its
// pairing-issued mTLS cert, validates the MS chain against pinned
// roots from identity. The MS-side gating is recorder-mTLS at
// `/v1/recording-servers/{id}/software-updates/available` (mirroring
// cameras-desired-state). If the endpoint returns 404 (MS hasn't
// added it yet), the poller logs once and idles — the recorder side
// is forward-compatible.
package updatepoll

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// DefaultPollInterval mirrors camerasync's default. 30s is the ADR
// 0014-suggested cadence for recorder-side update awareness.
const DefaultPollInterval = 30 * time.Second

// httpTimeout is the per-poll request deadline.
const httpTimeout = 30 * time.Second

// PendingUpdate describes the most-recent approved lifecycle row the
// recorder has discovered via a poll. Mirrors the MS lifecycle wire
// shape, projected down to the fields the recorder actually surfaces.
type PendingUpdate struct {
	LifecycleID    string `json:"lifecycle_id"`
	State          string `json:"state"`
	StateChangedAt string `json:"state_changed_at,omitempty"`
	ManifestID     string `json:"manifest_id,omitempty"`
	Version        string `json:"version,omitempty"`
	Channel        string `json:"channel,omitempty"`
	ReleaseNotes   string `json:"release_notes_url,omitempty"`
}

// State is the process-wide cache of the most-recent poll result.
// Goroutine-safe; safe for the API handler to call from request
// goroutines.
type State struct {
	mu       sync.RWMutex
	pending  *PendingUpdate
	pollTime time.Time
}

// NewState builds an empty State.
func NewState() *State { return &State{} }

// Pending returns the most-recent pending update, or nil if none.
// Returned pointer is a copy; safe to read without further locking.
func (s *State) Pending() *PendingUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pending == nil {
		return nil
	}
	cp := *s.pending
	return &cp
}

// LastPoll returns the time the last successful poll completed (zero
// if none). Used by the SPA to surface staleness.
func (s *State) LastPoll() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pollTime
}

// set replaces the cached pending update. Setting nil clears the
// state (e.g., when the MS revokes approval).
func (s *State) set(p *PendingUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = p
	s.pollTime = time.Now().UTC()
}

// Options bundles constructor inputs.
type Options struct {
	Identity     *identity.Identity
	Logger       logger.Writer
	State        *State
	PollInterval time.Duration
	HTTPClient   *http.Client // optional; built lazily from identity when nil
}

// Poller polls the MS for approved updates.
type Poller struct {
	opts Options

	mu       sync.Mutex
	cancel   context.CancelFunc
	running  bool
	wg       sync.WaitGroup
	warned404 bool
}

// New constructs a Poller.
func New(opts Options) (*Poller, error) {
	if opts.Identity == nil {
		return nil, errors.New("updatepoll: nil identity")
	}
	if opts.Logger == nil {
		return nil, errors.New("updatepoll: nil logger")
	}
	if opts.State == nil {
		return nil, errors.New("updatepoll: nil state")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	return &Poller{opts: opts}, nil
}

// Start begins the poll loop. Idempotent. No-op when the recorder is
// unpaired (the goroutine re-checks each tick once started, but the
// first Start needs a paired identity to enter the running state).
func (p *Poller) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return
	}
	if !p.opts.Identity.IsPaired() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.running = true
	p.wg.Add(1)
	go p.run(ctx)
	p.opts.Logger.Log(logger.Info, "[updatepoll] poller started, interval=%s", p.opts.PollInterval)
}

// Stop halts the poll loop. Idempotent.
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.running = false
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
}

func (p *Poller) run(ctx context.Context) {
	defer p.wg.Done()
	tick := time.NewTicker(p.opts.PollInterval)
	defer tick.Stop()

	// First poll immediately so post-pairing transitions reconcile
	// without waiting a full tick.
	p.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *Poller) pollOnce(ctx context.Context) {
	if !p.opts.Identity.IsPaired() {
		return
	}
	meta := p.opts.Identity.MSMetadata()
	if meta == nil || meta.IssuerURL == "" {
		return
	}
	recorderID := p.opts.Identity.ID().String()
	url := strings.TrimRight(meta.IssuerURL, "/") +
		"/v1/recording-servers/" + recorderID + "/software-updates/available"

	pending, err := p.fetch(ctx, url, recorderID)
	if err != nil {
		// Quiet on the first 404 — the MS may not have shipped the
		// endpoint yet (cross-repo coordination item). Loud after that
		// would just be log spam.
		var notFound *errNotFound
		if errors.As(err, &notFound) {
			if !p.warned404 {
				p.opts.Logger.Log(logger.Warn,
					"[updatepoll] MS endpoint %s returned 404; recorder is forward-compatible — badge will surface once the MS adds the endpoint",
					url)
				p.warned404 = true
			}
			return
		}
		p.opts.Logger.Log(logger.Warn, "[updatepoll] poll %s failed: %s", url, err)
		return
	}
	p.opts.State.set(pending)
}

// errNotFound is a sentinel for 404 responses. Wrapped through
// errors.As so the caller can react quietly the first time.
type errNotFound struct{}

func (e *errNotFound) Error() string { return "endpoint not found (HTTP 404)" }

// availableResponse mirrors the MS-side handleListAvailableUpdates
// response. Only the fields the recorder needs are decoded.
type availableResponse struct {
	Items []availableItem `json:"items"`
}

type availableItem struct {
	ID             string             `json:"id"`
	ManifestID     string             `json:"manifest_id"`
	State          string             `json:"state"`
	StateChangedAt string             `json:"state_changed_at"`
	Manifest       *availableManifest `json:"manifest,omitempty"`
}

type availableManifest struct {
	Version         string `json:"version"`
	Channel         string `json:"channel"`
	ReleaseNotesURL string `json:"release_notes_url"`
}

// fetch executes one GET. Returns the most-recent approved lifecycle
// row projected into PendingUpdate, or nil if the MS reports nothing
// pending.
func (p *Poller) fetch(ctx context.Context, url, recorderID string) (*PendingUpdate, error) {
	client := p.opts.HTTPClient
	if client == nil {
		c, err := p.buildHTTPClient()
		if err != nil {
			return nil, fmt.Errorf("build http client: %w", err)
		}
		client = c
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, &errNotFound{}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var av availableResponse
	if err := json.Unmarshal(body, &av); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Pick the first lifecycle row in state=approved. The MS lists
	// rows in the order the operator approved them; the recorder
	// surfaces the freshest approved row to the SPA. If none are
	// approved (all in `available` / `applied` / `superseded`), the
	// recorder shows nothing — same as today.
	for _, it := range av.Items {
		if it.State == "approved" {
			pu := &PendingUpdate{
				LifecycleID:    it.ID,
				State:          it.State,
				StateChangedAt: it.StateChangedAt,
				ManifestID:     it.ManifestID,
			}
			if it.Manifest != nil {
				pu.Version = it.Manifest.Version
				pu.Channel = it.Manifest.Channel
				pu.ReleaseNotes = it.Manifest.ReleaseNotesURL
			}
			return pu, nil
		}
	}
	return nil, nil
}

// buildHTTPClient mirrors camerasync.Poller.buildHTTPClient — same
// posture, same pinned-root validation. Duplicated rather than
// extracted into a shared helper because the two pollers should be
// loosely coupled (one might evolve independently of the other).
func (p *Poller) buildHTTPClient() (*http.Client, error) {
	id := p.opts.Identity
	certPEM := id.IssuedCert()
	chainPEM := id.IssuingChain()
	if len(certPEM) == 0 {
		return nil, errors.New("no issued cert")
	}
	clientCert, err := buildClientCert(certPEM, chainPEM, id.PrivateKey())
	if err != nil {
		return nil, fmt.Errorf("build client cert: %w", err)
	}
	pool := x509.NewCertPool()
	added := 0
	for _, root := range id.PinnedRoots() {
		if root.CertPEM == "" {
			continue
		}
		if pool.AppendCertsFromPEM([]byte(root.CertPEM)) {
			added++
		}
	}
	if added == 0 {
		return nil, errors.New("no pinned roots to validate against")
	}
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		Certificates:       []tls.Certificate{clientCert},
		InsecureSkipVerify: true, //nolint:gosec — chain validation done in VerifyPeerCertificate
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("no peer cert presented")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("parse leaf: %w", err)
			}
			intermediates := x509.NewCertPool()
			for _, raw := range rawCerts[1:] {
				if c, err := x509.ParseCertificate(raw); err == nil {
					intermediates.AddCert(c)
				}
			}
			_, err = leaf.Verify(x509.VerifyOptions{
				Roots:         pool,
				Intermediates: intermediates,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			})
			return err
		},
	}
	return &http.Client{
		Timeout:   httpTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

func buildClientCert(certPEM, chainPEM []byte, priv any) (tls.Certificate, error) {
	var derChain [][]byte
	rest := certPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			derChain = append(derChain, block.Bytes)
		}
	}
	rest = chainPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			derChain = append(derChain, block.Bytes)
		}
	}
	if len(derChain) == 0 {
		return tls.Certificate{}, errors.New("no certificates in cert pem")
	}
	return tls.Certificate{
		Certificate: derChain,
		PrivateKey:  priv,
	}, nil
}
