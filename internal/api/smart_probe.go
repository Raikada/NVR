// SMART probe for /v1/storage-volumes.
//
// Best-effort: shells out to `smartctl -j -A -i -H <device>` if the
// binary is on PATH and the mount path resolves to a block device.
// Recorder runs as the appliance's only privileged user under the
// production Docker image, so smartctl access works as long as the
// host installs smartmontools and the container has --privileged
// (or at least CAP_SYS_RAWIO + access to the device files). When
// any of those conditions fail we silently return nil — the
// /v1/storage-volumes wire shape omits the SMART block and the UI
// renders a "—" for the affected fields.
//
// Results are cached per device for 60s — SMART attributes don't
// change minute-to-minute and the probe is the most expensive part
// of /v1/storage-volumes by an order of magnitude.

package api

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
)

type smartCacheEntry struct {
	at   time.Time
	data *defs.StorageVolumeSMART
}

type smartProber struct {
	mu    sync.Mutex
	cache map[string]smartCacheEntry
	// Whether smartctl was found on PATH. Resolved on first call;
	// cached so subsequent calls don't pay the LookPath cost.
	smartctlPath string
	checked      bool
	// Whether we found smartctl. False when not installed or not
	// in PATH; once false, every Probe returns nil immediately.
	available bool
}

const smartCacheTTL = 60 * time.Second

// Probe returns a SMART block for the device backing mountPath, or
// nil if smartctl isn't available, the path doesn't resolve to a
// device, or the probe failed for any reason. Always returns
// quickly — slow paths log + return nil rather than blocking the
// /v1/storage-volumes call.
func (p *smartProber) Probe(mountPath string) *defs.StorageVolumeSMART {
	p.mu.Lock()
	if !p.checked {
		p.checked = true
		path, err := exec.LookPath("smartctl")
		if err == nil {
			p.smartctlPath = path
			p.available = true
		}
	}
	if !p.available {
		p.mu.Unlock()
		return nil
	}
	if p.cache == nil {
		p.cache = make(map[string]smartCacheEntry)
	}
	if entry, ok := p.cache[mountPath]; ok && time.Since(entry.at) < smartCacheTTL {
		p.mu.Unlock()
		return entry.data
	}
	smartctlPath := p.smartctlPath
	p.mu.Unlock()

	device := deviceForMount(mountPath)
	if device == "" {
		return p.cacheAndReturn(mountPath, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, smartctlPath, "-j", "-A", "-i", "-H", device)
	out, _ := cmd.Output() // ignore exit code: smartctl exits non-zero on smart-failure detection too, but JSON is still valid
	if len(out) == 0 {
		return p.cacheAndReturn(mountPath, nil)
	}

	parsed, ok := parseSmartctlJSON(out, device)
	if !ok {
		return p.cacheAndReturn(mountPath, nil)
	}
	return p.cacheAndReturn(mountPath, parsed)
}

func (p *smartProber) cacheAndReturn(mountPath string, data *defs.StorageVolumeSMART) *defs.StorageVolumeSMART {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[mountPath] = smartCacheEntry{at: time.Now(), data: data}
	return data
}

// deviceForMount resolves a mount path to its underlying block
// device by walking /proc/mounts. Returns "" if the path isn't
// mounted on a block device (tmpfs, NFS, missing) — those skip
// the SMART probe.
func deviceForMount(mountPath string) string {
	// Use os.Stat + Sys() to grab the device id; then look it up in
	// /proc/self/mountinfo. The simpler path: look for a /proc/mounts
	// entry whose mount-point is a prefix of mountPath, pick the
	// longest match. That's what `df` does and it handles bind mounts
	// reasonably.
	return longestMountPrefix(mountPath)
}

// parseSmartctlJSON pulls the fields the UI surfaces out of the
// smartctl --json output. Defensive: every field is optional and
// missing fields stay nil.
func parseSmartctlJSON(raw []byte, device string) (*defs.StorageVolumeSMART, bool) {
	var sj smartctlJSON
	if err := json.Unmarshal(raw, &sj); err != nil {
		return nil, false
	}
	out := &defs.StorageVolumeSMART{
		Device:       device,
		ModelFamily:  sj.ModelFamily,
		ModelName:    sj.ModelName,
		SerialNumber: sj.SerialNumber,
	}
	if sj.SmartStatus != nil {
		passed := sj.SmartStatus.Passed
		out.HealthPassed = &passed
	}
	if sj.PowerOnTime != nil {
		hours := sj.PowerOnTime.Hours
		out.PowerOnHours = &hours
	}
	if sj.Temperature != nil {
		t := sj.Temperature.Current
		out.TemperatureC = &t
	}
	return out, true
}

// smartctlJSON mirrors the subset of `smartctl -j` we consume.
// smartctl's output format is stable across the smartmontools
// versions in Alpine packages and Debian backports.
type smartctlJSON struct {
	ModelFamily  string `json:"model_family"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	SmartStatus  *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
}

var smartProberSingleton = &smartProber{}

// longestMountPrefix is the cross-platform mount-path resolver
// (in host_sampler.go). gopsutil disk.Partitions handles the
// per-OS branching, so smartctl receives the right device-path
// shape on Linux (/dev/sda), macOS (/dev/disk0), and Windows
// (\\.\PHYSICALDRIVE0).
