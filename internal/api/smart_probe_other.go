//go:build !linux

package api

// longestMountPrefix is a no-op on non-Linux platforms. The SMART
// probe runs only when smartctl is available AND we can resolve
// the underlying block device from /proc/mounts; on macOS/Windows
// dev environments neither holds, so the SMART block is omitted
// from /v1/storage-volumes responses.
func longestMountPrefix(_ string) string {
	return ""
}
