package updatepoll

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/logger"
)

type nullLogger struct{}

func (nullLogger) Log(_ logger.Level, _ string, _ ...any) {}

// pairedIdentityWithIssuer mirrors camerasync's helper. Builds an
// identity in a temp dir with a fake-CA-signed cert and stamps the
// MS metadata's IssuerURL to point at the test server.
func pairedIdentityWithIssuer(t *testing.T, issuerURL string) *identity.Identity {
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
		IssuerURL:    issuerURL,
		JWKSURL:      issuerURL + "/.well-known/jwks.json",
		RootsURL:     issuerURL + "/.well-known/raikada-roots",
		WebSocketURL: "wss://ms.invalid/v1/ws",
	}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, []identity.PinnedRoot{root}, meta))
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))
	require.True(t, id.IsPaired())
	return id
}

func TestState_PendingDefaultsNil(t *testing.T) {
	s := NewState()
	require.Nil(t, s.Pending())
	require.True(t, s.LastPoll().IsZero())
}

func TestState_SetReturnsCopy(t *testing.T) {
	s := NewState()
	s.set(&PendingUpdate{LifecycleID: "lc-1", State: "approved", Version: "1.2.3"})
	got := s.Pending()
	require.NotNil(t, got)
	require.Equal(t, "lc-1", got.LifecycleID)
	require.False(t, s.LastPoll().IsZero())

	// Mutating the returned copy must not mutate the cache.
	got.LifecycleID = "mutated"
	again := s.Pending()
	require.Equal(t, "lc-1", again.LifecycleID)
}

func TestState_SetNilClears(t *testing.T) {
	s := NewState()
	s.set(&PendingUpdate{LifecycleID: "lc-1"})
	require.NotNil(t, s.Pending())
	s.set(nil)
	require.Nil(t, s.Pending())
}

// TestPoller_PicksApprovedItem covers the happy path: poller GETs the
// MS endpoint, sees an `approved` item, and stashes it in State.
func TestPoller_PicksApprovedItem(t *testing.T) {
	var hits int32
	var recorderID string

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)
	recorderID = id.ID().String()

	mux.HandleFunc("/v1/recording-servers/"+recorderID+"/software-updates/available",
		func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&hits, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":               "lc-applied",
						"manifest_id":      "mf-1",
						"state":            "applied",
						"state_changed_at": "2026-05-06T00:00:00Z",
					},
					{
						"id":               "lc-approved",
						"manifest_id":      "mf-2",
						"state":            "approved",
						"state_changed_at": "2026-05-06T01:00:00Z",
						"manifest": map[string]any{
							"version":           "2.0.0",
							"channel":           "stable",
							"release_notes_url": "https://example.invalid/notes",
						},
					},
				},
			})
		})

	st := NewState()
	p, err := New(Options{
		Identity:     id,
		Logger:       nullLogger{},
		State:        st,
		PollInterval: 50 * time.Millisecond,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	defer p.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st.Pending() != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	pu := st.Pending()
	require.NotNil(t, pu)
	require.Equal(t, "lc-approved", pu.LifecycleID)
	require.Equal(t, "approved", pu.State)
	require.Equal(t, "2.0.0", pu.Version)
	require.Equal(t, "stable", pu.Channel)
	require.Equal(t, "https://example.invalid/notes", pu.ReleaseNotes)
	require.False(t, st.LastPoll().IsZero())
	require.GreaterOrEqual(t, atomic.LoadInt32(&hits), int32(1))
}

// TestPoller_NoApprovedClearsNothing covers the case where the MS
// returns only non-approved items: pending stays nil.
func TestPoller_NoApprovedClearsNothing(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)
	mux.HandleFunc("/v1/recording-servers/"+id.ID().String()+"/software-updates/available",
		func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"id": "lc-applied", "state": "applied"},
				},
			})
		})

	st := NewState()
	p, err := New(Options{
		Identity:     id,
		Logger:       nullLogger{},
		State:        st,
		PollInterval: 50 * time.Millisecond,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	defer p.Stop()

	// Wait long enough for at least one poll; pending should remain nil.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !st.LastPoll().IsZero() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.False(t, st.LastPoll().IsZero(), "expected at least one poll")
	require.Nil(t, st.Pending())
}

// TestPoller_404IsQuiet covers the cross-repo coordination case: MS
// hasn't shipped the recorder-mTLS endpoint yet. Poller stays running
// with no pending update.
func TestPoller_404IsQuiet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)
	st := NewState()
	p, err := New(Options{
		Identity:     id,
		Logger:       nullLogger{},
		State:        st,
		PollInterval: 50 * time.Millisecond,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	defer p.Stop()

	time.Sleep(150 * time.Millisecond)
	require.Nil(t, st.Pending())
}

// TestPoller_StartUnpairedIsNoop verifies an unpaired identity does
// not start the goroutine.
func TestPoller_StartUnpairedIsNoop(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	require.False(t, id.IsPaired())

	st := NewState()
	p, err := New(Options{
		Identity:     id,
		Logger:       nullLogger{},
		State:        st,
		PollInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	p.Start()
	defer p.Stop()

	time.Sleep(100 * time.Millisecond)
	require.Nil(t, st.Pending())
	require.True(t, st.LastPoll().IsZero())
}

// TestPoller_StopIdempotent ensures double-Stop is safe.
func TestPoller_StopIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	id := pairedIdentityWithIssuer(t, server.URL)
	st := NewState()
	p, err := New(Options{
		Identity:     id,
		Logger:       nullLogger{},
		State:        st,
		PollInterval: 50 * time.Millisecond,
		HTTPClient:   server.Client(),
	})
	require.NoError(t, err)

	p.Start()
	time.Sleep(20 * time.Millisecond)
	p.Stop()
	p.Stop()
}
