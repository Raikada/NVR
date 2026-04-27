//go:build linux

package api

import (
	"os"
	"strings"
)

// longestMountPrefix walks /proc/mounts and returns the device for
// the mount-point that is the longest prefix of mountPath. Returns
// "" when no mount-point matches or when the matched device isn't
// a /dev/* block device (skips tmpfs, overlay, NFS shares — SMART
// doesn't apply to those).
func longestMountPrefix(mountPath string) string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	bestLen := -1
	best := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		device, mp := fields[0], fields[1]
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}
		if !strings.HasPrefix(mountPath, mp) {
			continue
		}
		if len(mp) > bestLen {
			bestLen = len(mp)
			best = device
		}
	}
	return best
}
