// Package crl polls the paired MS's certificate revocation list per
// ADR 0011 D5. When the MS marks a recorder's service-cert as
// revoked (operator-driven decommission via DELETE
// /v1/recording-servers/{id}, or future MS-initiated unpair flows),
// the recorder discovers the revocation by finding its own cert
// fingerprint in the CRL and reacts by clearing its local
// DeviceIdentity — the polling-based realization of pairing-flows.md
// §2.5's MS-initiated unpair semantics.
//
// The recorder ↔ MS WebSocket-driven push path lands in a later
// slice; until then, polling is the bound-staleness contract.
package crl

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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

// Default poll interval — chosen low so revocation propagates
// reasonably quickly during testing. ADR 0011 D5 names 6 hours as
// the production default; operators tune downward in deployments
// with stricter revocation-staleness requirements.
const DefaultPollInterval = 5 * time.Minute

// RevocationCallback is fired when the recorder discovers its own
// cert is in the CRL. The callback is responsible for the recorder-
// side cleanup (ClearIssuedIdentity, pairing-manager reset, mDNS
// refresh, audit emit). Returning an error logs but does not stop
// the poller — the next tick will re-detect the revocation.
type RevocationCallback func(reason string) error

// Poller fetches the CRL from the paired MS on a tick and fires the
// callback when the recorder's own fingerprint shows up. One Poller
// per recorder process.
type Poller struct {
	identity     *identity.Identity
	logger       logger.Writer
	pollInterval time.Duration
	onRevoked    RevocationCallback

	mu       sync.Mutex
	cancelFn context.CancelFunc
	running  bool
	wg       sync.WaitGroup
}

// New constructs a Poller. The poller is dormant until Start is
// called; Start is a no-op when the recorder is unpaired (no MS to
// poll) and Stop is safe to call when the poller isn't running.
func New(id *identity.Identity, log logger.Writer, interval time.Duration, onRevoked RevocationCallback) *Poller {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Poller{
		identity:     id,
		logger:       log,
		pollInterval: interval,
		onRevoked:    onRevoked,
	}
}

// Start begins the polling loop. Idempotent — calling on a running
// poller is a no-op. Calling on an unpaired recorder is a no-op
// (the poller has nothing to do; Start should be re-called when
// pairing transitions to paired).
func (p *Poller) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return
	}
	if !p.identity.IsPaired() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelFn = cancel
	p.running = true
	p.wg.Add(1)
	go p.run(ctx)
	p.logger.Log(logger.Info, "[crl] poller started, interval=%s", p.pollInterval)
}

// Stop halts the polling loop. Idempotent.
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
	tick := time.NewTicker(p.pollInterval)
	defer tick.Stop()

	// Run one poll immediately so a freshly-paired recorder doesn't
	// wait for the first tick.
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
	if !p.identity.IsPaired() {
		return
	}
	meta := p.identity.MSMetadata()
	if meta == nil || meta.IssuerURL == "" {
		// Older pairings (before MS metadata persistence landed) won't
		// have a URL to poll. Nothing to do.
		return
	}
	cert := p.identity.IssuedCert()
	if len(cert) == 0 {
		return
	}
	ownFingerprint, err := certFingerprint(cert)
	if err != nil {
		p.logger.Log(logger.Warn, "[crl] cannot compute own cert fingerprint: %s", err)
		return
	}

	url := strings.TrimRight(meta.IssuerURL, "/") + "/.well-known/raikada-crl"
	revoked, err := p.fetchCRL(ctx, url)
	if err != nil {
		p.logger.Log(logger.Warn, "[crl] fetch %s failed: %s", url, err)
		return
	}

	for _, e := range revoked {
		if strings.EqualFold(e.FingerprintSHA256, ownFingerprint) {
			p.logger.Log(logger.Warn, "[crl] own cert fingerprint %s is revoked: %s",
				ownFingerprint, e.Reason)
			reason := e.Reason
			if reason == "" {
				reason = "ms_initiated_revocation"
			}
			if err := p.onRevoked(reason); err != nil {
				p.logger.Log(logger.Error, "[crl] revocation callback failed: %s", err)
			}
			return
		}
	}
}

// crlEntry mirrors one item in the MS's /.well-known/raikada-crl
// response.
type crlEntry struct {
	FingerprintSHA256 string `json:"fingerprint_sha256"`
	RevokedAt         string `json:"revoked_at"`
	Reason            string `json:"reason"`
}

type crlResponse struct {
	Revoked     []crlEntry `json:"revoked"`
	GeneratedAt string     `json:"generated_at"`
}

func (p *Poller) fetchCRL(ctx context.Context, url string) ([]crlEntry, error) {
	tlsCfg, err := p.buildTLSConfig()
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var parsed crlResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return parsed.Revoked, nil
}

// buildTLSConfig produces a TLS config that validates the MS chain
// against the recorder's pinned roots WITHOUT hostname verification.
//
// Hostname verification is skipped intentionally: the recorder's
// trust anchor for the MS is the pinned root fingerprint per ADR
// 0012 D5, not the hostname. The MS may be reached at multiple
// addresses (LAN IP, hostname.local, Cloud-brokered hostname when
// that lands in a future slice) without re-issuing the service
// cert, so binding trust to hostname would create operational
// brittleness. Chain validation against the pinned root pool is
// the cryptographic guarantee.
//
// We use InsecureSkipVerify + a custom VerifyPeerCertificate that
// builds the chain manually — same posture as the pairing client.
func (p *Poller) buildTLSConfig() (*tls.Config, error) {
	pool := x509.NewCertPool()
	added := 0
	for _, root := range p.identity.PinnedRoots() {
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
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
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
	}, nil
}

// certFingerprint computes the lower-case hex SHA-256 of the cert's
// DER bytes — the same shape the MS uses in service_credentials.
// fingerprint_sha256 (per management/internal/crypto/crypto.go).
func certFingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse cert: %w", err)
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}
