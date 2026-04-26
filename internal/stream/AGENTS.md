# AGENTS.md — `internal/stream/`

This package is the **live media pipeline core**. A `Stream` represents a
single live media flow inside the recorder; `Reader` consumes it via
`OnDataFunc` callbacks per `(media, format)` pair. RTP encoding/decoding
sits in `rtp_encoder.go` / `rtp_decoder.go`; format-specific bridging
between protocols runs through `format_updater.go` and `unit_remuxer.go`;
"always available" / offline streams are backed by the embedded
`offline_*.mp4` files via `offline_sub_stream*.go`.

## Entry points

- `Stream` (`stream.go`) — the central type. Producers write into it,
  readers subscribe.
- `Reader` (`reader.go`) — registers `OnDataFunc`s, drains a
  `ringbuffer.RingBuffer`, and reports when frames were discarded.
- `SubStream`, `SubStreamFormat`, `SubStreamMedia` — per-format
  consumer-side state.

## Boundary

This package speaks **MediaMTX-internal vocabulary** (`description.Media`,
`format.Format`, `unit.Unit`). It does **not** translate to canonical
Raikada entities — that translation happens upstream in `internal/api/`
and `internal/defs/`, and downstream in `internal/recorder/`.

## Gotchas

- The ringbuffer queue size is producer-set; readers cannot resize.
  Discards are surfaced via `outboundFramesDiscarded`, not silently.
- A `Reader` is single-consumer and not thread-safe across multiple
  goroutines for the same instance.
- `offline_*.mp4` files are committed binaries used by always-available
  streams; do not regenerate them casually.

## Discipline

Per root [`../../AGENTS.md`](../../AGENTS.md) §6: **do not refactor or
"clean up" this package without an explicit request that names the
file and the goal.** Adding observability (metrics, structured logs)
around the existing flow is fine when asked. Changing concurrency or
buffer semantics is not.

See also workspace [`../../../platform/CLAUDE.md`](../../../platform/CLAUDE.md).
