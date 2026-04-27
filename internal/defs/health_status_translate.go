package defs

import (
	"time"
)

// HealthStatusInput is the recorder's internal snapshot of the data the
// canonical HealthStatus shape needs. The recorder doesn't have a single
// internal HealthStatus aggregator today — process metrics live near the
// Prometheus surface, storage state lives near the recordstore, network
// reachability lives near the MS-pairing client. This input contract
// names what BuildHealthStatus needs and lets Phase 2D wire the sources
// in one place.
type HealthStatusInput struct {
	CPUPct float64
	MemPct float64
	Uptime time.Duration

	CamerasTotal     int
	CamerasRecording int
	CamerasOffline   int

	Storage   []HealthStatusStorage
	Network   HealthStatusNetwork
	Bandwidth HealthStatusBandwidth

	// Overall is optional; if empty, BuildHealthStatus derives a
	// classification from CamerasOffline / Storage states using a
	// simple, conservative heuristic.
	Overall HealthStatusOverall

	// ReportedAt is optional; defaults to time.Now() when zero.
	ReportedAt time.Time
}

// BuildHealthStatus synthesizes a canonical HealthStatus snapshot from
// internal recorder telemetry plus per-process identity. Used at the
// /v1/health handler boundary.
//
// The overall-classification heuristic is intentionally conservative: any
// degraded volume → degraded; any cameras-offline → degraded; any full
// or missing volume → unhealthy. Callers that have a richer internal
// classifier supply Overall directly to skip the heuristic.
func BuildHealthStatus(
	in HealthStatusInput,
	healthStatusID string,
	recordingServerID string,
	tenantID string,
) HealthStatus {
	reported := in.ReportedAt
	if reported.IsZero() {
		reported = time.Now().UTC()
	}
	overall := in.Overall
	if overall == "" {
		overall = deriveOverallHealth(in)
	}
	return HealthStatus{
		ID:                healthStatusID,
		RecordingServerID: recordingServerID,
		TenantID:          tenantID,
		ReportedAt:        reported,
		CPUPct:            in.CPUPct,
		MemPct:            in.MemPct,
		Uptime:            in.Uptime,
		CamerasTotal:      in.CamerasTotal,
		CamerasRecording:  in.CamerasRecording,
		CamerasOffline:    in.CamerasOffline,
		Storage:           in.Storage,
		Network:           in.Network,
		Bandwidth:         in.Bandwidth,
		Overall:           overall,
	}
}

// deriveOverallHealth picks a HealthStatusOverall value from the
// HealthStatusInput when the caller didn't supply one.
func deriveOverallHealth(in HealthStatusInput) HealthStatusOverall {
	for _, s := range in.Storage {
		if s.Status == HealthStatusVolumeStateFull || s.Status == HealthStatusVolumeStateReadOnly {
			return HealthStatusOverallUnhealthy
		}
	}
	for _, s := range in.Storage {
		if s.Status == HealthStatusVolumeStateDegraded {
			return HealthStatusOverallDegraded
		}
	}
	if in.CamerasOffline > 0 {
		return HealthStatusOverallDegraded
	}
	return HealthStatusOverallHealthy
}
