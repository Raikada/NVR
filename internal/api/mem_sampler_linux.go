//go:build linux

package api

import (
	"os"
	"strconv"
	"strings"
)

// readSystemMemPct reads the recorder's RSS (VmRSS from
// /proc/self/status) and the system total memory (MemTotal from
// /proc/meminfo) and returns RSS as a percentage of MemTotal.
//
// Both files are well-defined kernel interfaces present on every
// Linux system the recorder targets (Alpine 3.23 ships the standard
// procfs). Read failures (e.g., a non-procfs filesystem in an
// unusual container setup) return ok=false and the caller falls
// back to the heap proxy.
//
// VmRSS is the resident-set size — physical pages this process
// actually has mapped — which captures cgo allocations from libav,
// goroutine stacks, mmap'd segment files, and the Go runtime's
// committed pages alike. This is what `top` and `ps -o rss` report.
func readSystemMemPct() (float64, bool) {
	rssKB, ok := readVmRSSKB("/proc/self/status")
	if !ok {
		return 0, false
	}
	totalKB, ok := readMemTotalKB("/proc/meminfo")
	if !ok || totalKB == 0 {
		return 0, false
	}
	return float64(rssKB) / float64(totalKB) * 100.0, true
}

// readVmRSSKB extracts the VmRSS value (in kB) from a /proc/<pid>/status
// file. Returns ok=false on any read or parse failure.
func readVmRSSKB(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return findKBField(string(data), "VmRSS:")
}

// readMemTotalKB extracts MemTotal (in kB) from a /proc/meminfo file.
// Returns ok=false on any read or parse failure.
func readMemTotalKB(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return findKBField(string(data), "MemTotal:")
}

// findKBField walks line-by-line looking for one prefixed by `key`,
// extracts the numeric value, and verifies the trailing unit is "kB".
// Both /proc/self/status's VmRSS and /proc/meminfo's MemTotal use the
// kB unit; defensive about anything else so a kernel quirk doesn't
// silently produce a 1024x-off reading.
func findKBField(content, key string) (int64, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, key) {
			continue
		}
		// Format: "VmRSS:    12345 kB"
		fields := strings.Fields(line[len(key):])
		if len(fields) < 2 || fields[1] != "kB" {
			return 0, false
		}
		v, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}
