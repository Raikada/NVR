//go:build !linux

package api

// readSystemMemPct returns ok=false on non-Linux platforms — the
// /v1/health handler will fall back to the Go-runtime heap-in-use
// proxy. The recorder's production target is Linux; macOS / Windows
// support is for development and CI only, where the proxy is good
// enough to keep the field shape coherent.
func readSystemMemPct() (float64, bool) {
	return 0, false
}
