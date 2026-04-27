// Package conf: RecordingVolumeConfig — operator-set bootstrap
// metadata for storage volumes per ADR 0009 §D5.
//
// StorageVolume entities are surfaced read-only at /v1/storage-volumes
// from a synthesis walk over conf.Path.RecordPath roots; the canonical
// model includes a `priority` field ("order in which the recorder fills
// volumes") that, until now, the recorder filled with the deterministic
// mount-path-sort index — adequate for read-only synthesis, but
// providing no operator control.
//
// This file adds a top-level `recordingVolumes:` map keyed by mount
// path so operators can override the synthesized priority via
// /v1/recorder/config (the bootstrap escape hatch). The map is
// optional: unset mount paths fall back to the sort-index behavior, so
// upgrading deployments see no change.
//
// YAML/JSON tags use camelCase to match the rest of the conf package's
// existing convention.
package conf

import "fmt"

// RecordingVolumeConfig is the persistence shape for one operator-set
// storage-volume override. Keyed in conf.Conf.RecordingVolumes by
// absolute mount path. Today this carries only Priority; additional
// per-volume bootstrap fields (kind override, reservation hints, etc.)
// can extend this struct without breaking on-disk shape.
type RecordingVolumeConfig struct {
	// Priority is the operator-set fill order for this volume. Pointer
	// so an absent value cleanly distinguishes "no override" from
	// "explicitly set to 0". Lower values fill first per ADR 0009 §D5
	// (see also defs.StorageVolume.Priority).
	Priority *int `json:"priority,omitempty"`
}

// validate checks one RecordingVolumeConfig entry. Called by
// Conf.Validate() per-key as part of the recordingVolumes: top-level
// map walk. Mount-path string format is intentionally not validated
// here — the recorder accepts both absolute and relative paths in
// conf.Path.RecordPath, and we mirror that latitude on the override
// side.
func (rv *RecordingVolumeConfig) validate(mountPath string) error {
	if mountPath == "" {
		return fmt.Errorf("recording volume has empty mount-path key")
	}
	if rv.Priority != nil && *rv.Priority < 0 {
		return fmt.Errorf("recording volume '%s' has negative priority %d",
			mountPath, *rv.Priority)
	}
	return nil
}
