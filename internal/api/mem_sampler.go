// Process-level memory sampler for /v1/health.
//
// HealthStatus.mem_pct (ADR 0009 §D5 Health) wants a coarse "how much
// memory is the recorder using" reading. Prior to this sampler the
// /v1/health handler emitted Go-runtime HeapInUse / HeapSys, a proxy
// that ignores the runtime's reserved-but-unused arenas plus all
// non-heap mappings (stacks, mmap'd files, cgo allocations from
// go-astiav). On a recorder that links libav, the heap proxy
// underreports actual residency by a wide margin.
//
// On Linux we read VmRSS from /proc/self/status against MemTotal from
// /proc/meminfo, giving system-wide percent of physical memory used by
// the recorder process. On non-Linux platforms (development on macOS
// notably) the heap proxy is retained — the recorder's production
// target is Linux/Alpine, and pulling gopsutil for the macOS dev path
// is more dep weight than the signal warrants.
//
// Mirrors cpu_sampler.go's split-by-build-tag pattern.
package api

// readSystemMemPct returns the recorder's process RSS as a percentage
// of system total memory, sourced from the OS's process-accounting
// surface. ok is true only when both readings succeeded; the caller
// falls back to the Go-runtime heap proxy when ok is false.
//
// The actual readings are platform-specific; see mem_sampler_linux.go
// and mem_sampler_other.go.
