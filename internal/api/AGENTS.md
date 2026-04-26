# AGENTS.md — `internal/api/`

This package is the **HTTP API server**. It is one of the recorder's
public surfaces — every JSON shape that leaves this package is a wire
contract, and the rule from root [`../../AGENTS.md`](../../AGENTS.md)
§3 ("public API shape changes require a domain-model.md update")
binds here in particular.

## Entry points

- `API` (`api.go`) — the Gin server, the routing wiring, and shared
  helpers (`paramName`, `interfaceIsEmpty`, `sortedKeys`).
- Per-feature handlers in `api_*.go`: `paths`, `recordings`, `hls`,
  `rtmp`, `rtsp`, `srt`, `webrtc`, plus `config_*`. One file per
  feature area; each file's tests live next to it
  (`api_*_test.go`).
- Pagination helper in `paginate.go`.
- Test fixtures in `testdata/`.

## Boundary

- **Inbound.** HTTP requests from the Management Server, web UI,
  Flutter client, broker-tunnelled clients. All untrusted until a
  token is validated upstream by `internal/auth/`.
- **Outbound.** JSON shapes — these are defined in
  `internal/defs/api_*.go`, **not** here. Keep handlers thin: parse,
  authorize, call into core, marshal a `defs.*` type. Don't define
  response types in this package.
- **Translation seam.** Wherever a handler accepts or returns a
  canonical entity (`Camera`, `RecordingSegment`, `Clip`,
  `RecordingPolicy`, …) the wire shape lives in `internal/defs/`. Any
  rename / type change is a public-API change requiring a docs
  update.

## Gotchas

- Tests call into the same `gin.Engine`; don't share global state
  between handlers.
- `interfaceIsEmpty` is a defensive check around nil-typed-pointer
  bugs; keep it where it is unless you understand the failure mode.
- `paginate.go` enforces a consistent contract across list endpoints —
  do not roll your own pagination per handler.

## Discipline

Adding a route or changing a response shape is **not** an internal
edit. It is a wire-format change. See root
[`../../AGENTS.md`](../../AGENTS.md) §1, §3 and workspace
[`../../../platform/CLAUDE.md`](../../../platform/CLAUDE.md). New endpoints default to
authenticated access (root §9).
