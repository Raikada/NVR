# Operator console (client-v2) — shipped v1.5.0, 2026-10-15

The SPA was rebuilt from the Claude Design "Operator Console" handoff
bundle (tactical HUD system). Every screen renders real recorder data —
no simulated numbers.

## Screens → data sources

| Screen | Backing surface |
|---|---|
| Live View | HLS (:8888) per tile, `<name>_sub` sub-streams in grids, PTZ hold-to-move via /v1 ptz, prefs-persisted grid + tours |
| Playback | fmp4 time-range chunks (:9996 /get), /v1 coverage ranges, real event markers, bookmark+clip export (async: poll /v1/clips/:id until ready) |
| Event Search | /v1/events (DB records) + signed snapshot thumbnails |
| Alerts | same events through the acknowledge lens; SSE-live via ?access_token= |
| Devices | /v1/discovery probe + adopt, capabilities, reboot, re-probe |
| Storage & Retention | storage volumes + breakdown × recording policies (tiers), per-camera assignment, manual sweep |
| Users | /v1/users CRUD (5 roles), /v1/audit |
| System Health | /v1/health (CPU/mem/bandwidth), per-camera stats deltas, network info, volumes |
| Settings | system settings, me/prefs, notification targets, recorder identity (pending update), config backup |
| Onboarding | setup-status gate; real system checks + inline discovery/adopt |

## Verification

- `npx tsc -b --noEmit` + vite build green; bundle mirrored to internal/web/dist.
- `cypress/e2e/console-smoke.cy.ts` — 5 specs against a live scratch
  recorder (login form, all 9 routes, bookmark in search+alerts,
  devices flyout, storage tiers / users): all passing.
- Real-camera check (Amcrest IP5M at 192.168.1.110 on a scratch
  instance): RTSP connect + record, live HLS m3u8, stats counters,
  coverage returns the recorded range, playback /get returns valid fmp4
  for a time window, bookmark→clip export → ready → signed download of
  a playable MP4.

## Gotchas

- Clip export is async: POST /v1/events/:id/clip returns state
  "preparing"; the download URL 409s until GET /v1/clips/:id reports
  "ready" (usually <1s). The UI polls via api.waitForClip.
- HLS m3u8 302s to the cookie-check; curl needs -L + a cookie jar,
  hls.js needs withCredentials (already set).
- The pre-console cypress specs (flows/, live-smoke, setup-wizard,
  notifications, cameras UI parts) target the deleted old UI;
  console-smoke.cy.ts is the canonical UI spec now.
