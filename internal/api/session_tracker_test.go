package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJTISessionRegistry_FirstSeenThenDedup(t *testing.T) {
	r := &jtiSessionRegistry{}
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	// First sighting: emit.
	require.True(t, r.Touch("jti-A", now))

	// Same jti within TTL: dedup.
	require.False(t, r.Touch("jti-A", now.Add(1*time.Second)))
	require.False(t, r.Touch("jti-A", now.Add(5*time.Minute)))

	// Different jti: emit.
	require.True(t, r.Touch("jti-B", now.Add(2*time.Second)))
}

func TestJTISessionRegistry_EmptyJTINeverEmits(t *testing.T) {
	r := &jtiSessionRegistry{}
	now := time.Now()

	// Pre-ADR-0011 paths produce empty RawJTI; never emit.
	require.False(t, r.Touch("", now))
	require.False(t, r.Touch("", now.Add(1*time.Second)))
}

func TestJTISessionRegistry_TTLPrune(t *testing.T) {
	r := &jtiSessionRegistry{}
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	// First sighting.
	require.True(t, r.Touch("jti-stale", now))

	// Continuous use within TTL keeps refreshing first-seen.
	require.False(t, r.Touch("jti-stale", now.Add(20*time.Minute)))

	// After TTL with no activity, the entry prunes; the next sighting
	// is treated as a new session.
	farFuture := now.Add(20*time.Minute).Add(jtiSessionTTL).Add(1 * time.Minute)
	require.True(t, r.Touch("jti-stale", farFuture))
}
