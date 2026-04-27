//go:build !windows

package api

import (
	"syscall"
	"time"
)

// readProcessCPU returns the calling process's accumulated user +
// system CPU time. The value is monotonic non-decreasing across the
// life of the process. ok is false only if the syscall fails (which
// in practice means the kernel doesn't support RUSAGE_SELF — every
// supported unix target does).
func readProcessCPU() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	utime := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	stime := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	return utime + stime, true
}
