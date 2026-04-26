# AGENTS.md — `internal/recordstore/`

This package handles **on-disk segment storage and retrieval**: scanning
the filesystem for existing recordings, parsing their boundaries,
expanding the `PathFormat` template into concrete paths, and reading
just enough of an MP4 container to answer playback queries. It is the
authoritative side of the segment index that the Management Server
projects.

## Entry points

- `Segment` (`segment.go`) — the in-memory shape of one on-disk
  recording (`Fpath`, `Start`).
- Segment scanning helpers in `segment.go` (`FindSegments`,
  `ErrNoSegmentsFound`, etc.).
- `Path` template handling in `path.go` — translates the user-set
  `PathFormat` into a glob and back into a parsed timestamp.
- MP4 box utilities in `mp4_boxes.go` — used by playback to read
  segment internals without mounting the full container.

## Boundary

- **Input.** A `*conf.Path` (or its successor) plus the filesystem.
- **Output.** Sorted lists of `Segment`s and helpers for opening one
  for read.
- Maps to the canonical `RecordingSegment` entity defined in
  [`../../../platform/docs/domain-model.md`](../../../platform/docs/domain-model.md).
  This package is authoritative; the MS-side index is a projection.

## Gotchas

- `Fpath` is a **recorder-local** path. It must never appear in
  client-facing API responses (root
  [`../../AGENTS.md`](../../AGENTS.md) §3 / §9).
- Segment ordering is chronological by `Start`. Do not assume it is
  also alphabetical — different `PathFormat` templates break that.
- `mp4_boxes.go` reads box headers off disk by offset. Edits here
  must be backwards-compatible with already-recorded files; you
  cannot reformat an existing fleet.
- `ErrNoSegmentsFound` is a normal not-found, not an error. Treat it
  as such at call sites.

## Discipline

Part of the media-adjacent pipeline; the root rule against drive-by
refactors applies. See [`../../AGENTS.md`](../../AGENTS.md) and
workspace [`../../../platform/CLAUDE.md`](../../../platform/CLAUDE.md).
