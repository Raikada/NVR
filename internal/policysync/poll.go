// Package policysync — poll loop for slice 4-C per ADR 0017 D2.
//
// The Poller is a background goroutine that GETs the MS's
// /v1/recording-servers/{id}/recording-policies-desired-state
// endpoint at a fixed cadence (default 30 seconds, configurable via
// MSPollInterval), feeds the response to Apply, and on success
// advances the recorder's applied_version high-water-mark which
// rides as an ACK on the next poll request body per
// recording-policy-canonical-push.md §4.2.
//
// Mirrors the slice-4-B internal/camerasync.Poller shape verbatim;
// the only differences are the URL path (recording-policies-desired-
// state instead of cameras-desired-state) and the lockdown flag the
// goroutine reads (PolicyCanonicalSource() instead of
// CanonicalSource()).
package policysync

import (
	"bytes"
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

// DefaultPollInterval matches ADR 0017 D2 (inheriting ADR 0016 D2's
// 30s default).
const DefaultPollInterval = 30 * time.Second

// httpTimeout is the per-poll request deadline. Generous compared to
// the MS push timeout (5s per recording-policy-canonical-push.md
// §10) because the response surfaces the full per-recorder policy
// set.
const httpTimeout = 30 * time.Second

// PollerOptions bundles the constructor inputs.
type PollerOptions struct {
	Identity     *identity.Identity
	Logger       logger.Writer
	PollInterval time.Duration
	Applier      Applier
	AuditEmitter AuditEmitter
	// Mu is the same mutex the push-direction handlers (in
	// internal/api) hold during conf mutation. The poll-direction
	// apply takes it during its diff+mutate sequence so the two
	// directions cannot interleave. Passed as sync.Locker.
	Mu sync.Locker
	// HTTPClient is optional; when nil the Poller builds a default
	// client whose TLS layer presents the recorder's pairing-issued
	// cert and validates the MS chain against the recorder's pinned
	// roots (per recording-policy-canonical-push.md §6.2). Same
	// posture as the camerasync.Poller.
	HTTPClient *http.Client
}

// Poller polls the MS for desired-state and applies updates.
type Poller struct {
	opts             PollerOptions
	mu               sync.Mutex
	cancelFn         context.CancelFunc
	running          bool
	wg               sync.WaitGroup
	appliedHighWater int64
}

// New constructs a Poller. The Poller is dormant until Start; Start is
// a no-op when the recorder is unpaired or policy_canonical_source !=
// ms.
func New(opts PollerOptions) (*Poller, error) {
	if opts.Identity == nil {
		return nil, errors.New("policysync: nil identity")
	}
	if opts.Logger == nil {
		return nil, errors.New("policysync: nil logger")
	}
	if opts.Applier == nil {
		return nil, errors.New("policysync: nil applier")
	}
	if opts.AuditEmitter == nil {
		return nil, errors.New("policysync: nil audit emitter")
	}
	if opts.Mu == nil {
		return nil, errors.New("policysync: nil mutex")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	return &Poller{opts: opts}, nil
}

// Start begins the poll loop. Idempotent — calling on a running
// poller is a no-op. Calling on an unpaired recorder is also a no-op.
//
// Start does NOT gate on policy_canonical_source. The goroutine
// itself re-checks the source each tick: pre-import recorders do not
// poll, so a recorder that has paired but not yet been imported by
// the MS for policies stays quiet (the MS auto-import path flips
// policy_canonical_source to `ms` and the next tick picks it up).
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
	p.cancelFn = cancel
	p.running = true
	p.wg.Add(1)
	go p.run(ctx)
	p.opts.Logger.Log(logger.Info, "[policysync] poller started, interval=%s", p.opts.PollInterval)
}

// Stop halts the poll loop. Idempotent.
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel := p.cancelFn
	p.cancelFn = nil
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

	// Run one poll immediately so the post-pairing transition
	// reconciles without waiting for the first tick.
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

// pollOnce performs a single poll-then-apply cycle. Errors are
// logged and swallowed; the next tick retries.
func (p *Poller) pollOnce(ctx context.Context) {
	if !p.opts.Identity.IsPaired() {
		return
	}
	if p.opts.Identity.PolicyCanonicalSource() != identity.CanonicalSourceMS {
		// Pre-import: the MS hasn't taken canonical authority for this
		// recorder's RecordingPolicy yet. Skip; the next tick re-checks.
		return
	}
	meta := p.opts.Identity.MSMetadata()
	if meta == nil || meta.IssuerURL == "" {
		return
	}

	recorderID := p.opts.Identity.ID().String()
	url := strings.TrimRight(meta.IssuerURL, "/") +
		"/v1/recording-servers/" + recorderID + "/recording-policies-desired-state"

	state, err := p.fetchDesiredState(ctx, url, recorderID)
	if err != nil {
		p.opts.Logger.Log(logger.Warn, "[policysync] poll %s failed: %s", url, err)
		return
	}

	report := Apply(ctx, p.opts.Applier, p.opts.AuditEmitter, p.opts.Mu, *state)

	hadFailure := false
	for _, it := range report.Items {
		if it.Outcome == OutcomeFailed {
			hadFailure = true
			break
		}
	}
	if !hadFailure && state.VersionSet > p.appliedHighWater {
		p.appliedHighWater = state.VersionSet
	}

	if changedCount(report) > 0 {
		p.opts.Logger.Log(logger.Info,
			"[policysync] applied desired-state version=%d: %s",
			state.VersionSet, summarizeCounts(report.Counts()))
	}
}

// fetchDesiredState issues the GET, optionally including the ACK body
// when we have a non-zero applied high-water-mark to report.
func (p *Poller) fetchDesiredState(ctx context.Context, url, recorderID string) (*DesiredState, error) {
	client := p.opts.HTTPClient
	if client == nil {
		c, err := p.buildHTTPClient()
		if err != nil {
			return nil, fmt.Errorf("build http client: %w", err)
		}
		client = c
	}

	var body io.Reader
	if p.appliedHighWater > 0 {
		ack := struct {
			RecordingServerID string `json:"recording_server_id"`
			AppliedVersion    int64  `json:"applied_version"`
		}{
			RecordingServerID: recorderID,
			AppliedVersion:    p.appliedHighWater,
		}
		ackBytes, err := json.Marshal(ack)
		if err != nil {
			return nil, fmt.Errorf("marshal ack: %w", err)
		}
		body = bytes.NewReader(ackBytes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var state DesiredState
	if err := json.Unmarshal(respBody, &state); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if state.RecordingServerID != recorderID {
		return nil, fmt.Errorf("recording_server_id mismatch: want %s got %s",
			recorderID, state.RecordingServerID)
	}
	return &state, nil
}

// buildHTTPClient constructs an *http.Client whose TLS layer presents
// the recorder's pairing-issued mTLS cert and validates the MS chain
// against the pinned roots per recording-policy-canonical-push.md
// §6.2. Same posture as the slice-4-B Poller (camerasync/poll.go).
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

// buildClientCert assembles a tls.Certificate from PEM bytes and the
// recorder's private key. Mirrors the slice-4-B helper.
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
	leaf, err := x509.ParseCertificate(derChain[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse leaf: %w", err)
	}
	return tls.Certificate{
		Certificate: derChain,
		PrivateKey:  priv,
		Leaf:        leaf,
	}, nil
}

// changedCount returns the count of items in the report that produced
// a state change (everything except OutcomeUnchanged).
func changedCount(r AppliedReport) int {
	n := 0
	for _, it := range r.Items {
		if it.Outcome != OutcomeUnchanged {
			n++
		}
	}
	return n
}

// summarizeCounts produces a stable "added=N updated=M deleted=K"
// string for the periodic apply log line.
func summarizeCounts(c map[AppliedOutcome]int) string {
	order := []AppliedOutcome{
		OutcomeAdded, OutcomeUpdated, OutcomeDeleted,
		OutcomeVersionRegression, OutcomeFailed, OutcomeUnchanged,
	}
	parts := []string{}
	for _, k := range order {
		if v := c[k]; v > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, v))
		}
	}
	if len(parts) == 0 {
		return "no_changes"
	}
	return strings.Join(parts, " ")
}
