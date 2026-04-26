# AGENTS.md — `internal/recorder/`

This package **produces `RecordingSegment` files on disk** for one path.
It is the load-bearing producer of the canonical `RecordingSegment`
entity defined in
[`../../../platform/docs/domain-model.md`](../../../platform/docs/domain-model.md). If
this package is wrong, footage is lost — see
[`../../ARCHITECTURE.md`](../../ARCHITECTURE.md) §9.

## Entry points

- `Recorder` (`recorder.go`) — the public type. Configured with a
  `*stream.Stream`, a `PathFormat` template, a `RecordFormat`
  (`fmp4` or `mpegts`), part / segment durations, and lifecycle
  callbacks `OnSegmentCreate` / `OnSegmentComplete`.
- `recorderInstance` (`recorder_instance.go`) — the active recording
  state machine. A `Recorder` re-creates instances on stream restart.
- Format dispatch: `format.go` selects between `format_fmp4.go` and
  `format_mpegts.go`; segment writers live in `format_*_segment.go`;
  fMP4 part writer in `format_fmp4_part.go`.

## Boundary

- **Input.** Decoded media units pulled from `*stream.Stream` via a
  `Reader`.
- **Output.** Segment files on disk, plus
  `OnSegmentCreate(path)` / `OnSegmentComplete(path, duration)`
  callbacks. The path follows `PathFormat`; emit it for indexing —
  see `internal/recordstore/`.

## Gotchas

- `ntpDriftTolerance = 5 * time.Second`. Larger NTP jumps trigger a
  reset rather than silently rewriting timestamps.
- Segment durations are governed by `RecordingPolicy` upstream; this
  package consumes them as values, it does not enforce policy
  semantics.
- `restartPause` exists so that a misbehaving stream cannot busy-loop
  the recorder. Don't shorten it without a stated reason.
- A `RecordingSegment` is **immutable once sealed**. Modifying a
  finalized segment file is a bug.

## Discipline

Part of the media pipeline — root [`../../AGENTS.md`](../../AGENTS.md)
§6 applies. Do not change codec, container, or segment-format defaults
without an ADR. See workspace
[`../../../platform/CLAUDE.md`](../../../platform/CLAUDE.md).
