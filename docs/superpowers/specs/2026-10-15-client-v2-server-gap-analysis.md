---
title: Client-v2 implied API surface — server gap analysis
date: 2026-10-15
scope: Server roadmap driving the web-app redesign
status: analysis (feeds Phase A/B spec)
---

# Client-v2 server gap analysis

The UI redesign assumes a capability surface (grouped below by its
screens). Verdicts against the shipped server:

✅ have · 🟡 partial · 🔨 build (clear path) · ⚠️ negotiate (hard or
architecture-bending) · ❌ push back for v1

## Streaming & live view

| UI assumption | Verdict | Notes |
|---|---|---|
| Live per camera (WebRTC/HLS/RTSP) | ✅/🔨 | All three served today; browsers need **signed stream URLs** (video tags/EventSource can't send JWT headers — same fix as media URLs). |
| Status online/degraded/offline + retry state | ✅ | `camera_health.rtsp_state` (`connected/reconnecting/failed/idle`) + `consecutive_failures`. |
| Per-stream res/fps/bitrate/codec | 🟡 | Codec/resolution known (capabilities + path tracks); fps/bitrate need a small **stats endpoint** over path-manager counters. |
| Realtime motion flag | 🟡 | Derivable (last motion event age / motion controller state); expose as a field on health. |
| Snapshot JPEG | ✅ | `/v1/cameras/:id/snapshot` (live + `?at=` historical). |
| Two-way audio | ⚠️ | ONVIF backchannel/vendor-specific; defer (UI already flags optional). |
| Camera groups | ✅ | CRUD exists. |
| Tours (ordered list + dwell) | 🔨 | Pure preference data; add a tiny server-side UI-prefs store (tours, default grid, dwell) — client-defined schema. |

## PTZ

| | | |
|---|---|---|
| Relative move / zoom / home / absolute readback | 🔨 | ONVIF PTZ service on the existing SOAP client (ContinuousMove/RelativeMove/GotoHomePosition/GetStatus). |
| Named presets (recall + save) | 🔨 | ONVIF SetPreset/GotoPreset — camera-native, no DB needed. |
| Capability flags REC/AI/PTZ | ✅ | `camera_capabilities` (`has_ptz`, `has_motion`…) + policy state. |

## Recording & playback

| | | |
|---|---|---|
| Recording modes: continuous/off | ✅ | Policy `record` + schedules. |
| Motion/event recording modes | 🔨/⚠️ | Recommend **retention-tiering** first (record continuously, keep event-overlapping segments on a longer horizon — `content_type` classifier + events table already exist). True gated capture with pre-roll ring buffers is a much bigger lift for little extra disk saving. |
| Pre/post-roll | 🟡 | Exists for clip export; applies to event-retention tiering naturally; capture-time pre-roll only matters for gated mode (see above). |
| Seek to timestamp / variable speed / jump live | 🔨 | **The playback server already exists in-tree** (`internal/playback`, `/get?start&duration` streaming fmp4) but is disabled, un-integrated with our auth, on port 9996. Integrate behind signed URLs. Speed is client-side on fmp4. |
| Segment timeline with gaps | 🔨 | **Coverage endpoint** needed: segments currently expose start times only (no ends) — add duration/end tracking + a per-camera `/coverage?from&to` range-list API. |
| Multi-camera synchronized playback | 🟡 | Server serves per-camera ranges; sync is client clock logic. No server work beyond coverage+playback. |
| Bookmarks / markers | 🔨 | Trivial: custom event type (`bookmark`) through the existing events pipeline — timeline, retention, export all inherit. |
| Clip export MP4 + progress | ✅ | States requested→preparing→ready + signed download. |
| MKV/GIF export | ❌ | MP4 only in v1 (MKV adds container surface for no consumer gain; GIF = transcode). |
| Burned-in overlay | ❌ | Requires video transcode; the server never re-encodes by design (NAS CPUs). Overlay belongs in the client render / future. |

## AI / events

| | | |
|---|---|---|
| Person/Vehicle/Package/Motion/Animal classes | ✅ | Seeded types, fed by camera-side AI via vendor channels (Amcrest SmartMotion live-verified). |
| Face / license plate | ❌ | Not in camera-side events; needs the detector-sidecar architecture (future project). |
| Confidence + bounding boxes | ⚠️ | **Negotiate.** Vendor events rarely carry them; `events.region_json` column exists and we pass vendor payloads through — treat as *optional* fields the UI renders when present. Full fidelity = detector sidecar. |
| Event duration | 🔨 | We emit on Start and drop Stop; pair them to stamp `duration`. Moderate. |
| Query: class/time/camera/sort | ✅ | List filters + cursor pagination. |
| Min-confidence filter | ⚠️ | Only meaningful once confidence exists (above). |
| Free-text search | ❌ | v1 push-back; structured filters cover the real use. |
| Per-camera motion sensitivity | 🟡 | Exists for the recorder's own pixel-motion detector (`motion-config`); camera-side sensitivity = vendor settings passthrough (Tier C). |
| Event thumbnails/previews | ✅ | SP4 snapshots + signed URLs; clip preview via event-clip. |

## Alerts & notifications

| | | |
|---|---|---|
| Realtime push stream | ✅/🔨 | SSE exists; **needs the signed-URL auth fix** (EventSource can't send headers — currently falls back to polling). |
| Offline/online, storage threshold alerts | ✅ | Health events + storage volume events ship today. |
| Degraded-link (packet loss) | 🟡 | Health covers degraded; per-stream loss counters → stats endpoint. |
| Read/unread, dismiss, mark-all | ✅/🔨 | Ack exists; add batch mark-all (trivial). |
| Channels: webhook/email | ✅ | Shipped (HMAC webhooks, SMTP). |
| Mobile push | 🔨 | Web Push target kind (VAPID) on the existing dispatcher/outbox. |
| Quiet hours + critical override, digests | 🔨 | Dispatcher scheduling layer; moderate, self-contained. |

## Devices

| | | |
|---|---|---|
| Discovery (IP/model/codec/res) | ✅ | WS-Discovery cache + capability probe. MAC 🟡 (ARP enrich, easy). |
| Add via RTSP/ONVIF + test connection | ✅ | Adopt flow + `/diagnostics/rtsp-probe` + capability report. |
| Model/IP/firmware/last-seen | ✅ | Store row + health. Uptime 🟡 (ONVIF GetSystemDateAndTime delta). |
| Reboot camera | 🔨 | ONVIF SystemReboot — one SOAP call. |
| Remove camera | ✅ | Cascade delete. |

## Storage & retention

| | | |
|---|---|---|
| Pools, capacity/used | ✅ | Volumes + breakdown endpoints. RAID internals ❌ — the NAS OS owns RAID; we report volume-level truth only. |
| Retention rules (scope/days/enable) | ✅ | Policies + groups + schedules. |
| Archive tier / legal hold | ❌ | Flagged optional by design; defer. |
| Usage breakdown | ✅ | `storage-volumes/breakdown` (per content-type rollup). |
| Forecast (write rate, days left, next GC) | 🔨 | `volume_write_rate` sampler already exists; add oldest-segment + estimation endpoint. |
| GC/oldest-first deletion | ✅ | Retention sweepers + manual sweep endpoint. |

## System health

| | | |
|---|---|---|
| CPU/RAM + history, uptime | ✅/🟡 | Host sampler exists; add a small history ring. |
| GPU utilization | ❌ | No GPU workload exists (no transcode/detect); drop from v1. |
| Network throughput/interfaces/connectivity | ✅ | `recorder/network-info` + diagnostics ping/ntp. |
| Thermals, fan RPM, drive SMART | ⚠️ | **Negotiate.** Inside Docker these aren't visible without privileged device access, and the NAS OS (DSM) already owns them. v1: link out to DSM; revisit for bare-metal installs. |
| Service list w/ status | 🟡 | Single binary — expose subsystem states (path manager, event channels, dispatcher, sweepers) honestly instead of fake "services". |
| Log stream | ✅ | Logs surface exists. |
| Diagnostics bundle export | ✅ | ADR 0015 recovery-bundle export shipped. |
| Reboot | ✅ | `recorder/reboot` (container restart under Docker). |

## Users & access

| | | |
|---|---|---|
| Roles Owner/Admin/Operator/Viewer/Audit | 🔨 | RBAC permissions are already granular (40+); today only two role→perm mappings exist. Add three mappings + migration — small. |
| Permission scopes | ✅ | The perm vocabulary already covers cameras/recording/export/PTZ(new)/rules/users/system/audit. |
| Invite users | 🟡 | Create-user exists; email-invite flow defer (needs SMTP assumptions). |
| MFA | ❌→🔨 | v2: TOTP is self-contained when wanted. |
| Active sessions + revoke | 🟡/🔨 | Session tracker exists; JWTs are stateless so revoke needs short-TTL + refresh (same work as the token-refresh item) or a denylist. |
| API keys (scoped) | 🔨 | New table + auth path; moderate; valuable for integrations. |
| Audit log + CSV export | ✅ | Both shipped (`/v1/audit`, `/v1/audit/export`). |

## Onboarding / system

| | | |
|---|---|---|
| Claiming via pairing code/QR | ❌ | Cloud-era flow; LAN product claims via mDNS + browser + bootstrap password. Revisit with cloud relay. |
| Network self-checks | ✅ | Diagnostics (ping/NTP/RTSP-probe) + network-info; mDNS state known. |
| Settings (name/tz/clock/grid/dwell) | ✅/🔨 | system_settings covers device-level; grid/tour/dwell → the UI-prefs store (see Tours). |
| Cloud relay tunnel | ❌ | The `cloudbridge` seam exists but is nop — this is its own future project. v1 remote access = VPN. |
| UPnP toggle | ❌ | Declined on security grounds (auto-exposing an NVR is how botnets eat cameras); document port-forward/VPN instead. |
| mDNS, HTTPS/RTSP port config | ✅ | Shipped. |
| Firmware update channel/check/auto-install | 🟡 | Two worlds: Docker deployments update via image pull (`update.sh`); the ADR-0014 signed-manifest applier exists for bare-metal. v1: an **update-available check** endpoint (compare running VERSION vs registry) + surfacing in UI; auto-install during quiet hours later. |
| Config backup/restore, factory reset | ✅ | ADR 0015: config reset, factory wipe, recovery bundle. |

## Proposed server phases

**Phase A — client blockers (the UI can't ship without):**
signed stream/SSE URLs · playback-server integration · coverage
endpoint (segment ends + gaps) · sub-stream paths for grids · token
refresh (+ session revoke basis) · per-camera stats (fps/bitrate/loss)
· role expansion (5 roles) · bookmarks · mark-all-read · PTZ core +
presets · camera reboot · UI-prefs store (tours/grid/dwell).

**Phase B — parity features:**
retention tiering (event vs continuous horizons) · Web Push target ·
quiet hours + digest · storage forecast · event duration · API keys ·
update-available check · MAC/uptime enrichment · health-history ring ·
subsystem status endpoint.

**Phase C — negotiated/future:**
detector sidecar (true confidence/bboxes, face/plate) · two-way audio ·
vendor settings passthrough (camera-side sensitivity) · cloud relay +
claiming · MFA · email invites · SMART/thermals (bare-metal only) ·
archive tier/legal hold.

## Push-backs the design should absorb now

1. **Confidence + bounding boxes are best-effort** until a detector
   sidecar exists — render when present, never require.
2. **No burned-in overlays / GIF / MKV** — the server never transcodes;
   overlays are a client-render concern.
3. **GPU/SMART/fan panels** — not visible from a container; link to the
   NAS OS. Design the health screen so those tiles are optional.
4. **UPnP** — declined; remote access arrives via cloud relay (later)
   or VPN (now).
5. **QR claiming** — replaced by LAN discovery + bootstrap password
   until the cloud exists.
