package defs

import (
	"time"
)

// StorageVolumeInput is the recorder's internal snapshot of one volume.
// The recorder doesn't carry a clean canonical-shape internal type today;
// volume state lives near the recordstore subsystem and the
// /v1/recorder/config bootstrap path. This input contract names what
// BuildStorageVolume needs.
type StorageVolumeInput struct {
	MountPath     string
	Kind          StorageVolumeKind
	CapacityBytes int64
	UsedBytes     int64
	ReservedBytes int64
	Status        StorageVolumeStatus
	LastCheckedAt time.Time
	Priority      int
}

// BuildStorageVolume synthesizes a canonical StorageVolume from internal
// recorder volume telemetry. Read-only at this surface per ADR 0009 §D2;
// volume bootstrap (mount path, kind, priority) happens via
// /v1/recorder/config and stays out of this translator's scope.
//
// Caller supplies a per-call volume id (UUID) and the recorder's own
// recording_server_id. LastCheckedAt defaults to time.Now() when zero.
func BuildStorageVolume(in StorageVolumeInput, volumeID, recordingServerID string) StorageVolume {
	checked := in.LastCheckedAt
	if checked.IsZero() {
		checked = time.Now().UTC()
	}
	status := in.Status
	if status == "" {
		status = StorageVolumeStatusHealthy
	}
	return StorageVolume{
		ID:                volumeID,
		RecordingServerID: recordingServerID,
		MountPath:         in.MountPath,
		Kind:              in.Kind,
		CapacityBytes:     in.CapacityBytes,
		UsedBytes:         in.UsedBytes,
		ReservedBytes:     in.ReservedBytes,
		Status:            status,
		LastCheckedAt:     checked,
		Priority:          in.Priority,
	}
}
