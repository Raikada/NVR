// Cross-platform host metrics for /v1/health.
//
// Single sampler module replacing the previous per-platform build-
// tagged files. gopsutil works on Linux, macOS, Windows, FreeBSD,
// and the BSDs — covers every platform the recorder targets and
// every platform a developer is realistically using.
//
// Three samplers exposed at package scope, all host-level:
//
//   - cpuPctSampler.Sample()        → host-wide CPU percent
//     (saturated reads ~100 regardless of core count; the value
//     is the host aggregate, not a per-process or per-core
//     reading)
//   - memPctSampler.Sample()        → host-wide used-memory
//     percent (in-use RAM across every process on the box,
//     divided by total RAM); falls back to a Go-runtime heap
//     proxy if the OS reading is unavailable
//   - bandwidthSampler.Sample()     → host network throughput
//     (rx_bps, tx_bps) summed across non-loopback interfaces
//
// Per ADR 0009 amendment 2026-04-27-adr-0009-amendment-3: the
// `/v1/health` endpoint answers the fleet-monitoring question
// ("is this box hot?"), not "is the recorder process itself the
// bottleneck?" — the latter belongs to the Prometheus surface.
//
// CPU and bandwidth samplers prime on first call (return zero or
// (0, 0)) and produce real readings on subsequent calls. Memory
// is a point-in-time host reading and does not need priming.

package api

import (
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	memstat "github.com/shirou/gopsutil/v3/mem"
	netstat "github.com/shirou/gopsutil/v3/net"
)

/* ---------- CPU ---------- */

type cpuSampler struct{}

// Sample returns the host-wide CPU percent. Saturated reads ~100
// regardless of core count — the value is the host aggregate,
// not a per-process or per-core reading. First call after process
// start returns 0 (gopsutil's cpu.Percent has no previous CPU
// time delta to compare against on its first invocation);
// subsequent calls return the % since the previous Sample() call.
func (s *cpuSampler) Sample() float64 {
	pcts, err := cpu.Percent(0, false)
	if err != nil || len(pcts) == 0 {
		return 0
	}
	return pcts[0]
}

var cpuPctSampler = &cpuSampler{}

/* ---------- Memory ---------- */

type memSampler struct{}

// Sample returns the host-wide used-memory percent (in-use RAM
// across every process on the box, divided by total RAM). Falls
// back to (HeapInuse / HeapSys) * 100 from Go runtime when
// gopsutil can't read the OS value — keeps the field shape
// coherent on constrained dev environments.
func (s *memSampler) Sample() float64 {
	vm, err := memstat.VirtualMemory()
	if err == nil && vm != nil {
		return vm.UsedPercent
	}

	// Fallback: Go runtime heap. Process-scoped and underreports
	// residency, but keeps the field shape coherent when the OS
	// reading is unavailable.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapSys > 0 {
		return float64(ms.HeapInuse) / float64(ms.HeapSys) * 100.0
	}
	return 0
}

var memPctSampler = &memSampler{}

/* ---------- Bandwidth ---------- */

type bandwidthSnapshot struct {
	rxBytes uint64
	txBytes uint64
	at      time.Time
}

type bandwidthSampler struct {
	mu        sync.Mutex
	prev      bandwidthSnapshot
	primed    bool
	lastRxBps float64
	lastTxBps float64
	// Rolling-window history for PEAK / AVG over the last 24h.
	// One sample per Sample() call; bounded to roughly 24h at
	// 1Hz polling (86400 samples; about 1.4 MB resident — fine
	// for an appliance recorder).
	history []bandwidthHistoryEntry
}

type bandwidthHistoryEntry struct {
	at    time.Time
	rxBps float64
	txBps float64
}

const bandwidthHistoryWindow = 24 * time.Hour

