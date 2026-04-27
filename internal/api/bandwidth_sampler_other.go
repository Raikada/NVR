//go:build !linux

package api

// readNetCounters returns ok=false on non-Linux platforms.
// Recorder's production target is Linux/Alpine; macOS / Windows
// support is for development and CI where bandwidth-rate accuracy
// isn't a goal — the field stays at zero so the response shape
// remains coherent.
func readNetCounters() (bandwidthSample, bool) {
	return bandwidthSample{}, false
}
