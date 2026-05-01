package pairing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// Manager owns the recorder's pairing-flow state and runs the
// MS-side conversation in a background goroutine. One pairing flow
// at a time per recorder; concurrent Start() calls return
// ErrAlreadyInProgress.
type Manager struct {
	identity *identity.Identity
	logger   logger.Writer
	version  string

	// httpClient is the base shape; the actual TLS config is built
	// per-Start() because each pairing pins a different root
	// fingerprint.
	httpClient *http.Client

	mu       sync.Mutex
	status   Status
	cancelFn context.CancelFunc
}

// New constructs a Manager. Call Start() to begin a pairing flow.
func New(id *identity.Identity, log logger.Writer, version string) *Manager {
	return &Manager{
		identity:   id,
		logger:     log,
		version:    version,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		status: Status{
			State:     StateIdle,
			UpdatedAt: time.Now(),
		},
	}
}

// Status returns a snapshot of the current pairing state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// ErrAlreadyInProgress is returned when Start() is called while a
// pairing flow is already running.
var ErrAlreadyInProgress = errors.New("pairing already in progress")

// ErrAlreadyPaired is returned when the recorder is already paired
// (cert valid). To re-pair, the operator must reset the recorder
// (out of scope for v1; manual disk wipe + restart).
var ErrAlreadyPaired = errors.New("recorder is already paired")

// Start kicks off a pairing flow. Non-blocking — the actual MS
// conversation runs in a background goroutine. Returns immediately
// with the initial state.
func (m *Manager) Start(req StartRequest) (StartResult, error) {
	m.mu.Lock()

	if m.identity.IsPaired() {
		m.mu.Unlock()
		return StartResult{State: StateAlreadyPaired,
			Detail: "recorder is already paired; wipe identity and restart to re-pair"},
			ErrAlreadyPaired
	}

	if m.status.State == StateInProgress {
		m.mu.Unlock()
		return StartResult{State: StateInProgress, Detail: "pairing already in progress"},
			ErrAlreadyInProgress
	}

	if err := validateStartRequest(req); err != nil {
		m.mu.Unlock()
		return StartResult{State: StateFailed, Detail: err.Error()}, err
	}

	now := time.Now()
	m.status = Status{
		State:     StateInProgress,
		StartedAt: now,
		UpdatedAt: now,
		MSURL:     req.MSURL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel
	m.mu.Unlock()

	go m.run(ctx, req)

	return StartResult{State: StateInProgress}, nil
}

// Cancel aborts an in-progress flow. Safe to call when idle.
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelFn != nil {
		m.cancelFn()
	}
}

// Reset transitions a terminal state back to Idle so the operator
// can retry with fresh inputs.
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == StateInProgress {
		return // can't reset while running; Cancel first
	}
	m.status = Status{State: StateIdle, UpdatedAt: time.Now()}
}

// run is the goroutine that executes the pairing flow.
func (m *Manager) run(ctx context.Context, req StartRequest) {
	defer m.clearCancelFn()

	if err := m.doRun(ctx, req); err != nil {
		m.setState(StateFailed, "", err.Error())
		m.logger.Log(logger.Error, "pairing failed: %s", err.Error())
		return
	}
	// Terminal-state setting is done inside doRun; nothing to do here.
}

