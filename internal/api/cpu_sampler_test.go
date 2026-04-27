package api //nolint:revive

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCPUSamplerFirstCallZero verifies the documented "first call
// returns 0" semantic — the sampler has no previous sample to diff
// against, so it primes its state and returns zero.
func TestCPUSamplerFirstCallZero(t *testing.T) {
	s := &cpuSampler{}
	got := s.Sample()
	require.Equal(t, 0.0, got, "first call must return 0")
}

// TestCPUSamplerSecondCallNonNegative verifies the second call
// returns a sane percentage. We do a small busy-loop between samples
// so the interval has nonzero CPU work; the result must be in
// [0, 100*NumCPU].
func TestCPUSamplerSecondCallSane(t *testing.T) {
	s := &cpuSampler{}
	_ = s.Sample()

	// Burn a small amount of CPU time so the rusage delta is real.
	deadline := time.Now().Add(50 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	_ = x

	got := s.Sample()
	maxPct := 100.0 * float64(runtime.NumCPU())
	require.GreaterOrEqual(t, got, 0.0, "cpu_pct must be non-negative")
	require.LessOrEqual(t, got, maxPct, "cpu_pct must not exceed 100*NumCPU")
}

// TestCPUSamplerImmediateReSampleZero verifies that two samples
// taken in immediate succession (effectively zero wall-time delta)
// don't produce a divide-by-zero or absurd reading. The handler
// can be hit by an aggressive client and must not panic.
func TestCPUSamplerImmediateReSample(t *testing.T) {
	s := &cpuSampler{}
	_ = s.Sample()
	got := s.Sample()
	maxPct := 100.0 * float64(runtime.NumCPU())
	require.GreaterOrEqual(t, got, 0.0)
	require.LessOrEqual(t, got, maxPct)
}
