---
title: Phase A — client-v2 API contract
date: 2026-10-15
scope: Server features the web-app redesign codes against (→ v1.4.0)
depends-on: 2026-10-15-client-v2-server-gap-analysis.md
branch: consumer-foundation
---

# Phase A — client API contract

Twelve items from the gap analysis, implemented as eight batches. One
simplification discovered during design: the in-tree playback server
already parses true segment durations and merges adjacent segments
(`internal/playback/on_list.go parseAndConcatenate`), so coverage wraps
that logic and no segment-end schema work is needed.

Posture note (applies to B4): media ports (RTSP 8554 / HLS 8888 /
WebRTC 8889) ship LAN-open via `authInternalUsers: any` while the /v1
API is JWT-authed. Playback (9996) joins that same posture — a LAN
appliance decision, revisited when remote access arrives.

## B1 — stream auth + token refresh
- `/v1/events/stream` accepts `?access_token=<jwt>` (EventSource can't
  send headers; today it silently 401s → polling fallback). Query token
  accepted ONLY on this route.
- `POST /v1/auth/refresh`: valid current token → fresh token, same
  claims, sliding expiry. SPA: auto-refresh timer + EventSource token.

## B2 — five roles
Roles `owner`, `operator`, `audit` join `admin`/`viewer` (migration
seeds + rbac mappings): owner = admin ∪ {}; operator = viewer ∪
{event.ack, clip.create, clip.export, camera.ptz}; audit = {audit.read,
*.list/read subset}. Users API accepts them.

## B3 — events UX
- `POST /v1/events/acknowledge-all` {camera_id?, before?} → count.
- Bookmarks: seeded `bookmark` event type; `POST
  /v1/cameras/:id/bookmarks` {time?, note} → event via events.Service
  (timeline/retention/export inherit).

## B4 — playback + coverage
- Enable playback server (consumer config `playback: yes`, port 9996;
  compose EXPOSE). Client seeks via
  `http://<nas>:9996/get?path=<cam>&start=<RFC3339>&duration=<s>`.
- `GET /v1/cameras/:id/coverage?from&to` → `{items:[{start,duration}]}`
  wrapping playback's parse+concatenate over recordstore segments.

## B5 — PTZ + camera reboot
- `internal/onvif/ptz.go`: ContinuousMove, Stop, GotoHomePosition,
  GetStatus, GetPresets/GotoPreset/SetPreset (WS-Security transport as
  MediaClient). Device: SystemReboot.
- API under `/v1/cameras/:id/ptz/*` + `/v1/cameras/:id/reboot`; new
  perms `camera.ptz`, `camera.reboot` wired into roles. PTZ XAddr from
  capability probe (vendor JSON gains ptz_xaddr).

## B6 — stats + user prefs
- `GET /v1/cameras/:id/stats`: path-manager counters snapshot
  {online, tracks, bytes_received, readers, ts} — client derives rates
  from deltas.
- `user_prefs` table (user_id,key,value) + `GET/PUT /v1/me/prefs`
  (opaque JSON values; tours/grid/dwell live here).

## B7 — substreams
Capability probe also resolves the smallest profile's stream URI
(`sub_stream_uri` in vendor JSON); PathBridge registers `<name>_sub`
(record: false) when present. Grid pulls `_sub`, solo view pulls main.

## B8 — ship
SPA minimal wiring (EventSource token, refresh timer), bundle rebuild,
full sweep, scratch-instance live verify, docs, tag v1.4.0 → CI image.

Each batch: failing tests first, suite green, commit. Live acceptance
against a local scratch recorder (NAS updates via ./update.sh after
the tag).
