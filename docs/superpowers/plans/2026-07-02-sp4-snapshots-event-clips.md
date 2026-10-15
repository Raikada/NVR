# SP4 Snapshots + Event-to-Clip Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Events get JPEG snapshots + thumbnails at capture time, signed media URLs on every surface, and lazy pre/post-roll clip extraction.

**Architecture:** `internal/snapshots` consumes the events bus with a fetch ladder (Amcrest CGI → ONVIF URI → live frame grab); `internal/mediasign` signs short-TTL media paths served by anonymous-with-signature API endpoints; clip creation reuses the existing remux pipeline keyed off the event window; retention grows a file pass.

**Spec:** `docs/superpowers/specs/2026-07-02-sp4-snapshots-event-clips-design.md`

---

### Task 1: migration 0025 `event_types.capture_snapshot` + store plumbing
Files: `internal/store/migrations/0025_event_types_capture_snapshot.sql`; `internal/store/event_types.go` (+test).
`ALTER TABLE event_types ADD COLUMN capture_snapshot INTEGER NOT NULL DEFAULT 1; UPDATE event_types SET capture_snapshot = 0 WHERE id IN ('camera_online','camera_offline');`
- [ ] RED round-trip test → GREEN → commit `feat(store): event_types.capture_snapshot`.

### Task 2: `internal/mediasign`
Files: `internal/mediasign/{mediasign.go,mediasign_test.go}`.
`Open(identityDir)` loads/creates `media-sign.key` (0600). `Sign(path, ttl clock-injected) (exp int64, sig hex)`, `Verify(path, exp, sig)` constant-time, expiry-checked.
- [ ] RED (round-trip, tamper, expiry, key persistence across Open) → GREEN → commit `feat(mediasign): HMAC-signed short-TTL media URLs`.

### Task 3: `internal/snapshots` service
Files: `internal/snapshots/{service.go,fetch.go,thumbnail.go,service_test.go,fetch_test.go}`.
- Subscribe to events bus; skip types with capture_snapshot=0 (store lookup, 60s cached).
- Fetch ladder per spec; camera info via resolver seam mirroring vendorevents (host, snapshot URI from capabilities vendor JSON, creds closure, frame-grab func injected by core).
- Per-camera single-flight + 2s burst reuse; 5s fetch timeout; bounded work queue (drop-oldest + warn).
- Write full + 320px thumb (nearest-neighbor, stdlib only) to `<snapshot_root>/<camera_name>/<yyyy-mm-dd>/<event_id>-<kind>.jpg`; insert `event_snapshots` rows.
- [ ] RED (ladder order with fake servers, burst reuse, skip flag, layout + thumb size, queue overflow) → GREEN → commit `feat(snapshots): on-event JPEG capture with fetch ladder`.

### Task 4: media API + event wire URLs
Files: `internal/api/api_v1_media.go` (+test), modify `internal/api/api.go` (pre-auth bypass for /v1/media with mandatory verify; Signer field), `internal/api/api_v1_events.go` + `api_v1_events_ext.go` (snapshot_url/thumbnail_url/clip_id on wire), `internal/notifications/*` payload builder (URLs into webhook payload — check where snapshot_url placeholder lives).
- [ ] RED (signed fetch 200; bad sig 403; expired 403; events list carries URLs when rows exist) → GREEN → commit `feat(api): signed media endpoints + event snapshot URLs`.

### Task 5: event-to-clip
Files: `internal/api/api_v1_events_clip.go` (+test), reuse `clip_pipeline.go`/`clip_store.go`; policy pre/post-roll resolution (default 5s/5s).
- [ ] RED (create → clip row + segments + file; idempotent second POST; 409 when segments swept; signed download URL) → GREEN → commit `feat(api): lazy event-to-clip extraction`.

### Task 6: retention file pass
Files: `internal/retention/*.go` (+test): events sweeper collects `event_snapshots.path` before delete, unlinks after; startup orphan sweep under snapshot_root.
- [ ] RED → GREEN → commit `feat(retention): snapshot file sweep`.

### Task 7: core wiring
Files: `internal/core/core.go`, `internal/core/foundation_wiring.go`: snapshots service start (events bus + store + resolver + frame-grab adapter into `api` snapshot internals), mediasign.Open at identity load, Signer into API.
- [ ] Build + boot smoke → commit `feat(core): wire snapshot capture + media signing`.

### Task 8: SPA — Events page + drawer strip + SP3 channel select (batched)
Files: `web/src/routes/Events.tsx` (new), `web/src/App.tsx` route, `web/src/lib/{api,types}.ts`, Cameras drawer.
- [ ] Events list (thumbnails via signed URLs, filters, ack, export-clip button, SSE prepend); drawer recent-events strip; SP3 event-channel select. `make web`. Commit including `internal/web/dist`.

### Task 9: live acceptance + tag
- [ ] Walk-test → snapshot JPEG + thumb on disk, webhook signed URLs fetch, export clip plays; doorbell snapshot; retention spot-check (manual sweep endpoint with short retention type).
- [ ] `go test -p 1 ./internal/...` + vet; cypress spec for Events page (mocked) if the existing cypress harness makes it cheap.
- [ ] Update smoke runbook Steps 4-5 with the now-real event pipeline; tag `v1.3.0-snapshots-clips`; push; refresh PR #1 description.

## Self-review notes
- Snapshot fetch must never block the events bus consumer: queue decouples (Task 3).
- Signed endpoints bypass auth — verify() is mandatory before any file read; paths are always server-derived (event/clip ids), never client paths (no traversal).
- Frame-grab fallback depends on api snapshot internals — Task 7 exposes it as a func(cameraName) ([]byte, error) adapter rather than importing api from snapshots (import cycle).
