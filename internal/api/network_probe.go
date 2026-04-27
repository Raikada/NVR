// Network reachability probe for /v1/health.
//
// HealthStatus.network (ADR 0009 §D5 Health) reports whether the
// recorder can reach its upstream Management Server and Cloud
// endpoints. Until the MS-pairing client lands (ADR 0008, reserved)
// the recorder has no live control-plane connection to observe; this
// probe stands in by performing simple TCP-reachability checks
// against optionally-configured endpoint URLs from the bootstrap
// config (conf.Conf.ManagementServerEndpoint, conf.Conf.CloudEndpoint).
//
// Design notes:
//   - Probe is a TCP dial with a 2s timeout. We don't speak the
//     control-plane protocol here; we only confirm the network path
//     to host:port is open. False negatives are acceptable (a TLS-
//     terminating LB that refuses bare TCP would read unreachable);
//     the eventual pairing client supersedes this stub.
//   - Results are cached for 30s. /v1/health is the canonical
//     "current snapshot" endpoint and may be polled; we don't want
//     each call to issue two outbound dials. The cache is lazy:
//     refreshed on the first request after expiry.
//   - last_sync_at stays nil. This stub has no notion of a successful
//     control-plane sync (TCP reachability is not a sync). The field
//     flips to live values once the pairing client lands.
//   - Per ADR 0003 the recorder is a client of cloud, never a server;
//     these probes are outbound-only.
package api

import (
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
)

const (
	networkProbeDialTimeout = 2 * time.Second
	networkProbeCacheTTL    = 30 * time.Second
)

// networkProbe samples TCP-reachability of optionally-configured
// upstream endpoints for HealthStatus.network. Safe for concurrent
// callers; the cache is mutex-guarded.
type networkProbe struct {
	// dial is the dialer used for probes. Tests override it.
	dial func(network, address string, timeout time.Duration) (net.Conn, error)
	// now returns the current time. Tests override it.
	now func() time.Time

	mu          sync.Mutex
	lastSampled time.Time
	cached      defs.HealthStatusNetwork
	primed      bool
}

// newNetworkProbe builds a probe with production defaults.
func newNetworkProbe() *networkProbe {
	return &networkProbe{
		dial: net.DialTimeout,
		now:  time.Now,
	}
}

// Sample returns the current HealthStatusNetwork given the recorder's
// configured upstream endpoints. Endpoints that are unset produce a
// false reachable flag without dialing. Results are cached for
// networkProbeCacheTTL; callers within that window get the cached
// value without issuing fresh dials.
//
// last_sync_at stays nil here: TCP reachability is not a successful
// control-plane sync. The pairing client (ADR 0008) is what will set
// that field once it lands.
func (p *networkProbe) Sample(msEndpoint, cloudEndpoint string) defs.HealthStatusNetwork {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if p.primed && now.Sub(p.lastSampled) < networkProbeCacheTTL {
		return p.cached
	}

	out := defs.HealthStatusNetwork{
		ManagementServerReachable: p.reachable(msEndpoint),
		CloudReachable:            p.reachable(cloudEndpoint),
		// LastSyncAt: intentionally nil — see file-level comment.
	}
	p.cached = out
	p.lastSampled = now
	p.primed = true
	return out
}

// reachable returns true if a TCP connection to the host:port
// derived from endpoint succeeds within networkProbeDialTimeout.
// An empty endpoint is reported false (the "unconfigured" case).
// A malformed URL is reported false (defensive; Validate() should
// catch shape issues earlier).
func (p *networkProbe) reachable(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	addr, ok := hostPortFromEndpoint(endpoint)
	if !ok {
		return false
	}
	conn, err := p.dial("tcp", addr, networkProbeDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// hostPortFromEndpoint extracts host:port from a typical endpoint
// URL. Schemes wss/https default to port 443; ws/http default to 80.
// Endpoints that already carry an explicit port are passed through.
// Bare host:port (no scheme) is also accepted.
func hostPortFromEndpoint(endpoint string) (string, bool) {
	// Try URL parse first.
	u, err := url.Parse(endpoint)
	if err == nil && u.Host != "" {
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = defaultPortForScheme(u.Scheme)
		}
		if host == "" || port == "" {
			return "", false
		}
		return net.JoinHostPort(host, port), true
	}
	// Fallback: bare host:port.
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" || port == "" {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

func defaultPortForScheme(scheme string) string {
	switch scheme {
	case "wss", "https":
		return "443"
	case "ws", "http":
		return "80"
	}
	return ""
}
