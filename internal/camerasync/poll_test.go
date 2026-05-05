package camerasync

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// nullLogger drops every log line. Tests use it to keep the output
// quiet; the recorder's real logger.Logger satisfies logger.Writer.
type nullLogger struct{}

func (nullLogger) Log(_ logger.Level, _ string, _ ...any) {}

// pairedIdentity opens an identity in a temp dir and seeds it with a
// fake-CA-signed cert so IsPaired() returns true. Returns the
// identity ready for poll-loop tests.
func pairedIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "recorder"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, id.PublicKey(), caKey)
	require.NoError(t, err)

	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chainPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	root := identity.PinnedRoot{
		FingerprintSHA256: "abc",
		CertPEM:           string(chainPEM),
		NotBefore:         caTpl.NotBefore,
		NotAfter:          caTpl.NotAfter,
		Kind:              "ms_self_signed",
		PinnedAt:          time.Now(),
	}
	meta := &identity.MSMetadata{
		IssuerURL:    "https://ms.invalid",
		JWKSURL:      "https://ms.invalid/.well-known/jwks.json",
		RootsURL:     "https://ms.invalid/.well-known/raikada-roots",
		WebSocketURL: "wss://ms.invalid/v1/ws",
	}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, []identity.PinnedRoot{root}, meta))
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))
	require.True(t, id.IsPaired())
	return id
}

// pairedIdentityWithIssuer is like pairedIdentity but takes an
// issuer URL (used to point the poller at a httptest server).
func pairedIdentityWithIssuer(t *testing.T, issuerURL string) *identity.Identity {
	t.Helper()
	id := pairedIdentity(t)
	// Re-stamp metadata with the test server's URL.
	leafPEM := id.IssuedCert()
	chainPEM := id.IssuingChain()
	roots := id.PinnedRoots()
	meta := &identity.MSMetadata{
		IssuerURL:    issuerURL,
		JWKSURL:      issuerURL + "/.well-known/jwks.json",
		RootsURL:     issuerURL + "/.well-known/raikada-roots",
		WebSocketURL: "wss://ms.invalid/v1/ws",
	}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, roots, meta))
	return id
}

func TestPoller_StartGracefulShutdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(DesiredState{
			RecordingServerID: "ignored", // Will be overridden below.
		})
	}))
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)
	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}

	// Match the response's recording_server_id against the recorder's
	// own id by switching the handler now that we have it.
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(DesiredState{
			RecordingServerID: id.ID().String(),
		})
	})

	p, err := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 50 * time.Millisecond,
		Applier:      app,
		AuditEmitter: emit,
		Mu:           &sync.Mutex{},
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	time.Sleep(20 * time.Millisecond) // let the immediate poll fire
	p.Stop()
	// Stop is idempotent.
	p.Stop()
}

func TestPoller_AppliesDesiredState(t *testing.T) {
	var requestCount int32

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	// Build a desired-state for our recorder; we'll capture the
	// recorder id once we have it. Use a closure-captured id var.
	var recorderID string
	mux.HandleFunc("/v1/recording-servers/", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_ = json.NewEncoder(w).Encode(DesiredState{
			RecordingServerID: recorderID,
			VersionSet:        1,
			Cameras: []DesiredStateItem{
				mkItem("cam-1", "front", 1),
				mkItem("cam-2", "back", 1),
			},
		})
	})

	id := pairedIdentityWithIssuer(t, server.URL)
	recorderID = id.ID().String()

	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	p, err := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 50 * time.Millisecond,
		Applier:      app,
		AuditEmitter: emit,
		Mu:           mu,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	defer p.Stop()

	// Wait for at least one apply to land. Acquire the same mutex
	// the apply takes so the read isn't racy with respect to the
	// poll goroutine's mutation.
	addCalls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(app.addCalls)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addCalls() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.GreaterOrEqual(t, addCalls(), 2)
	require.GreaterOrEqual(t, atomic.LoadInt32(&requestCount), int32(1))
}

func TestPoller_SkipsPreImport(t *testing.T) {
	// canonical_source = recorder => poller should not actually GET.
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	// Seed paired (with a cert) but leave canonical_source = recorder.
	pairedID := pairedIdentity(t)
	require.NoError(t, id.SetIssuedIdentity(
		pairedID.IssuedCert(),
		pairedID.IssuingChain(),
		pairedID.PinnedRoots(),
		&identity.MSMetadata{IssuerURL: "https://ms.invalid"},
	))
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceRecorder))

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_ = json.NewEncoder(w).Encode(DesiredState{RecordingServerID: id.ID().String()})
	}))
	defer server.Close()
	// Re-stamp issuer to point at server.
	require.NoError(t, id.SetIssuedIdentity(
		pairedID.IssuedCert(),
		pairedID.IssuingChain(),
		pairedID.PinnedRoots(),
		&identity.MSMetadata{IssuerURL: server.URL},
	))

	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}
	p, err := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 30 * time.Millisecond,
		Applier:      app,
		AuditEmitter: emit,
		Mu:           mu,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)
	p.Start()
	time.Sleep(120 * time.Millisecond)
	p.Stop()

	require.Equal(t, int32(0), atomic.LoadInt32(&requestCount),
		"pre-import recorder must not poll the MS")
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, app.addCalls)
}