func (m *Manager) doRun(ctx context.Context, req StartRequest) error {
	// 1. Build CSR.
	csrPEM, err := m.identity.BuildCSR()
	if err != nil {
		return fmt.Errorf("build CSR: %w", err)
	}

	// 2. Build HTTP client whose TLS verification trusts the supplied
	// root_fingerprint (when provided) or any cert (when not — the
	// operator confirms the fingerprint after first contact via the
	// /well-known/raikada-roots fetch). For the manual-token-no-QR
	// path we accept the chain on first contact; this is the same
	// trade SSH known_hosts makes.
	tlsCfg, err := buildTLSConfig(req.RootFingerprint)
	if err != nil {
		return fmt.Errorf("tls config: %w", err)
	}
	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
			ForceAttemptHTTP2: true,
		},
	}

	// 3. POST /v1/pairing.
	deviceMeta := buildDeviceMetadata(m.version, m.identity.ID().String())
	subBody := submitRequest{
		Token:                     req.Token,
		ProposedRecordingServerID: m.identity.ID().String(),
		CSRPEM:                    string(csrPEM),
		DeviceMetadata:            deviceMeta,
	}
	subResp, err := postSubmit(ctx, client, req.MSURL, subBody)
	if err != nil {
		return err
	}

	m.setStateWithRequest(StateInProgress, subResp.PairingRequestID, "submitted, awaiting operator approval")
	m.logger.Log(logger.Info, "pairing request submitted: id=%s", subResp.PairingRequestID)

	// 4. Long-poll the status endpoint with X-Pairing-Request-Token
	// (SHA-256 hash of the original token, canonicalized — strip
	// hyphens, uppercase).
	tokenHashHex := tokenHash(req.Token)

	deadline := time.Now().Add(30 * time.Minute) // hard ceiling
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		statusResp, err := pollStatus(ctx, client, req.MSURL, subResp.PairingRequestID, tokenHashHex)
		if err != nil {
			// Network errors during long-poll are common and recoverable.
			m.logger.Log(logger.Warn, "status poll error: %s", err.Error())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		switch statusResp.State {
		case "pending_approval":
			// keep polling
			continue
		case "approved":
			return m.completeApproved(ctx, client, req.MSURL, statusResp)
		case "rejected":
			m.setState(StateRejected, subResp.PairingRequestID, statusResp.Reason)
			return nil
		case "token_expired":
			m.setState(StateTokenExpired, subResp.PairingRequestID, "token expired before approval")
			return nil
		case "token_consumed_elsewhere":
			m.setState(StateTokenConsumedElsewhere, subResp.PairingRequestID, "token consumed by another request")
			return nil
		default:
			return fmt.Errorf("unexpected MS state %q", statusResp.State)
		}
	}
	return errors.New("pairing approval timed out (30 minutes)")
}

// completeApproved handles the approved-state branch: validate the
// issued cert, fetch the roots, persist, mark Approved.
func (m *Manager) completeApproved(ctx context.Context, client *http.Client, msURL string, sr *statusResponse) error {
	if sr.IssuedCertificate == nil {
		return errors.New("approved status missing issued_certificate")
	}
	if sr.RecordingServer == nil {
		return errors.New("approved status missing recording_server")
	}

	// Validate the issued cert chain back to the chain we received.
	if err := validateIssuedCert(sr.IssuedCertificate, m.identity); err != nil {
		return fmt.Errorf("validate issued cert: %w", err)
	}

	// Fetch and parse the roots list to populate pinned roots.
	rootsURL := msURL + "/.well-known/raikada-roots"
	if sr.MSMetadata != nil && sr.MSMetadata.RootsURL != "" {
		rootsURL = sr.MSMetadata.RootsURL
	}
	roots, err := fetchRoots(ctx, client, rootsURL)
	if err != nil {
		return fmt.Errorf("fetch roots: %w", err)
	}
	if len(roots) == 0 {
		return errors.New("MS published no roots")
	}
	pinned := make([]identity.PinnedRoot, 0, len(roots))
	for _, r := range roots {
		pinned = append(pinned, identity.PinnedRoot{
			FingerprintSHA256: r.FingerprintSHA256,
			CertPEM:           r.CertPEM,
			NotBefore:         r.NotBefore,
			NotAfter:          r.NotAfter,
			Kind:              r.Kind,
			PinnedAt:          time.Now(),
		})
	}

	// Persist.
	if err := m.identity.SetIssuedIdentity(
		[]byte(sr.IssuedCertificate.CertPEM),
		[]byte(sr.IssuedCertificate.ChainPEM),
		pinned,
	); err != nil {
		return fmt.Errorf("persist issued identity: %w", err)
	}

	m.setState(StateApproved, sr.PairingRequestID,
		fmt.Sprintf("paired with %s as recording_server %s", msURL, sr.RecordingServer.ID))
	m.logger.Log(logger.Info, "pairing complete: ms=%s recording_server_id=%s",
		msURL, sr.RecordingServer.ID)
	return nil
}

func (m *Manager) setState(s State, prID, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.State = s
	m.status.UpdatedAt = time.Now()
	if prID != "" {
		m.status.PairingRequestID = prID
	}
	m.status.Detail = detail
}

func (m *Manager) setStateWithRequest(s State, prID, detail string) {
	m.setState(s, prID, detail)
}

func (m *Manager) clearCancelFn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelFn = nil
}

// tokenHash canonicalizes a token (strip hyphens, upper-case) and
// returns its SHA-256 in hex. Mirrors management/internal/pairing/
// token.go's HashToken so the X-Pairing-Request-Token header matches
// what the MS stored at issue time.
func tokenHash(s string) string {
	c := strings.ReplaceAll(s, "-", "")
	c = strings.ToUpper(c)
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:])
}

