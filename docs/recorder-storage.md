# Recorder Storage

How the Recording Server manages local storage: `StorageVolume`
inventory, fill order, retention pruning, and the failure modes of disks
under sustained recording.

The canonical entity is `StorageVolume` — see
[`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md). Platform
ownership of storage decisions is in
[`../../platform/docs/service-boundaries.md`](../../platform/docs/service-boundaries.md);
the recorder is the authoritative owner.

---

## Status

> **Scaffold.** Content will land as storage handling solidifies.

## Hard rules

- The recorder is the authoritative source of `StorageVolume` state.
  Cloud and Management Server hold projections.
- Pruning is governed by `RecordingPolicy.retention_duration`, not by
  hardcoded TTLs in storage code.
- A `RecordingSegment` referenced by an active `Clip` (state
  `requested` / `preparing` / `ready`) **must not be pruned**, even
  past policy retention. See
  [`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md) §Clip notes.
- Refusing writes when storage is exhausted is correct behavior, but
  the recorder must emit a high-severity `Event` so operators see it.

## Topics planned for this document

- Volume types: `internal_disk`, `external_disk`, `network_share`,
  `ramdisk` — when each is appropriate and what its failure modes look
  like.
- Fill order: how the recorder picks a volume per camera, given
  multiple healthy volumes.
- Reserved bytes: the headroom that must not be filled, and the
  behavior near that limit.
- Pruning: the order in which segments are pruned, how clip pins
  override pruning, and what happens when no segments are prunable.
- Clip-export storage: separating clip artifacts from raw segments to
  prevent write contention.
- Volume disappearance: behavior when a mount goes away mid-recording.
- Health surfacing: how volume state propagates into `HealthStatus`
  heartbeats.
