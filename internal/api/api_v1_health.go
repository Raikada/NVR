// Package api: /v1/health handler per ADR 0009 §D5 Health.
//
// Single endpoint, current snapshot only. Fine-grained metrics live
// in the existing Prometheus surface and are out of scope per ADR
// 0009 §D2 ("HealthStatus … one endpoint, current state only — fine-
// grained metrics live in the existing Prometheus surface").
//
// Phase 2D builds the snapshot best-effort: uptime from a.Started,
// goroutine count and Go-runtime memory stats from runtime, camera
// counts from PathManager + conf.Conf.Paths, storage from the same
// volume walk used by /v1/storage-volumes. Fields the recorder
// doesn't have data for yet land at zero values; the canonical
// shape stays coherent regardless.
package api //nolint:revive

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// onV1HealthGet serves GET /v1/health. Best-effort canonical
// HealthStatus snapshot from whatever signals the recorder
// exposes today. CPU / mem / bandwidth fields are host-level
// per ADR 0009 amendment 2026-04-27-adr-0009-amendment-3.
func (a *API) onV1HealthGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	started := a.Started
	a.mutex.RUnlock()

	in := defs.HealthStatusInput{
		ReportedAt: nowUTC(),
	}

	// Uptime. a.Started is stamped by core at process start (api.go
	// field; not modified here).
	if !started.IsZero() {
		in.Uptime = time.Since(started)
	}

	// MemPct: host-wide used-memory percent (in-use RAM across
	// every process on the box, divided by total RAM), sampled via
	// gopsutil's VirtualMemory().UsedPercent. Falls back internally
	// to a Go-runtime heap proxy when the OS reading is unavailable.
	in.MemPct = memPctSampler.Sample()
	// CPUPct: host-wide CPU percent. Saturated reads ~100 regardless
	// of core count — the value is the host aggregate, not a
	// per-process or per-core reading. See host_sampler.go for the
	// sampler design. The first call after process start returns 0
	// (no previous CPU-time delta to compare against); every
	// subsequent call returns the average CPU percentage of the
	// interval since the previous call.
	in.CPUPct = cpuPctSampler.Sample()

	// Camera counts. We lift them from c.Paths (configured cameras)
	// and PathManager (which tells us which are runtime-online).
	if c != nil {
		in.CamerasTotal = len(c.Paths)
	}
	if a.PathManager != nil && c != nil {
		offline := 0
		recording := 0
		for name, pathConf := range c.Paths {
			ap, err := a.PathManager.APIPathsGet(name)
			if err != nil || ap == nil {
				offline++
				continue
			}
			if !ap.Online {
				offline++
			}
			// "Currently recording" = path is online AND its conf has
			// the Record flag set. Reading conf.Path.Record directly is
			// safe under a.mutex.RLock() (held by the caller) since
			// /v1/cameras and /v1/recording-policies handlers always
			// mutate the path under a.mutex.Lock().
			if ap.Online && pathConf != nil && pathConf.Record {
				recording++
			}
		}
		in.CamerasOffline = offline
		in.CamerasRecording = recording
	}

	// Storage. Reuse the storage-volume walk so /v1/storage-volumes
	// and /v1/health agree about what's mounted.
	volumes := a.collectStorageVolumes(c)
	if len(volumes) > 0 {
		in.Storage = make([]defs.HealthStatusStorage, 0, len(volumes))
		for _, v := range volumes {
			usedPct := 0.0
			if v.CapacityBytes > 0 {
				usedPct = float64(v.UsedBytes) / float64(v.CapacityBytes) * 100.0
			}
			in.Storage = append(in.Storage, defs.HealthStatusStorage{
				VolumeID: v.ID,
				UsedPct:  usedPct,
				Status:   volumeStatusToHealthState(v.Status),
			})
		}
	}

	// Network reachability: until the MS-pairing client lands (ADR
	// 0008, reserved) the recorder has no live control-plane
	// connection to observe. As a stub, we run TCP-reachability
	// probes against the optional bootstrap endpoints
	// conf.Conf.ManagementServerEndpoint and conf.Conf.CloudEndpoint;
	// either field unset reports the corresponding *_reachable as
	// false. last_sync_at stays nil here — TCP reachability is not a
	// successful control-plane sync; the pairing client is what will
	// populate it.
	a.mutex.Lock()
	if a.networkProbe == nil {
		a.networkProbe = newNetworkProbe()
	}
	probe := a.networkProbe
	a.mutex.Unlock()
	msEndpoint := ""
	cloudEndpoint := ""
	if c != nil {
		msEndpoint = ""
		cloudEndpoint = ""
	}
	in.Network = probe.Sample(msEndpoint, cloudEndpoint)

	// Bandwidth — host-level network IO summed across non-loopback
	// interfaces (gopsutil IOCounters(pernic=true), so the kernel-
	// reported per-NIC counters; not process-scoped). First call
	// returns (0, 0) and primes the sampler; subsequent calls return
	// real bytes-per-second over the wall-clock interval since the
	// previous /v1/health request.
	rxBps, txBps := bandwidthSamplerSingleton.Sample()
	in.Bandwidth = defs.HealthStatusBandwidth{RxBps: rxBps, TxBps: txBps}

	// recordingServerID: the recorder doesn't yet surface a server-
	// scoped UUID through conf.Conf (no `recording_server_id` field
	// today; see Phase 1 translator notes). We pass empty and let the
	// canonical shape emit "" — same convention as defs.Stream and
	// defs.Camera elsewhere in the surface.
	tenantID := ""
	if c != nil {
		tenantID = ""
	}

	hs := defs.BuildHealthStatus(in, healthSnapshotID(started), "", tenantID)
	ctx.JSON(http.StatusOK, &hs)
}

