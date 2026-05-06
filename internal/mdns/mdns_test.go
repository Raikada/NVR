package mdns

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLiveMSBroadcastFingerprintMatch verifies the recorder's mDNS
// listener returns the cached MS broadcast that matches one of the
// pinned-root fingerprints, and ignores broadcasts whose advertised
// root_fp doesn't match. This is the security-critical filter
// preventing a hostile MS broadcast on the LAN from being treated as
// the bound MS just because it's there.
func TestLiveMSBroadcastFingerprintMatch(t *testing.T) {
	s := &Service{
		cache: map[string]*DiscoveredManagement{
			"ms-1": {
				MSID:            "ms-1",
				Hostname:        "ms-real.local",
				URL:             "https://ms-real.local:8443",
				RootFingerprint: "abc",
				LastSeenAt:      time.Now(),
			},
			"ms-2": {
				MSID:            "ms-2",
				Hostname:        "hostile.local",
				URL:             "https://hostile.local:8443",
				RootFingerprint: "deadbeef",
				LastSeenAt:      time.Now(),
			},
		},
	}

	got := s.LiveMSBroadcast([]string{"abc"})
	require.NotNil(t, got)
	require.Equal(t, "ms-real.local", got.Hostname)

	got = s.LiveMSBroadcast([]string{"unknown"})
	require.Nil(t, got, "no match should return nil")

	// Case-insensitive match.
	got = s.LiveMSBroadcast([]string{"ABC"})
	require.NotNil(t, got)
	require.Equal(t, "ms-real.local", got.Hostname)
}

// TestLiveMSBroadcastEmptyInputs guards against logic errors when
// callers pass empty fingerprint slices or have an empty cache.
func TestLiveMSBroadcastEmptyInputs(t *testing.T) {
	s := &Service{cache: map[string]*DiscoveredManagement{}}
	require.Nil(t, s.LiveMSBroadcast([]string{"abc"}))
	require.Nil(t, s.LiveMSBroadcast(nil))
	require.Nil(t, s.LiveMSBroadcast([]string{""}))
}

// TestLiveMSBroadcastPicksMostRecent ensures that when multiple
// broadcasts share a root (e.g. during root rotation), the most
// recently-seen one wins.
func TestLiveMSBroadcastPicksMostRecent(t *testing.T) {
	older := time.Now().Add(-time.Minute)
	newer := time.Now()
	s := &Service{
		cache: map[string]*DiscoveredManagement{
			"old": {
				MSID:            "ms-old",
				URL:             "https://ms-old.local:8443",
				RootFingerprint: "abc",
				LastSeenAt:      older,
			},
			"new": {
				MSID:            "ms-new",
				URL:             "https://ms-new.local:8443",
				RootFingerprint: "abc",
				LastSeenAt:      newer,
			},
		},
	}
	got := s.LiveMSBroadcast([]string{"abc"})
	require.NotNil(t, got)
	require.Equal(t, "ms-new", got.MSID)
}