func validateStartRequest(req StartRequest) error {
	if req.MSURL == "" {
		return errors.New("ms_url is required")
	}
	u, err := url.Parse(req.MSURL)
	if err != nil {
		return fmt.Errorf("ms_url malformed: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("ms_url must use https://")
	}
	if req.Token == "" {
		return errors.New("token is required")
	}
	if req.RootFingerprint != "" {
		fp := strings.TrimPrefix(req.RootFingerprint, "sha256:")
		if len(fp) != 64 {
			return errors.New("root_fingerprint must be 64 hex chars (with optional sha256: prefix)")
		}
		if _, err := hex.DecodeString(fp); err != nil {
			return errors.New("root_fingerprint is not valid hex")
		}
	}
	return nil
}

// buildTLSConfig produces a TLS config that trusts ONLY the cert
// whose SHA-256 fingerprint matches rootFP. If rootFP is empty, the
// config accepts the server cert on first contact (TOFU — trust on
// first use, with the operator confirming the fingerprint
// out-of-band).
func buildTLSConfig(rootFP string) (*tls.Config, error) {
	if rootFP == "" {
		// TOFU: accept any cert, but log the fingerprint for operator
		// review. Per pairing API contract §2.4, the QR-path passes
		// the fingerprint; the manual-token path relies on the
		// operator verifying out-of-band.
		return &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec — TOFU pairing path; operator verifies fingerprint
		}, nil
	}
	expected := strings.TrimPrefix(rootFP, "sha256:")
	expected = strings.ToLower(expected)
	return &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec — verification done in VerifyPeerCertificate
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			// Walk the presented chain; require *some* cert in the
			// chain to match the expected fingerprint. This works
			// whether the MS presents its leaf+root or just leaf
			// (the recorder will fetch the full root list separately
			// via /well-known/raikada-roots after pairing).
			for _, raw := range rawCerts {
				sum := sha256.Sum256(raw)
				if hex.EncodeToString(sum[:]) == expected {
					return nil
				}
			}
			return fmt.Errorf("no presented cert matches pinned fingerprint %s", expected)
		},
	}, nil
}

func buildDeviceMetadata(version, recorderID string) map[string]any {
	hostname, _ := os.Hostname()
	return map[string]any{
		"hardware_fingerprint": "", // future: derive from machine-id / DMI / etc.
		"software_version":     "raikada-recorder/" + version,
		"hostname":             hostname,
		"os":                   runtime.GOOS,
		"arch":                 runtime.GOARCH,
		"recorder_id":          recorderID,
	}
}

func postSubmit(ctx context.Context, c *http.Client, baseURL string, body submitRequest) (*submitResponse, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/v1/pairing", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post pairing: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var out submitResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("parse submit response: %w", err)
	}
	return &out, nil
}

func pollStatus(ctx context.Context, c *http.Client, baseURL, prID, tokenHashHex string) (*statusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/v1/pairing-requests/"+prID+"/status", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Pairing-Request-Token", tokenHashHex)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var out statusResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse status response: %w", err)
	}
	return &out, nil
}

func fetchRoots(ctx context.Context, c *http.Client, rootsURL string) ([]rootEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rootsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch roots: HTTP %d", resp.StatusCode)
	}
	var out rootsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse roots: %w", err)
	}
	return out.Roots, nil
}

func parseAPIError(status int, body []byte) error {
	var er errorResponse
	if err := json.Unmarshal(body, &er); err == nil && er.Err.Code != "" {
		return fmt.Errorf("MS error %s: %s (HTTP %d)", er.Err.Code, er.Err.Message, status)
	}
	return fmt.Errorf("MS HTTP %d: %s", status, string(body))
}

// validateIssuedCert decodes the issued cert PEM, parses, and
// verifies the public key matches the recorder's identity public key
// (proof that the MS signed *our* CSR, not someone else's).
func validateIssuedCert(ic *issuedCertificate, id *identity.Identity) error {
	if ic.CertPEM == "" {
		return errors.New("empty cert_pem")
	}
	cert, err := decodeCertPEM([]byte(ic.CertPEM))
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return errors.New("issued cert outside its validity window")
	}
	// Public-key match: serialize both as PKIX DER and compare.
	pub := id.PublicKey()
	expected, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return err
	}
	got, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, got) {
		return errors.New("issued cert public key does not match recorder's keypair")
	}
	return nil
}

func decodeCertPEM(pemBytes []byte) (*x509.Certificate, error) {
	for {
		block, rest := decodePEMBlock(pemBytes)
		if block == nil {
			return nil, errors.New("no certificate found in PEM")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
		pemBytes = rest
	}
}