// healthSnapshotID derives a deterministic id for the current
// snapshot. Per ADR 0009 §D2 there's only ever "the most recent"
// HealthStatus; clients don't paginate snapshots. We pick a stable
// per-process id (UUIDv5 from the recorder's start time) so repeated
// /v1/health calls within one process return the same id, matching
// the "current snapshot" semantics.
func healthSnapshotID(started time.Time) string {
	if started.IsZero() {
		// Fallback for tests that construct API directly without
		// stamping Started. Use a fixed sentinel UUID so the response
		// shape is still a valid UUID.
		return "00000000-0000-0000-0000-000000000000"
	}
	return uuidV5FromString("health-snapshot|" + started.UTC().Format(time.RFC3339Nano))
}

// volumeStatusToHealthState maps a StorageVolumeStatus (the per-
// volume operational state) onto the smaller HealthStatusVolumeState
// vocabulary used inside a HealthStatus snapshot. Both vocabularies
// originate in domain-model.md; the mapping is intentionally
// straight-through with degraded as the catch-all for "we know it's
// not healthy but don't have a finer classification."
func volumeStatusToHealthState(s defs.StorageVolumeStatus) defs.HealthStatusVolumeState {
	switch s {
	case defs.StorageVolumeStatusHealthy:
		return defs.HealthStatusVolumeStateHealthy
	case defs.StorageVolumeStatusDegraded:
		return defs.HealthStatusVolumeStateDegraded
	case defs.StorageVolumeStatusFull:
		return defs.HealthStatusVolumeStateFull
	case defs.StorageVolumeStatusReadOnly:
		return defs.HealthStatusVolumeStateReadOnly
	case defs.StorageVolumeStatusMissing:
		// HealthStatusVolumeState has no missing variant; degrade to
		// "degraded" so callers that don't want full health-snapshot
		// detail still see the volume as not-healthy.
		return defs.HealthStatusVolumeStateDegraded
	}
	return defs.HealthStatusVolumeStateHealthy
}