func TestPoller_AckCarriesAppliedHighWaterMark(t *testing.T) {
	var firstReqHadBody, secondReqHadBody atomic.Bool
	var firstAck, secondAck struct {
		RecordingServerID string `json:"recording_server_id"`
		AppliedVersion    int64  `json:"applied_version"`
	}

	var calls atomic.Int32
	var captureMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		hasBody := len(body) > 0

		captureMu.Lock()
		switch c {
		case 1:
			firstReqHadBody.Store(hasBody)
			if hasBody {
				_ = json.Unmarshal(body, &firstAck)
			}
		case 2:
			secondReqHadBody.Store(hasBody)
			if hasBody {
				_ = json.Unmarshal(body, &secondAck)
			}
		}
		captureMu.Unlock()

		_ = json.NewEncoder(w).Encode(DesiredState{
			RecordingServerID: r.URL.Path[len("/v1/recording-servers/"):
				len("/v1/recording-servers/")+36],
			VersionSet: 5,
			Cameras: []DesiredStateItem{
				mkItem("cam-1", "front", 5),
			},
		})
	}))
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)

	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	p, err := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 30 * time.Millisecond,
		Applier:      app,
		AuditEmitter: emit,
		Mu:           &sync.Mutex{},
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.Stop()

	require.GreaterOrEqual(t, calls.Load(), int32(2))

	// First request should have no body (we haven't applied
	// anything yet), second request should carry the high-water-mark
	// after the first apply succeeded.
	require.False(t, firstReqHadBody.Load(), "first poll should not include ack body")
	require.True(t, secondReqHadBody.Load(), "second poll should carry the ack body")
	require.Equal(t, id.ID().String(), secondAck.RecordingServerID)
	require.Equal(t, int64(5), secondAck.AppliedVersion)
}

func TestPoller_RejectsMismatchedRecordingServerID(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		// Server returns a wrong id — poller should reject and not
		// invoke the applier.
		_ = json.NewEncoder(w).Encode(DesiredState{
			RecordingServerID: "00000000-0000-0000-0000-000000000000",
			VersionSet:        1,
			Cameras:           []DesiredStateItem{mkItem("cam-1", "front", 1)},
		})
	}))
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)
	app := &fakeApplier{}
	emit := &fakeAuditEmitter{}
	mu := &sync.Mutex{}

	p, err := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 30 * time.Millisecond,
		Applier:      app,
		AuditEmitter: emit,
		Mu:           mu,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)
	p.Start()
	time.Sleep(120 * time.Millisecond)
	p.Stop()

	require.Greater(t, calls.Load(), int32(0))
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, app.addCalls, "applier must not run on mismatched id")
}

func TestPoller_NewValidatesOptions(t *testing.T) {
	// Each missing required field should fail.
	cases := []struct {
		name string
		mod  func(*PollerOptions)
	}{
		{"no identity", func(o *PollerOptions) { o.Identity = nil }},
		{"no logger", func(o *PollerOptions) { o.Logger = nil }},
		{"no applier", func(o *PollerOptions) { o.Applier = nil }},
		{"no audit emitter", func(o *PollerOptions) { o.AuditEmitter = nil }},
		{"no mutex", func(o *PollerOptions) { o.Mu = nil }},
	}

	id := pairedIdentity(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := PollerOptions{
				Identity:     id,
				Logger:       nullLogger{},
				PollInterval: time.Second,
				Applier:      &fakeApplier{},
				AuditEmitter: &fakeAuditEmitter{},
				Mu:           &sync.Mutex{},
			}
			tc.mod(&opts)
			_, err := New(opts)
			require.Error(t, err)
		})
	}
}

func TestPoller_DefaultIntervalApplies(t *testing.T) {
	id := pairedIdentity(t)
	p, err := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 0, // request default
		Applier:      &fakeApplier{},
		AuditEmitter: &fakeAuditEmitter{},
		Mu:           &sync.Mutex{},
	})
	require.NoError(t, err)
	require.Equal(t, DefaultPollInterval, p.opts.PollInterval)
}

func TestPoller_StartIsNoOpWhenUnpaired(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	require.False(t, id.IsPaired())

	p, perr := New(PollerOptions{
		Identity:     id,
		Logger:       nullLogger{},
		PollInterval: 30 * time.Millisecond,
		Applier:      &fakeApplier{},
		AuditEmitter: &fakeAuditEmitter{},
		Mu:           &sync.Mutex{},
	})
	require.NoError(t, perr)
	p.Start() // should be a no-op
	p.Stop()
}
