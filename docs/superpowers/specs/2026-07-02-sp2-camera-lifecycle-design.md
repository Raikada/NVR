---
title: SP2 — Camera lifecycle (discovery, adopt, capability probe, health)
date: 2026-07-02
scope: Sub-project 2 of 4 (consumer NVR)
depends-on: 2026-05-07-consumer-nvr-foundation-design.md
branch: consumer-foundation
---

# SP2 — Camera lifecycle

Foundation (SP1) gave the recorder manual camera addition: the operator
types an RTSP URL and credentials into a form. SP2 makes cameras
first-class lifecycle objects: the recorder finds them on the LAN, adopts
them with one probe, knows what they can do, and tells the operator when
they stop working.

User-approved UX: **discover → review → adopt**. No camera is touched
until the operator acts on a discovered entry.

## Out of scope

- Vendor event channels (SP3) — but `camera_health.last_event_at` is the
  seam they will write through.
- Snapshots on events (SP4). The existing live-snapshot handler stays.
- Firmware tracking/updates, camera-side config mirroring (post-SP4).
- PTZ control (capabilities record `has_ptz`; no control surface).

## Components

### 1. Discovery service — `internal/discovery`

Wraps the existing `onvif.Discoverer` (WS-Discovery probe) in a cached,
periodically-refreshed service.

- **Probe cadence**: on startup, every 60s thereafter, and on demand via
  API. A probe is one multicast round with the existing 3s window.
- **Cache**: in-memory map keyed by `EndpointRef` (stable WS-Addressing
  ID). Entries carry `DiscoveredDevice` fields + `first_seen_at`,
  `last_seen_at`, and `matched_camera_id` (non-empty when an existing
  camera row claims the same XAddr host or source-URL host). Entries not
  seen for 10 minutes are dropped. Nothing persists — a reboot re-probes.
- **Matching**: by host (IP or name) extracted from `cameras.source_url`
  and `cameras.onvif_xaddr`. Matching is advisory display state only.
- Concurrency: one goroutine owns the cache; API reads go through a
  mutex-guarded snapshot.

### 2. ONVIF capability probe — `internal/onvif` additions

New `MediaClient` beside the existing `DeviceClient`, same WS-Security
UsernameToken transport:

- `GetCapabilities` (device svc) → media/events service XAddrs.
- `GetProfiles` (media svc) → profile tokens, video codec/resolution,
  audio presence.
- `GetStreamUri(profile)` → RTSP URL (the vendor-blessed one, replacing
  operator guesswork).
- `GetSnapshotUri(profile)` → JPEG URL (recorded now, consumed by SP4).

`ProbeCapabilities(xaddr, user, pass) (*CapabilityReport, error)`
composes these: device info + profiles + chosen profile (highest
resolution H.264/H.265 profile with audio preferred) + stream/snapshot
URIs + booleans (`has_audio`, `has_ptz`, `has_motion` from events
capability, `has_io`, `has_imaging`). The report persists to
`camera_capabilities` through the existing
`store.CameraCapabilitiesRepo` (shipped in foundation Phase 2, unused
until now).

### 3. Adopt flow — API

`POST /v1/discovery/adopt` (perm `camera.create`):

```json
{"endpoint_reference": "...", "name": "front_door",
 "rtsp_username": "admin", "rtsp_password": "..."}
```

1. Look up the discovered entry (404 if the ref aged out).
2. `ProbeCapabilities` with the supplied credentials (401 → 400 with a
   "camera rejected credentials" message; unreachable → 502).
3. Through the existing conf-path create handler internals, create the
   camera with `source_url` = probed `GetStreamUri` result (no userinfo)
   and `onvif_xaddr` = discovered XAddr; store row via `cameras.Service`
   (foundation fix F1 machinery), credentials into the vault, capability
   report into `camera_capabilities`. One failure unwinds the lot
   (vault/capability failure deletes the just-created camera row + path).
4. Path registers via the normal flow; recording starts per policy.

Also: `POST /v1/cameras/:id/probe` (currently 501) re-runs
`ProbeCapabilities` using vault credentials + stored xaddr and refreshes
the `camera_capabilities` row. `GET /v1/discovery/cameras` lists the
cache (perm `camera.list`); `POST /v1/discovery/probe` forces a round
(perm `camera.create`) and returns the refreshed list.

### 4. Health collector — `internal/camerahealth`

A poll-based collector (5s tick) owned by core wiring:

- **Inputs**: path manager snapshot (`APIPathsList`: ready state, source
  connection, last-frame time per camera path) + `cameras.Service` list.
- **State machine** per camera (spec'd states from foundation):
  - `connected` — path ready, frames flowing.
  - `reconnecting` — path exists, source not ready, failures < 5.
  - `failed` — ≥5 consecutive poll rounds not ready.
  - `idle` — camera disabled or no path registered.
- **Writes**: upsert `camera_health` (existing repo) only on change or
  every 60s (heartbeat `updated_at`), with `last_keyframe_at` from the
  path's last-frame time and `consecutive_failures`.
- **Events**: on `connected → reconnecting/failed` transition, after a
  30s debounce (skip camera restarts from credential rotation), insert
  `camera.offline` (severity `warning`) via `events.Service`; on
  recovery insert `camera.online` (severity `info`). These are the
  recorder's first production event producers — they flow through
  subscriptions → notifications like any event.
- SP3 seam: `Touch(cameraID, lastEventAt)` lets vendor channels stamp
  `last_event_at` without owning the row.

### 5. SPA

- **Cameras page**: "Discovered on your network" section (model, vendor,
  IP, Adopt button → modal: name + credentials). Adopted/matched entries
  show as "already managed". Health badge per camera card
  (green/amber/red/grey from `rtsp_state`), capability chips (Audio,
  PTZ, Motion) from a new `GET /v1/cameras/:id/capabilities`.
- **Camera drawer**: health tab gets real data (state, last keyframe,
  consecutive failures, last error).

## API summary

| Method | Path | Perm | Notes |
|---|---|---|---|
| GET | `/v1/discovery/cameras` | `camera.list` | cache snapshot |
| POST | `/v1/discovery/probe` | `camera.create` | force probe round |
| POST | `/v1/discovery/adopt` | `camera.create` | probe + create + vault + capabilities |
| POST | `/v1/cameras/:id/probe` | `camera.update` | re-probe capabilities (replaces 501) |
| GET | `/v1/cameras/:id/capabilities` | `camera.read` | stored report |
| GET | `/v1/cameras/:id/health` | `camera.read` | now returns live data |

## Error handling

- Discovery probe failures log-and-continue (multicast may be filtered);
  the cache serves stale entries with honest `last_seen_at`.
- Adopt is the only multi-write flow: ordered store-row → vault →
  capabilities with compensating deletes on failure; the conf path
  applies last, so a failed adopt leaves no path behind.
- Health collector never blocks the path manager: list calls carry a 2s
  timeout; a timed-out poll counts as "no data", not a failure round.

## Testing

- Unit: discovery cache (dedup, aging, matching), capability-report
  parsing against captured Amcrest SOAP fixtures, health state machine
  (table-driven transitions + debounce), adopt handler with the
  in-process store (harness from `api_v1_camera_extensions_test.go`).
- Live acceptance (LAN): discovery lists all 3 cameras with correct
  models; adopt one Amcrest end-to-end (probe picks the vendor RTSP URL,
  stream lives, capabilities row populated); unplug/deny scenario →
  `camera.offline` event → webhook fires; recovery → `camera.online`.
- The runbook's `rtsp_state: "connected"` criterion (parked as finding
  F4) reactivates.
