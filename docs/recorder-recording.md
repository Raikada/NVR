# Recorder Recording

How the Recording Server records: segment lifecycle, indexing, the
authoritative metadata it holds for each `RecordingSegment`, and how
that metadata is projected upstream to the Management Server.

The canonical entity is `RecordingSegment` (and `Clip`, which references
ranges of segments) — see
[`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md). Platform
ownership of recording decisions is in
[`../../platform/docs/service-boundaries.md`](../../platform/docs/service-boundaries.md);
the recorder is the authoritative owner of segment files and their
metadata.

---

## Status

> **Scaffold.** Content will land as recording behavior solidifies.

## Hard rules

- A `RecordingSegment` is **immutable once sealed**. Modifying a sealed
  segment is a bug.
- Segment ranges are stored in UTC. The Site's `timezone` is for UI
  conversion only; it never reaches segment metadata.
- Segment `path` is recorder-local. It must never appear in client-
  facing responses; clients reach segments via the recorder's playback
  endpoints.
- The Management Server's segment index is **eventually consistent**
  with the recorder. Disagreements resolve in favor of the recorder.
- The recorder pins segments referenced by active `Clip`s against
  retention pruning. See [`recorder-storage.md`](recorder-storage.md).

## Topics planned for this document

- Segment lifecycle: `recording` → `sealed` → (optionally `archived`)
  → `deleted`, with the events that trigger each transition.
- Naming and on-disk layout: the deterministic naming scheme and why
  it is deterministic.
- Index: the local queryable index, what it stores, and how it stays
  consistent with the on-disk segment files (recovery on restart, etc.).
- Projection upstream: which fields the recorder forwards to the MS,
  cadence, and idempotency keys.
- Clip preparation: the recorder side of the clip lifecycle
  (`requested` → `preparing` → `ready`), source-segment pinning, and
  failure handling.
- Gap handling: what happens when ingest drops mid-segment and how the
  resulting "almost-segment" is closed out.
- Time skew: detecting and reporting clock jumps; never silently
  rewriting segment timestamps.
