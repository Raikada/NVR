package mediasign

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	require.NoError(t, err)
	now := time.Date(2026, 7, 2, 17, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	exp, sig := s.Sign("/v1/media/snapshots/ev-1/thumb", 15*time.Minute)
	require.True(t, s.Verify("/v1/media/snapshots/ev-1/thumb", exp, sig))
}

func TestVerifyRejectsTamper(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	now := time.Date(2026, 7, 2, 17, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	exp, sig := s.Sign("/v1/media/snapshots/ev-1/thumb", 15*time.Minute)
	require.False(t, s.Verify("/v1/media/snapshots/ev-2/thumb", exp, sig), "path swap")
	require.False(t, s.Verify("/v1/media/snapshots/ev-1/thumb", exp+60, sig), "exp swap")
	require.False(t, s.Verify("/v1/media/snapshots/ev-1/thumb", exp, "deadbeef"), "sig swap")
}

func TestVerifyRejectsExpired(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	now := time.Date(2026, 7, 2, 17, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	exp, sig := s.Sign("/p", time.Minute)
	s.now = func() time.Time { return now.Add(2 * time.Minute) }
	require.False(t, s.Verify("/p", exp, sig))
}

func TestKeyPersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	require.NoError(t, err)
	now := time.Date(2026, 7, 2, 17, 0, 0, 0, time.UTC)
	s1.now = func() time.Time { return now }
	exp, sig := s1.Sign("/p", time.Hour)

	s2, err := Open(dir)
	require.NoError(t, err)
	s2.now = func() time.Time { return now }
	require.True(t, s2.Verify("/p", exp, sig), "second Open must load the same key")
}
