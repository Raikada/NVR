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
}
