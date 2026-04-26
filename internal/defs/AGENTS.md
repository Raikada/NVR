# AGENTS.md — `internal/defs/`

This package holds **shared type definitions**, including every
JSON-serializable shape that the HTTP API exposes on the wire. It is
the **canonical-entity translation seam** of the recorder: the place
where MediaMTX-internal vocabulary is rendered into a public form.

## Entry points

- `defs.go`, `api.go` — basic shared types (`APIOK`, `APIError`).
- `api_*.go` — one file per externally-visible resource family:
  `api_path.go`, `api_path_track*.go`, `api_recording.go`,
  `api_hls.go`, `api_rtmp.go`, `api_rtsp.go`, `api_srt.go`,
  `api_webrtc.go`. Each defines the JSON shapes used by
  `internal/api/`.
- Internal protocol contracts: `path.go`, `path_access_request.go`,
  `publisher.go`, `reader.go`, `source.go`, `static_source.go`. These
  are interfaces between subsystems, not wire shapes.

## Boundary

This package is **a public API boundary**. Any field on an `API*`
struct, any JSON tag, any new exported type intended for marshalling
becomes part of the wire contract the moment it is consumed by
`internal/api/`. Renaming a field here renames it on every client.

Many of these types correspond to canonical entities defined in
[`../../../platform/docs/domain-model.md`](../../../platform/docs/domain-model.md):

- `APIRecording*` ↔ `RecordingSegment`
- `APIPath*` ↔ today's MediaMTX `Path` (currently MediaMTX-internal;
  Raikada's canonical equivalent is `Camera` / `Stream` and these
  shapes will need to converge or translate on the boundary).
- `APIPathTrack*` ↔ `Stream.tracks`.

When you touch a type that maps to a canonical entity, the canonical
shape in `domain-model.md` is the contract to honor.

## Gotchas

- Types here are `json:"..."`-tagged; tag changes are wire-breaking.
- Some types implement protocol interfaces (`Publisher`, `Reader`,
  `Source`); changing those signatures ripples into every protocol
  implementation under `internal/servers/`.
- This package imports `internal/conf` but most things here should
  not. Keep new types light on dependencies so they don't pull the
  whole graph during compilation.

## Discipline

A rename here is a non-additive contract change. Per root
[`../../AGENTS.md`](../../AGENTS.md) §1 and §3, that requires updating
[`../../../platform/docs/domain-model.md`](../../../platform/docs/domain-model.md), and
once API contracts exist, the relevant doc under
[`../../../platform/docs/api-contracts/`](../../../platform/docs/api-contracts/) — in
the same change set. See workspace
[`../../../platform/CLAUDE.md`](../../../platform/CLAUDE.md).
