package defs

import (
	"time"
)

// StorageVolumeKind classifies a storage volume's backing.
type StorageVolumeKind string

// Storage-volume kinds per domain-model.md.
const (
	StorageVolumeKindInternalDisk StorageVolumeKind = "internal_disk"
	StorageVolumeKindExternalDisk StorageVolumeKind = "external_disk"
	StorageVolumeKindNetworkShare StorageVolumeKind = "network_share"
	StorageVolumeKindRamdisk      StorageVolumeKind = "ramdisk"
)

// StorageVolumeStatus is the operational status of a StorageVolume.
type StorageVolumeStatus string

// Storage-volume statuses per domain-model.md.
const (
	StorageVolumeStatusHealthy  StorageVolumeStatus = "healthy"
	StorageVolumeStatusDegraded StorageVolumeStatus = "degraded"
	StorageVolumeStatusFull     StorageVolumeStatus = "full"
	StorageVolumeStatusMissing  StorageVolumeStatus = "missing"
	StorageVolumeStatusReadOnly StorageVolumeStatus = "read_only"
)

// StorageVolume is the canonical StorageVolume entity exposed at
// /v1/storage-volumes per ADR 0009 §D2.
//
// Read-only at this surface; volume-bootstrap configuration (mount path,
// kind, priority) lives in /v1/recorder/config per the escape-hatch
// boundary.
type StorageVolume struct {
	ID                string `json:"id"`
	RecordingServerID string `json:"recording_server_id"`

	MountPath     string            `json:"mount_path"`
	Kind          StorageVolumeKind `json:"kind"`
	CapacityBytes int64             `json:"capacity_bytes"`
	UsedBytes     int64             `json:"used_bytes"`
	ReservedBytes int64             `json:"reserved_bytes"`

	Status StorageVolumeStatus `json:"status"`

	LastCheckedAt time.Time `json:"last_checked_at"`

	Priority int `json:"priority"`

	// WriteBytesPerSecond is the recorder's recent write throughput
	// to this volume, computed from used_bytes deltas across
	// successive /v1/storage-volumes calls. Optional — populated
	// only after the second sample arrives. Negative values
	// (retention pruned faster than recorder wrote) are clamped to
	// zero so the field surfaces only positive write activity.
	WriteBytesPerSecond *int64 `json:"write_bytes_per_second,omitempty"`

	// SMART is best-effort drive metadata sourced from a `smartctl`
	// shell-out. Populated when smartctl is on PATH and the
	// underlying mount resolves to a block device; absent
	// otherwise. Operators on hosts without smartmontools see no
	// SMART block (graceful degradation).
	SMART *StorageVolumeSMART `json:"smart,omitempty"`
}

// StorageVolumeSMART carries the subset of S.M.A.R.T. attributes
// the configuration UI surfaces today. Fields are omitted from the
// wire shape when smartctl couldn't determine them, so consumers
// must treat each field as optional.
type StorageVolumeSMART struct {
	Device        string `json:"device,omitempty"`
	ModelFamily   string `json:"model_family,omitempty"`
	ModelName     string `json:"model_name,omitempty"`
	SerialNumber  string `json:"serial_number,omitempty"`
	PowerOnHours  *int64 `json:"power_on_hours,omitempty"`
	HealthPassed  *bool  `json:"health_passed,omitempty"`
	TemperatureC  *int   `json:"temperature_c,omitempty"`
}
