# Phase A acceptance — 2026-10-15 (v1.4.0)

Live acceptance of the Phase A client-API surface against a scratch
recorder (fresh identity + store, API on :19997, playback on :19996),
run after the full Go sweep. All 17 checks green.

## What shipped (B1–B8)

| Batch | Surface |
|---|---|
| B1 | `POST /v1/auth/refresh` (sliding session); `?access_token=` promoted to Bearer on `/v1/events/stream` only |
| B2 | Five-role RBAC: admin, owner, viewer, operator, audit; token scope drives claims |
| B3 | `POST /v1/events/acknowledge-all`; `POST /v1/cameras/:id/bookmarks` (plain `bookmark` events) |
| B4 | playback server enabled in docker config (:9996); `GET /v1/cameras/:id/coverage?from&to` |
| B5 | `POST /v1/cameras/:id/ptz/{move,stop,home,presets,presets/:token/goto}`; `POST /v1/cameras/:id/reboot` |
| B6 | `GET /v1/cameras/:id/stats`; `GET/PUT /v1/me/prefs` (merge-upsert, opaque JSON) |
| B7 | probe resolves `sub_stream_uri`; PathBridge registers `<name>_sub` (record off) |
| B8 | SPA: EventSource `?access_token=`, 10-min sliding refresh; bundle rebuilt |

## Acceptance checks (scratch instance)

login, forced password change, auth-refresh, SSE with/without token,
operator create/login/role/403-enforcement, camera create, stats
(available-based online), coverage on never-recorded camera (200 []),
PTZ 409 on non-PTZ camera + 404 on unknown, bookmark 201, ack-all,
prefs merge round-trip. Script:
`verify_phase_a.sh` (session scratchpad; recreate from this list if needed).

## Bugs found and fixed during verify

- **POST /v1/cameras with `source_type: publish` crashed the recorder**
  (since the path bridge landed, incl. v1.3.1 on the NAS):
  `cameraSpecToConfPath` overwrote `Source: publisher` with the empty
  source URL, routing into `staticsources.Handler.Initialize`'s
  `panic("should not happen")`. Fixed: empty SourceURL keeps the base
  Source.
- `/v1/cameras/:id/stats` said `online: true` for unreachable cameras
  (`APIPath.Online` = handler attached, not stream up). Now maps
  `Available`/`AvailableTime`.
- `/v1/cameras/:id/coverage` 500ed for cameras that never recorded
  (missing per-path dir). `fs.ErrNotExist` → empty coverage.
- SPA `refreshSession` parsed the flat recorder/login shape;
  `/v1/auth/refresh` returns the consumer shape (`access_token` +
  nested `user`).

## Test-sweep notes (this Mac, not CI)

- Core suite needs a **clean `TMPDIR`**: tests write their conf into the
  temp root and the conf watcher (fsnotify/kqueue) opens an fd per
  entry of the watched parent dir — a crowded macOS temp root (~1.7k
  entries) exhausts fds and fails ~20 tests. `TMPDIR=$(mktemp -d) go
  test ./internal/core/ -skip 'TestAPIProtocolListGet/webrtc'` is green.
- Environment-bound upstream failures on macOS (pass in Linux CI):
  `TestAPIProtocolListGet/webrtc` (ICE hang), `protocols/webrtc`
  ICE-candidate count (interface-count dependent), `protocols/udp`
  TestListen (read-buffer API unimplemented on darwin).
- Don't run two suites that bind fixed ports (1935 etc.) concurrently.
- Core tests share `dirname(conf)/identity/recorder.db`; a crashed run
  leaves stale camera rows that break `TestPathManagerConfigHotReload`.
