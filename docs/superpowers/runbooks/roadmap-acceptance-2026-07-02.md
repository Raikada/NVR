# SP2–SP4 Live Acceptance — 2026-07-02

Run against the LAN fleet (Amcrest IP5M-T1277EW-AI ×2, AD410 doorbell)
from a scratch instance at the roadmap head. Each sub-project's unit
suites were green before this; this is the real-hardware pass.

## SP2 — camera lifecycle: PASS

- WS-Discovery listed all three cameras (plus a non-camera WSD
  responder, correctly unmatched). Vendor/model surfaced after the
  scope-dialect fix (`88355b3d`) — Amcrest puts vendor in `name/` and
  model in `hardware/`.
- **Adopt** (192.168.1.110): one call probed ONVIF with the supplied
  credentials, selected `MediaProfile00000` (2960×1668 H264 + audio),
  stored the vendor's blessed RTSP URL
  (`...realmonitor?...unicast=true&proto=Onvif`), wrote vault
  credentials + `camera_capabilities` (audio ✓ motion ✓ PTZ ✗), and the
  stream was recording within seconds.
- **Health**: `rtsp_state: "connected"` with advancing
  `last_keyframe_at` (foundation finding F4 closed for real).
- **AD410 caveat**: the doorbell's ONVIF service reports a persistent
  account lockout ("Unlock Time is 0 Second(s)") — probably tripped by
  the foundation-era 401 retry flood and apparently held until a device
  reboot. Its RTSP and health are unaffected; adopted via the manual
  path with `event_channel=amcrest` override. Re-try ONVIF adopt after
  power-cycling the doorbell.

## SP3 — vendor event channels: PASS

- The Amcrest CGI attach channel produced a real `motion` event
  (`source: amcrest`) from natural scene motion within ~2 minutes of
  adoption; `camera_health.last_event_at` stamped.
- Subscription-driven webhook dispatch delivered it to a local sink
  with a verifying HMAC signature — the event → subscription →
  notification path is exercised end-to-end for the first time.
- Doorbell press + person/vehicle classification still need a human in
  frame; the mapping paths are unit-tested against captured payloads.

## SP4 — snapshots + clips: PASS (with two live fixes)

- The motion event produced
  `snapshots/front_amcrest/2026-07-02/<event>-full.jpg` (1.9 MB
  full-res) + `-thumb.jpg`, rows in `event_snapshots`, and signed
  `snapshot_url`/`thumbnail_url` on the event wire object that fetch
  with **no Authorization header** (HMAC verified, 403 on tamper).
- `POST /v1/events/:id/clip` produced a downloadable MP4 through the
  existing remux pipeline; idempotent second call returns the same clip.
- Live fixes found by this pass:
  - `volumeRootForPathClip` fell through to `/` for relative record
    paths (seeded policy uses `./recordings/...`) — clip export died
    with `mkdir /clips` (fixed + regression test).
  - Webhook payloads carried the Phase-6 placeholder snapshot URL
    (`/v1/events/:id/snapshot/:kind` — a route that never existed); now
    minted as 24h-TTL signed media URLs.

## Known limitations (deferred, documented)

- **Clip granularity**: the remux pipeline stitches whole overlapping
  segments; a 10s pre/post-roll window inside a long-running segment
  yields the whole segment (observed: 372s). Sample-accurate trimming
  in the remuxer is the follow-up.
- Doorbell ONVIF lockout requires a power cycle (device-side state).
- Pre-existing `internal/core` suite-order test flakiness unchanged.

## Post-acceptance fixes (same session)

- F8: `store.ParseTime` now accepts any RFC3339 variant, not only
  FormatTime's exact `.000` form.
- F9: re-examined — dispatch and test-endpoint timeouts were already
  5s; the original "~1s" observation was a cold local listener taking
  longer than 5s to accept. No code change.
- F10: a not-yet-created recordings directory no longer reports
  `storage volume degraded (statfs_failed)`; only real statfs failures
  degrade.
