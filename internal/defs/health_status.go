package defs

import (
	"time"
)

// HealthStatusOverall is the rolled-up health classification for a
// recorder.
type HealthStatusOverall string

// HealthStatus overall classifications per domain-model.md.
const (
	HealthStatusOverallHealthy   HealthStatusOverall = "healthy"
	HealthStatusOverallDegraded  HealthStatusOverall = "degraded"
	HealthStatusOverallUnhealthy HealthStatusOverall = "unhealthy"
)

// HealthStatusVolumeState is the per-volume state inside a HealthStatus
// snapshot. A subset of StorageVolume.status values applicable to
// health reporting.
type HealthStatusVolumeState string

// HealthStatus per-volume states per domain-model.md.
const (
	HealthStatusVolumeStateHealthy  HealthStatusVolumeState = "healthy"
	HealthStatusVolumeStateDegraded HealthStatusVolumeState = "degraded"
	HealthStatusVolumeStateFull     HealthStatusVolumeState = "full"
	HealthStatusVolumeStateReadOnly HealthStatusVolumeState = "read_only"
)

// HealthStatusStorage is one volume's entry inside a HealthStatus snapshot.
type HealthStatusStorage struct {
	VolumeID string                  `json:"volume_id"`
	UsedPct  float64                 `json:"used_pct"`
	Status   HealthStatusVolumeState `json:"status"`
}

// HealthStatusNetwork is the network-reachability block of a HealthStatus
// snapshot.
type HealthStatusNetwork struct {
	ManagementServerReachable bool       `json:"management_server_reachable"`
	CloudReachable            bool       `json:"cloud_reachable"`
	LastSyncAt                *time.Time `json:"last_sync_at,omitempty"`
}

// HealthStatusBandwidth is the process-level network bandwidth block
// of a HealthStatus snapshot. Both rates are bytes-per-second over
// the wall-clock interval since the previous /v1/health request,
// summed across every non-loopback interface the recorder process
// can see (so a multi-NIC host shows aggregate throughput, which
// matches what an operator expects from a "how busy is the
// recorder's network" reading).
type HealthStatusBandwidth struct {
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
}

// HealthStatus is the canonical HealthStatus entity exposed at
// /v1/health per ADR 0009 §D2.
//
// One snapshot, current state only; finer-grained metrics live in the
// Prometheus surface and are out of scope for this API.
type HealthStatus struct {
	ID                string    `json:"id"`
	RecordingServerID string    `json:"recording_server_id"`
	TenantID          string    `json:"tenant_id"`
	ReportedAt        time.Time `json:"reported_at"`

	CPUPct float64       `json:"cpu_pct"`
	MemPct float64       `json:"mem_pct"`
	Uptime time.Duration `json:"uptime"`

	CamerasTotal     int `json:"cameras_total"`
	CamerasRecording int `json:"cameras_recording"`
	CamerasOffline   int `json:"cameras_offline"`

	Storage   []HealthStatusStorage `json:"storage,omitempty"`
	Network   HealthStatusNetwork   `json:"network"`
	Bandwidth HealthStatusBandwidth `json:"bandwidth"`

	Overall HealthStatusOverall `json:"overall"`
}
