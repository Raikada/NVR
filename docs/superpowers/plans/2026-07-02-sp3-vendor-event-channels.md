# SP3 Vendor Event Channels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Camera-side detections (ONVIF PullPoint + Amcrest CGI stream) flow into `events.Service` under supervision, with per-camera channel selection.

**Architecture:** `internal/vendorevents` owns the adapter contract, channel selection, and a per-camera supervisor reconciled off the cameras Bus. Two adapter packages (`onvifchannel`, `amcrestchannel`) normalize vendor payloads onto the seeded `event_types` vocabulary. Core wires the manager; the funnel is `events.Service.Insert` + `camerahealth.Collector.Touch`.

**Tech Stack:** Go; existing `onvif.Manager` PullPoint machinery; Amcrest multipart-CGI parsing ported from `../amcrest-sdk` (vendored subset, digest auth via net/http + digest transport already used by... verify: amcrest-sdk client uses its own digest implementation — port the minimal digest client too if the repo lacks one).

**Spec:** `docs/superpowers/specs/2026-07-02-sp3-vendor-event-channels-design.md`

---

### Task 1: migration 0024 + store plumbing for `event_channel`

**Files:** create `internal/store/migrations/0024_camera_event_channel.sql`; modify `internal/store/cameras.go` (struct field `EventChannel string`, insert/update column lists, scan).

- [ ] RED: extend a cameras repo round-trip test asserting EventChannel persists through Insert/Update/GetByID.
- [ ] Migration: `ALTER TABLE cameras ADD COLUMN event_channel TEXT;` (+goose Down drops nothing — SQLite; use recreate-free additive down comment per existing migration style).
- [ ] GREEN → commit `feat(store): cameras.event_channel column`.

### Task 2: `internal/vendorevents` — contract, selection, supervisor, manager

**Files:** create `internal/vendorevents/{vendorevents.go,supervisor.go,manager.go,vendorevents_test.go,supervisor_test.go}`.

Key types per spec (NormalizedEvent, Adapter, AdapterFactory, ChannelCamera, ErrUnsupported). `SelectChannel(cam ChannelCamera, override string) string` pure function. Supervisor: backoff 1s doubling to 60s cap, reset after 10min stable; funnel inserts + Touch. Manager: `New(store CameraLister-ish via cameras.Service, bus *cameras.Bus, factories map[string]AdapterFactory, sink EventSink, touch func(string, time.Time), log)`; `Run(ctx)` bootstraps + consumes bus; reconcile tears down/starts supervisors on change.

- [ ] RED: selection table test (override wins; amcrest by manufacturer; onvif by xaddr; none). Supervisor test with scripted adapter (emit → sink+touch; error → restart with backoff using injected clock/sleep hook; ErrUnsupported → no restart). Manager reconcile test with fake bus events.
- [ ] GREEN → commit `feat(vendorevents): adapter contract + per-camera supervisor + manager`.

### Task 3: `internal/vendorevents/amcrestchannel`

**Files:** create `internal/vendorevents/amcrestchannel/{adapter.go,parser.go,digest.go,parser_test.go,adapter_test.go}`.

Parser: multipart `--myboundary` blocks → `Code=X;action=Y;index=Z[;data={json}]` lines (port parseEvent/parseEventBlock semantics from ../amcrest-sdk/event.go — reimplement, keep MIT attribution comment). Mapping + action filter per spec table. Adapter: GET `http://<host>/cgi-bin/eventManager.cgi?action=attach&codes=[...]&heartbeat=5` with digest auth, scan blocks, 90s liveness deadline (heartbeat refreshes), context-cancel clean exit.

- [ ] RED: parser fixtures (VideoMotion Start/Stop, SmartMotionHuman with data JSON, doorbell codes, Heartbeat), mapping table, adapter test against httptest server streaming fixture blocks (asserts emitted NormalizedEvents + liveness timeout error with short deadline override).
- [ ] GREEN → commit `feat(vendorevents/amcrest): CGI attach adapter`.

### Task 4: `internal/vendorevents/onvifchannel`

**Files:** create `internal/vendorevents/onvifchannel/{adapter.go,mapping.go,mapping_test.go,adapter_test.go}`; modify `internal/onvif/manager.go` only if a per-camera notification callback hook is missing (inspect: manager currently dispatches parsed PullMessages → check how notifications surface; add a `SetNotificationSink(func(SubscriptionRecord, Notification))` if needed, additive).

Mapping: canonical kind → type id table per spec + rising-edge filter keyed topic+source token (property events State true/false).

- [ ] RED: mapping table test; rising-edge test with synthesized notifications; adapter lifecycle test with a fake manager seam.
- [ ] GREEN → commit `feat(vendorevents/onvif): PullPoint adapter`.

### Task 5: API + store-sync surface for `event_channel`

**Files:** modify `internal/defs/camera.go` (field `EventChannel string \`json:"event_channel,omitempty"\``), `internal/api/api_v1_cameras.go` (cameraFromConfPath/mergeCameraOntoConfPath passthrough — the value lives on the store row, NOT the conf path; PATCH handler reads/writes via syncCameraStoreUpdate), `internal/api/api_v1_cameras_store_sync.go` (map EventChannel), tests in `api_v1_cameras_db_test.go`.

- [ ] RED: PATCH with `{"event_channel":"onvif"}` persists to store row and echoes on GET.
- [ ] GREEN → commit `feat(api): per-camera event_channel selection`.

### Task 6: core wiring + vendor registry

**Files:** modify `internal/core/core.go` (field `vendorEvents *vendorevents.Manager`, start after healthCollector, nil on shutdown), `internal/core/foundation_wiring.go` (factory registry: onvif + amcrest real; hikvision/reolink entries returning ErrUnsupported with a "not implemented" log; ChannelCamera construction incl. capabilities EventsXAddr lookup + PlaintextCredentials closure).

- [ ] Build + boot smoke (recorder starts; manager logs bootstrap with 0 or N channels).
- [ ] Commit `feat(core): wire vendor event channel manager`.

### Task 7: SPA — events channel select + last-event line

**Files:** `web/src/routes/Cameras.tsx` drawer, `web/src/lib/{api,types}.ts`. May be batched with SP4's SPA work to amortize `make web`; if batched, note it in the SP4 commit.

- [ ] Drawer select Auto/ONVIF/Amcrest/Off → PATCH event_channel; last-event line from health.last_event_at.
- [ ] `make web`; commit `feat(web): per-camera event channel selector`.

### Task 8: live acceptance + tag

- [ ] Boot scratch instance; adopt/ensure amcrest_110 + doorbell with creds; webhook subscription on motion+doorbell to local sink.
- [ ] Walk test → motion (± person) event within 5s, webhook delivered, health last_event_at stamps.
- [ ] AD410 button press → doorbell event.
- [ ] `go test -p 1 ./internal/...` (pre-existing core flakes only), `go vet ./internal/...`.
- [ ] Tag `v1.2.0-vendor-events`, push.

## Self-review notes
- Poison events: supervisor funnel logs-and-drops Insert errors (test in Task 2).
- Doorbell mapping subtlety: AD410 codes vary by firmware (CallNoAnswered vs Invite vs _DoTalkAction_) — adapter subscribes `codes=[All]` and filters in the parser, so unexpected codes are visible in debug logs during live acceptance rather than silently unsubscribed.
- The onvif.Manager notification-sink hook is the one open integration risk; Task 4 starts by reading manager.go/pullpoint.go and adapts (additive hook only).
