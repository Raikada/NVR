# Media Pipeline Notes

Notes on the media ingest, transcoding (where it exists), and serving
paths inside the Recording Server. The pipeline itself is largely
inherited from MediaMTX and is treated as a stable foundation; this
document is for **observations**, gotchas, and Raikada-specific
extensions, not for redesigning it.

For platform-level context, see
[`../../platform/docs/system-blueprint.md`](../../platform/docs/system-blueprint.md). The
recorder's media-specific responsibilities are in
[`../ARCHITECTURE.md`](../ARCHITECTURE.md) §5.

---

## Status

> **Scaffold.** This document is a placeholder. Notes will accumulate
> here as we work on the pipeline. Treat it as a notebook, not a spec.

## Hard rules

- The media pipeline is load-bearing and not to be refactored without
  explicit request. See [`../AGENTS.md`](../AGENTS.md) §6.
- Codec, container, and segment-format defaults are set by
  `RecordingPolicy` (a canonical entity — see
  [`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md)). Do not
  hardcode them in pipeline code.
- Segment files are immutable once sealed. Any edit to a sealed segment
  is a bug.

## Topics planned for this document

- Ingest: per-protocol notes (RTSP, RTMP, WebRTC, SRT, HLS), reconnect
  behavior, codec-change handling, clock drift.
- Segmenting: when the recorder cuts a new `RecordingSegment`, the
  bounds in `RecordingPolicy.min/max_segment_duration`, and the
  trade-offs.
- Storage write path: how segments hit `StorageVolume`s, fill order,
  and write contention.
- Serving: live re-distribution and historical playback paths,
  including gapless seek across segments.
- Clip preparation: how source segments are stitched, where the export
  lands, checksum, and the segment-pin lifecycle that prevents pruning.
- Failure modes: what happens when a write fails mid-segment, when a
  volume disappears, when the system clock jumps.
