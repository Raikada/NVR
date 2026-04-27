// Process-level CPU sampler for /v1/health.
//
// HealthStatus.cpu_pct (ADR 0009 §D5 Health) wants a coarse "how
// busy is this recorder" reading without bringing a third-party
// metrics library. We sample the process's user+system CPU time and
// the wall clock at each /v1/health call, then divide the deltas
// since the previous sample to get an interval-average percentage.
//
// Design notes:
//   - Process-level only. Out of scope to attribute time to specific
//     subsystems (the Prometheus surface is the place for that).
//   - The first call after process start has no previous sample; it
//     returns 0. Subsequent calls return CPU percent of the interval
//     between calls. This matches the "instantaneous best-effort"
//     framing the rest of /v1/health uses (uptime, mem, camera
//     counts).
//   - Percentage is normalized to one-CPU-equivalent at 100. A
//     fully-saturated 8-core box reads ~800 here; clients that want
//     a per-core figure divide by runtime.NumCPU(). The canonical
//     HealthStatus.cpu_pct is documented as "% CPU busy"; we
//     emit the unscaled (multi-core-aggregate) value because that's
//     what `top` and similar tools report and what bare-int CPU-time
//     deltas naturally produce.
//   - syscall.Getrusage(RUSAGE_SELF) is the Unix-portable stdlib
//     reader. It reports user+system time for the calling process,
//     same data /proc/self/stat fields 14/15 would yield on Linux,
//     but without /proc parsing and available on macOS too. Windows
//     gets a zero-stub (cpu_sampler_windows.go) — no new deps for
//     either branch.
package api

import (
	"sync"
	"time"
)

// cpuSampler computes process CPU usage between samples.
//
// Sample() is goroutine-safe; cpuPctSampler is the package-level
// instance reused by every /v1/health handler call.
type cpuSampler struct {
	mu       sync.Mutex
	prevCPU  time.Duration
	prevWall time.Time
	primed   bool
}

// Sample returns the CPU percentage between the previous Sample call
// and now. The first call (no previous sample) returns 0. On systems
// where the underlying syscall fails or is unsupported (e.g.
// Windows), Sample returns 0 — matching the "honest zero" the
// /v1/health handler used before this sampler existed.
func (s *cpuSampler) Sample() float64 {
	now := time.Now()
	cpu, ok := readProcessCPU()
	if !ok {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.primed {
		s.prevCPU = cpu
		s.prevWall = now
		s.primed = true
		return 0
	}

	wallDelta := now.Sub(s.prevWall)
	if wallDelta <= 0 {
		// Clock didn't advance (or went backwards). Don't update
		// state; the next call with a real interval will produce a
		// valid sample.
		return 0
	}
	cpuDelta := cpu - s.prevCPU
	if cpuDelta < 0 {
		// Defensive: rusage shouldn't go backwards, but a zero-stub
		// implementation that flips to non-zero would. Reset state
		// rather than return a negative percentage.
		s.prevCPU = cpu
		s.prevWall = now
		return 0
	}

	s.prevCPU = cpu
	s.prevWall = now
	return float64(cpuDelta) / float64(wallDelta) * 100.0
}

// cpuPctSampler is the singleton sampler used by /v1/health. It is
// primed on the first request; subsequent requests return the CPU
// percentage between calls.
var cpuPctSampler = &cpuSampler{}
