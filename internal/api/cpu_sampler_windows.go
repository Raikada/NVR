//go:build windows

package api

import "time"

// readProcessCPU is unimplemented on Windows. The /v1/health handler
// will emit cpu_pct=0 — the same behavior the field had before the
// sampler existed. A real Windows implementation would call
// GetProcessTimes via golang.org/x/sys/windows; this is intentionally
// deferred until there's a Windows production target. The recorder
// runs on Linux in production; the Windows stub is just to keep the
// package buildable.
func readProcessCPU() (time.Duration, bool) {
	return 0, false
}