// Sample returns (rx_bps, tx_bps) across non-loopback interfaces.
// First call primes and returns (0, 0); subsequent calls return
// real bytes-per-second over the wall-clock interval since the
// previous call. Counter-bounce / wall-clock-backwards cases reset
// to zero.
func (s *bandwidthSampler) Sample() (rxBps, txBps float64) {
	now := time.Now()
	cur, ok := readNetCounters()
	if !ok {
		return 0, 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.primed {
		s.prev = bandwidthSnapshot{rxBytes: cur.rxBytes, txBytes: cur.txBytes, at: now}
		s.primed = true
		return 0, 0
	}

	wall := now.Sub(s.prev.at).Seconds()
	if wall <= 0 {
		return s.lastRxBps, s.lastTxBps
	}
	dRx := int64(cur.rxBytes) - int64(s.prev.rxBytes)
	dTx := int64(cur.txBytes) - int64(s.prev.txBytes)
	s.prev = bandwidthSnapshot{rxBytes: cur.rxBytes, txBytes: cur.txBytes, at: now}

	if dRx < 0 || dTx < 0 {
		s.lastRxBps, s.lastTxBps = 0, 0
		return 0, 0
	}
	s.lastRxBps = float64(dRx) / wall
	s.lastTxBps = float64(dTx) / wall

	// History push + prune. Compact in-place: drop entries older
	// than the window from the front before appending.
	cutoff := now.Add(-bandwidthHistoryWindow)
	i := 0
	for i < len(s.history) && s.history[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		s.history = s.history[i:]
	}
	s.history = append(s.history, bandwidthHistoryEntry{at: now, rxBps: s.lastRxBps, txBps: s.lastTxBps})

	return s.lastRxBps, s.lastTxBps
}

// HistoryStats returns peak (max-of-total-bps) and avg (mean-of-total-bps)
// over the rolling window. Returns (0, 0) when the history is empty
// or only contains the priming entry.
func (s *bandwidthSampler) HistoryStats() (peakBps, avgBps float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) == 0 {
		return 0, 0
	}
	var peak float64
	var sum float64
	for _, h := range s.history {
		t := h.rxBps + h.txBps
		if t > peak {
			peak = t
		}
		sum += t
	}
	return peak, sum / float64(len(s.history))
}

type netCountersSnapshot struct {
	rxBytes uint64
	txBytes uint64
}

// readNetCounters sums RX/TX bytes across every non-loopback
// interface gopsutil enumerates. gopsutil handles the per-OS
// branching internally (procfs on Linux, GetIfTable2 on Windows,
// sysctl on macOS/BSD), so we get the same answer everywhere.
func readNetCounters() (netCountersSnapshot, bool) {
	stats, err := netstat.IOCounters(true) // pernic=true: per-interface
	if err != nil {
		return netCountersSnapshot{}, false
	}
	var total netCountersSnapshot
	for _, s := range stats {
		// Drop loopback interfaces. Names vary by OS:
		//   Linux:   "lo"
		//   macOS:   "lo0"
		//   Windows: "Loopback Pseudo-Interface 1" / "lo"
		// "lo" prefix catches the unix forms; the Windows form is
		// already excluded by gopsutil's name filter on most
		// versions, but we belt-and-suspenders here.
		if len(s.Name) >= 2 && s.Name[:2] == "lo" {
			continue
		}
		if s.Name == "Loopback Pseudo-Interface 1" {
			continue
		}
		total.rxBytes += s.BytesRecv
		total.txBytes += s.BytesSent
	}
	return total, true
}

var bandwidthSamplerSingleton = &bandwidthSampler{}

/* ---------- mount-path resolution (used by SMART probe) ---------- */

// longestMountPrefix walks gopsutil's per-OS mount partition list
// and returns the device path for the mount-point that's the
// longest prefix of mountPath. Returns "" when no partition matches
// or when the matched device isn't a real block device (smartctl
// runs on real disks, not tmpfs / overlay / NFS).
//
// Cross-platform: Linux → /proc/mounts, macOS → getfsstat, Windows
// → GetLogicalDrives. gopsutil normalizes the device strings into
// /dev/sda-style on unix and \\.\PHYSICALDRIVE0-style on Windows;
// callers feed those directly to smartctl.
func longestMountPrefix(mountPath string) string {
	parts, err := disk.Partitions(false) // physical only
	if err != nil {
		return ""
	}
	bestLen := -1
	best := ""
	for _, p := range parts {
		if p.Mountpoint == "" || p.Device == "" {
			continue
		}
		if len(p.Mountpoint) > len(mountPath) {
			continue
		}
		if mountPath[:len(p.Mountpoint)] != p.Mountpoint {
			continue
		}
		if len(p.Mountpoint) > bestLen {
			bestLen = len(p.Mountpoint)
			best = p.Device
		}
	}
	return best
}
