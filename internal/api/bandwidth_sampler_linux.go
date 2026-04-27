//go:build linux

package api

import (
	"os"
	"strconv"
	"strings"
)

// readNetCounters sums RX and TX bytes across every non-loopback
// interface from /proc/net/dev. Returns ok=false if the file is
// unreadable (containers without /proc, unusual mounts) — the
// sampler treats that as "no data" and the /v1/health.bandwidth
// fields stay at zero.
//
// /proc/net/dev format (after a 2-line header):
//
//	  Inter-|   Receive                                                |  Transmit
//	   face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
//	    eth0:  524288    1024    0    0    0     0          0         0   65536      512    0    0    0     0       0          0
//
// We only care about column 1 (rx bytes) and column 9 (tx bytes).
func readNetCounters() (bandwidthSample, bool) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return bandwidthSample{}, false
	}
	var total bandwidthSample
	for _, line := range strings.Split(string(data), "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:i])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 {
			continue
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			continue
		}
		total.rxBytes += rx
		total.txBytes += tx
	}
	return total, true
}
