# Real-Camera Smoke Test — Results (2026-07-02)

Run against three live LAN cameras from a scratch instance of the
`consumer-foundation` branch (tag `v1.0.0-foundation`, tip `e99c366f`).
Cameras: 2× Amcrest IP5M-T1277EW-AI (192.168.1.110, .218), 1× Amcrest
AD410 doorbell (192.168.1.111). All discovered via ONVIF WS-Discovery;
all require RTSP digest auth.

## Verdict

The media pipeline works end-to-end against real hardware: credentialed
RTSP pull, live restream, fmp4 recording segments, segments API, webhook
delivery with valid HMAC, audit trail with clean redaction. But the
**camera CRUD API is split-brained between the legacy conf-path layer and
the foundation DB layer**, which produces one hard crash (F2) and two
broken flows (F1, F3). Fix F1–F3 before merging to main.

## Step results

| Runbook step | Result | Notes |
|---|---|---|
| Boot + identity | PASS | clean boot, admin seeded, forced rotation works |
| 1 Setup | PASS | `setup_required` flips false; initial-password file self-deletes; `must_change_password=0` |
| 2 Add camera | PASS w/ bugs | live H264+AAC restream via `:8554`; no plaintext password in API/yml/audit — but see F1, F2 |
| 3 Recording | PASS | fmp4 segments on disk (246 s / 101 MB verified via ffprobe); `/v1/recording-segments` row correct |
| 4 Manual event | NOT POSSIBLE | `POST /v1/events` doesn't exist (F5) |
| 5 Webhook | PASS | test-endpoint delivery to local sink; `X-Raikada-*` headers present; HMAC-SHA256 verifies |
| 6 Audit | PASS | logins, password change, `camera.credentials_rotated`, target created/tested; no secret leakage (F6 naming nit) |
| Cleanup (cascade delete) | FAIL | conf path removed, DB rows orphaned (F3) |

## Findings

**F1 — `POST /v1/cameras` never writes the `cameras` DB row.**
`onV1CamerasPost` (`internal/api/api_v1_cameras.go:267`) is the legacy
conf-path handler; it updates mediamtx.yml only. Phase 5 Task 5.2
explicitly deferred the Service rewire (commit `3808c6b1`). Consequence:
`PUT /v1/cameras/:id/credentials` → `CamerasService.SetCredentials` fails
with `FOREIGN KEY constraint failed` for every API-created camera. The
credential vault is unreachable in the shipped binary without hand-editing
SQLite.

**F2 — Hot credential rotation panics the whole recorder (crash).**
`cameraSpecToConfPath` (`internal/core/foundation_wiring.go:84`) builds the
path conf from `defaultPaths[name]` — a snapshot taken at boot. For a
camera added after boot the base is nil, so it returns a zero-value
`conf.Path` (no `setDefaults`), and the RTSP dialer indexes the empty
`RTSPUDPSourcePortRange` → `panic: index out of range [0] with length 0`
at `internal/staticsources/rtsp/source.go:137`, killing the process.
Reproduced live. Workaround: restart (persisted yml paths become the
defaults). Fix: run the merged path through Validate/setDefaults, and
bounds-check source.go:137.

**F3 — `DELETE /v1/cameras/:id` orphans DB rows.** Mirror of F1: the
legacy handler removes the conf path only. `cameras`,
`camera_credentials` rows survive; the ON DELETE CASCADE never fires;
`CamerasService.Delete`/OnvifTeardown are never invoked.

**F4 — `camera_health` is never written.** `rtsp_state` stays "unknown"
with live streams up. Runbook Step 2 expects `"connected"`. Either wire a
health writer in the foundation or move that pass-criterion to SP2.

**F5 — Runbook Step 4 is unimplementable.** No `POST /v1/events`; events
API is read-only and nothing outside tests calls `events.Service.Insert`.
The event→subscription→notification path has no end-to-end exercise until
SP2/SP3 land a producer. Correct the runbook; consider an admin-only
synthetic-event endpoint for acceptance testing.

**F6 — Audit action names diverge from runbook.** Camera create/delete
land as `config.applied`; no `system.bootstrap_completed` row exists.
Cosmetic, but reconcile emitter vs. docs.

**F7 — RecordingPolicy id scheme is split.** Conf layer stamps
`00000000-0000-0000-0000-000000000001`; DB seeds `policy_default`. Any
future join across the two layers will miss.

**F8 — Store timestamp parsing is brittle.** `store` requires
`2006-01-02T15:04:05.000Z07:00` (exactly three fractional digits) and
errors on plain RFC3339 (`...:36Z`). Accept standard RFC3339 on read.

**F9 (minor) — webhook dispatch deadline is ~1 s** (first delivery to a
cold local listener timed out); confirm intended timeout for slow targets.
**F10 (minor) — `storage volume degraded (statfs_failed)`** warned at boot
when the recordings dir didn't exist yet.

## Fix pass — same day, verified against the same cameras

F1–F3 fixed and re-verified live from a fresh identity dir:

- **F1** — the camera CRUD handlers now mirror every create/patch/put onto
  the canonical store row (`internal/api/api_v1_cameras_store_sync.go`);
  all three API-created cameras appeared in `cameras` immediately and the
  credentials PUT worked first try.
- **F2** — two layers: the PathBridge adapter refreshes its path snapshot
  on every conf apply and synthesizes from validated `PathDefaults` when a
  camera has no conf path (`internal/core/foundation_wiring.go`), and the
  RTSP dialer no longer indexes an empty port range blind
  (`internal/staticsources/rtsp/source.go`). Live re-test: hot credential
  rotation on 3 post-boot cameras → zero panics, all streams recording
  within ~1 s, **no restart required**.
- **F3** — DELETE now cascades: `cameras` 3→2, `camera_credentials` 3→2,
  conf path gone, in one call.
- **F4/F5/F6/F9** — runbook corrected to match the implementation
  (`real-camera-smoke.md`): no `POST /v1/events`, `rtsp_state` criterion
  moved to SP2, audit action names, webhook dispatch timeout note, config
  field fixes (`--confpath`→positional, no `recordingsDir`/`databasePath`).
- Regression tests added: `internal/api/api_v1_cameras_db_test.go`,
  `internal/core/foundation_wiring_test.go`, and
  `TestZeroValuePathConfDoesNotPanic` in `internal/staticsources/rtsp`.
  Each watched failing before the fix (the rtsp one reproduces the exact
  production panic).

Still open (deferred, non-blocking): F7 policy-id namespace split (store
rows intentionally leave `recording_policy_id` empty → readers fall back
to `policy_default`), F8 strict timestamp parsing, F10 statfs warning.
Pre-existing `internal/core` suite-order test flakiness (19 tests fail
together, all pass in isolation) confirmed identical with and without
these changes.

## Notes for the fix pass

- The crash-then-recover behavior showed DB writes commit even when the
  HTTP response is lost mid-panic — credentials silently landed for all
  three cameras despite two "failed" PUTs. Fine, but explains confusing
  client-side errors.
- Redaction held everywhere: API responses, persisted yml, audit rows.
  `MaterializeRTSPURL` credentialed URLs never touch disk.
- Smoke instance data (identity dir, recordings, admin password) lived in
  a session scratchpad and can be deleted freely.
