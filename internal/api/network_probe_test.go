package api //nolint:revive

import (
	"errors"
	"net"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNetworkProbeSampleReachable stands up an httptest server (its
// TCP listener is enough for our probe) and confirms Sample reports
// it reachable. The cloud endpoint is left unset and must report
// false-by-default.
func TestNetworkProbeSampleReachable(t *testing.T) {
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)

	p := newNetworkProbe()
	got := p.Sample(srv.URL, "")
	require.True(t, got.ManagementServerReachable, "expected MS reachable: %s", srv.URL)
	require.False(t, got.CloudReachable, "expected cloud unreachable when unset")
	require.Nil(t, got.LastSyncAt, "stub does not populate last_sync_at")
}

// TestNetworkProbeSampleUnreachable points at a known-closed port
// (the host part of an httptest server, but we close it before
// probing).
func TestNetworkProbeSampleUnreachable(t *testing.T) {
	srv := httptest.NewServer(nil)
	endpoint := srv.URL
	srv.Close()

	p := newNetworkProbe()
	// Shorten dial timeout via stub so the test stays fast on
	// platforms where the OS waits the full timeout for a closed
	// port. We simulate the dial directly.
	p.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	got := p.Sample(endpoint, "")
	require.False(t, got.ManagementServerReachable, "expected MS unreachable after server close")
	require.False(t, got.CloudReachable)
}

// TestNetworkProbeBothUnsetAllFalse confirms that with no endpoints
// configured the probe reports the conservative all-false default
// without dialing.
func TestNetworkProbeBothUnsetAllFalse(t *testing.T) {
	p := newNetworkProbe()
	dialCount := int32(0)
	p.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		atomic.AddInt32(&dialCount, 1)
		return nil, errors.New("should not be called")
	}
	got := p.Sample("", "")
	require.False(t, got.ManagementServerReachable)
	require.False(t, got.CloudReachable)
	require.Nil(t, got.LastSyncAt)
	require.Equal(t, int32(0), atomic.LoadInt32(&dialCount), "probe must not dial when endpoints unset")
}

// TestNetworkProbeCaches confirms that two Sample calls within the
// cache TTL produce only one round of dials.
func TestNetworkProbeCaches(t *testing.T) {
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)

	p := newNetworkProbe()
	dialCount := int32(0)
	p.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		atomic.AddInt32(&dialCount, 1)
		return nil, errors.New("ignored — count is what matters")
	}

	_ = p.Sample(srv.URL, srv.URL)
	first := atomic.LoadInt32(&dialCount)
	require.Equal(t, int32(2), first, "first call dials both endpoints")

	_ = p.Sample(srv.URL, srv.URL)
	second := atomic.LoadInt32(&dialCount)
	require.Equal(t, first, second, "second call within TTL must use cache")
}

// TestNetworkProbeCacheExpiry confirms results refresh after the TTL.
// We drive time via the now hook rather than sleeping.
func TestNetworkProbeCacheExpiry(t *testing.T) {
	p := newNetworkProbe()
	dialCount := int32(0)
	p.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		atomic.AddInt32(&dialCount, 1)
		return nil, errors.New("ignored")
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := t0
	p.now = func() time.Time { return current }

	_ = p.Sample("https://ms.example.com", "")
	require.Equal(t, int32(1), atomic.LoadInt32(&dialCount))

	// Advance past the TTL.
	current = t0.Add(networkProbeCacheTTL + time.Second)
	_ = p.Sample("https://ms.example.com", "")
	require.Equal(t, int32(2), atomic.LoadInt32(&dialCount), "expired cache must re-dial")
}

func TestHostPortFromEndpoint(t *testing.T) {
	cases := []struct {
		in       string
		want     string
		wantOK   bool
		wantHost string
	}{
		{in: "wss://ms.example.com/ws", want: "ms.example.com:443", wantOK: true},
		{in: "https://cloud.example.com", want: "cloud.example.com:443", wantOK: true},
		{in: "http://ms.example.com", want: "ms.example.com:80", wantOK: true},
		{in: "ws://ms.example.com", want: "ms.example.com:80", wantOK: true},
		{in: "https://ms.example.com:9443/path", want: "ms.example.com:9443", wantOK: true},
		{in: "ms.example.com:9443", want: "ms.example.com:9443", wantOK: true},
		{in: "", want: "", wantOK: false},
		{in: "://broken", want: "", wantOK: false},
		{in: "not a url at all", want: "", wantOK: false},
	}
	for _, tc := range cases {
		got, ok := hostPortFromEndpoint(tc.in)
		require.Equalf(t, tc.wantOK, ok, "ok mismatch for %q", tc.in)
		if tc.wantOK {
			require.Equalf(t, tc.want, got, "addr mismatch for %q", tc.in)
		}
	}
}

// TestNetworkProbeBadURL confirms a malformed endpoint surfaces as
// unreachable rather than erroring or panicking.
func TestNetworkProbeBadURL(t *testing.T) {
	p := newNetworkProbe()
	dialed := false
	p.dial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		dialed = true
		return nil, errors.New("should not dial")
	}
	got := p.Sample("://not-a-url", "")
	require.False(t, got.ManagementServerReachable)
	require.False(t, dialed, "malformed URL must not cause a dial")
}

// TestNetworkProbeRealDialFailureFast confirms the live net.Dial path
// reports unreachable for an endpoint that's been torn down. Uses a
// real (closed) listener so the production dial path is exercised.
func TestNetworkProbeRealDialFailureFast(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	endpoint := (&url.URL{Scheme: "http", Host: addr}).String()
	p := newNetworkProbe()
	got := p.Sample(endpoint, "")
	require.False(t, got.ManagementServerReachable, "closed listener must report unreachable")
}
