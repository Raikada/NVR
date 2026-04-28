# Recorder Configuration UI

The recorder ships a static React SPA that an installer points a
browser at to configure the recorder, pair it to a management
server, and inspect logs / events / health. The UI is served from
the same HTTP listener as the `/v1` API (default `:9997`).

## Where it lives

```
web/                    # Vite + React + TypeScript source
  src/
    components/         # Shell + shared primitives + icons
    routes/             # One file per route (overview, cameras, ...)
    lib/                # Types, color helpers, mock data
    styles/tokens.css   # Design tokens (colors, fonts, spacing, motion)
  public/assets/        # Logo + brand assets
  package.json
  vite.config.ts
  tsconfig.json

internal/web/
  web.go                # go:embed dist + gin route registration
  dist/                 # Pre-built SPA bundle (committed)
```

The SPA source under `web/` builds to `web/dist/`. The committed
embed copy is at `internal/web/dist/` so the Go embed picks it up
at compile time. `make web` rebuilds and refreshes the embed copy
in one step.

## Working on the SPA

```sh
cd web
npm install               # one-time; Node 20+ required
npm run dev               # Vite dev server on :5173, proxies /v1 to :9997
```

In a separate terminal, run the recorder so the dev SPA has a real
API to talk to:

```sh
go run ./
```

Open `http://localhost:5173`. The dev server hot-reloads on save.

## Refreshing the embed

After landing UI changes under `web/`, refresh the bundle that
ships in the recorder binary:

```sh
make web
```

This runs `npm ci && npm run build` in a Node-20 Docker container
and replaces `internal/web/dist/` with the freshly-built bundle.
Commit the resulting `internal/web/dist/` diff alongside your
`web/` source diff so `go build` stays self-contained — anyone
cloning the recorder repo can build a working binary without
needing Node installed.

The build is reproducible: same source, same `package-lock.json`,
same Node image → byte-identical bundle (Vite hashes by content,
so unchanged code yields unchanged filenames).

## Architecture notes

- **Hash routing.** The SPA uses `window.location.hash` for routes
  (`/#/cameras`, `/#/logs`, etc.). The embed handler returns
  `index.html` for any unknown GET path; the SPA's `hashchange`
  listener handles the rest. No history-API server-side fallback
  needed.
- **Inline styles + CSS custom properties.** The design uses inline
  styles backed by CSS variables from `tokens.css`. Themes flow
  through the variables; component code never re-derives a color
  or spacing value. No styled-components / Tailwind layer.
- **Mock data layer.** Live data on the SPA is currently driven
  by `web/src/lib/mockdata.ts` — initial cameras, rolling events,
  log lines. Replaced piece-by-piece with real `/v1/` calls in a
  separate work stream; the data layer is structured so each route
  can swap its mock source for a real fetch independently.
- **Cache headers.** `/assets/*` (Vite's content-hashed bundle) is
  served `Cache-Control: public, max-age=31536000, immutable` —
  the hash invalidates the URL when the bundle changes. The HTML
  index is `Cache-Control: no-cache` so a `make web` redeploy is
  picked up on the next visit.

## Data-layer status

The SPA's data layer wires to real `/v1/` endpoints where the
recorder exposes the underlying data, and falls back to clearly-
marked stubs (`'—'` / "NOT EXPOSED" labels in the UI, inline
`STUB:` comments in the code) where it doesn't. Wired routes
poll on a sensible cadence and pause polling when the tab is
hidden.

Wired today:

- **Overview** — `/v1/health` (1Hz) drives CPU / Mem knobs +
  CAMERAS tile + uptime + reported-at. `/v1/events` (4s) drives
  the recent-events feed.
- **Cameras** — `/v1/cameras` drives the list. `POST` creates,
  `DELETE` removes. List refetches after every mutation.
- **Logs** — `/v1/events` (1.1s while tailing, 60s while paused).
  Level/source filters map onto canonical Event severity +
  subject_kind. Search hits message / kind / subject_kind.
- **Storage** — `/v1/storage-volumes` (30s). Per-volume rows show
  mount path / kind / priority / used / capacity / status. Total
  used vs free roll up into the breakdown bar and stat tiles.
- **Network** — `/v1/health.network` (5s) drives reachability
  block. `/v1/recorder/config` (one-shot) drives port enable/
  disable badges (api / rtsp / rtmp / hls / webrtc / srt).
- **Settings** — `/v1/recorder/config` (one-shot) keeps the data
  layer warm; per-field PATCH wiring is queued.

## Stub registry — fields the recorder doesn't expose yet

Every stub below renders as a placeholder in the UI today and is
labelled in source so future agents can find them with
`grep -n 'STUB' web/src/`.

Recently retired (now wired to real `/v1/`):
- Overview BANDWIDTH knob → `/v1/health.bandwidth` (rx_bps + tx_bps)
- Network bandwidth row NOW + PEAK/24H + AVG/24H →
  `/v1/health.bandwidth` (now) + `/v1/recorder/network-info.bandwidth`
  (24h rolling-window peak + avg)
- Network interface block (NAME / LINK / IPV4 / MAC / MTU / DNS) →
  `/v1/recorder/network-info` (gopsutil-driven, cross-platform)
- Storage WRITE RATE stat + per-volume write-rate → `/v1/storage-volumes.write_bytes_per_second`
- Storage `≈ N days at current rate` → derived from the above
- Storage SMART hours / vendor / model / temperature →
  `/v1/storage-volumes.smart` (best-effort smartctl shell-out,
  cross-platform mount resolution)
- Settings identity (hostname / timezone read-only, location editable) →
  `/v1/recorder/identity`
- Settings firmware version display → `/v1/recorder/identity.firmware_version`
- Settings Reboot button → `POST /v1/recorder/reboot`
- Settings Backup Config button → `GET /v1/recorder/config-backup`
- Settings Restore Config button → `POST /v1/recorder/config-restore`
  (file picker → JSON body; recorder validates before applying)
- Logs DEBUG severity filter → dropped (canonical events have no DEBUG tier)
- Logs NETWORK source filter → dropped (no canonical subject kind models it)
- Cameras list resolution / fps / codec columns → `/v1/streams` join
  (active video track per camera_id)
- Manual-add wizard "Probe Device" / "Test Stream" buttons →
  `POST /v1/cameras/probe` (TCP-handshake reachability + latency)
- Diagnostics route → `POST /v1/diagnostics/{ping,ntp,rtsp-probe}`
  (stdlib-based, cross-platform; no shell-outs)
- Cross-platform host metrics: cpu / mem / bandwidth samplers
  rebuilt on gopsutil. Drops 11 per-OS build-tagged files;
  Linux + macOS + Windows + BSD all work from one module.
- Cameras live preview tiles (drawer Stream tab + manual-add wizard
  inline preview + manual-add wizard stream-test preview) → real
  HLS via hls.js (Chrome / Firefox / Edge) or native `<video>`
  (Safari / iOS), reading from the recorder's HLS server on :8888.
  URL is constructed from `Camera.name` directly (per ADR 0009 the
  canonical name is the recorder's path-name). All three tiles are
  singular (one preview visible at a time), so autoplay is fine —
  the `manualStart` click-to-play mode on `HLSPreview` is reserved
  for a future camera-grid view that renders many tiles at once.
  Gracefully falls back to the original gradient placeholder for
  offline cameras, simulated wizard cameras whose path doesn't
  exist on the server, and any HLS player error. The CameraList
  row's 96×56 thumbnail icon is NOT a video tile — it stays a
  status icon.

**Overview.**
- `TEMP` mini-metric — no thermal sensor surface. Cross-platform
  thermal reading varies wildly by OS + hardware; deferred.
- `STORAGE` stat tile — used/total still uses a mock 256 GB / 2 TB
  pair. The values exist live on the Storage route; plumbing the
  rolled-up totals into the Overview tile is a small follow-up.
- `EVENTS · 24H` stat — needs an event-count-by-window query the
  recorder doesn't surface yet.

**Cameras.**
- Drawer Stream tab "STREAM HEALTH" panel (UPTIME, LATENCY,
  PACKET LOSS, JITTER, GOP, BITRATE) — values are hardcoded
  beside the now-real live preview. These are RTSP-source-level
  stream metrics that the recorder doesn't currently surface
  through `/v1/recorder/hls-muxers` (HLS muxer state has bytes-
  served but not source jitter / packet-loss). Wiring would need
  a recorder-side stat surface, separate swing.
- ONVIF Discover panel — the WS-Discovery probe and identify
  phases are entirely simulated. Real ONVIF discovery needs a
  recorder-side subsystem (probably `internal/onvif/`) with
  WS-Discovery + GetDeviceInformation handlers. Bounded but big.
- Config drawer — the drawer's Recording / Motion / ONVIF Events /
  Advanced tabs collect state that has no 1:1 PATCH endpoint:
  - Recording mode, retention, pre/post-buffer → maps to
    `RecordingPolicy`, a separate resource. Drawer split or
    multi-resource save (small).
  - Motion zones, sensitivity → recorder doesn't model motion
    detection canonically yet.
  - ONVIF events → recorder doesn't subscribe to ONVIF events.
  - Advanced (NTP source, OSD, audio track) → recorder-internal,
    no canonical surface.
  Save button currently toasts "saved locally" and closes the
  drawer; field changes land in component state only.

**Storage.**
- Per-content-type breakdown (Continuous / Motion events / AI
  detections) — recorder doesn't account by content type.
  Used-vs-free is the live data we have. Real model extension
  (`RecordingSegment.content_type`) is a recorder-side change.
- "RETENTION" stat tile — surfaces via `RecordingPolicy.
  RetentionDuration` once a small UI fetch wires up.

**Network.**
- NTP / VLAN mini-blocks — neither is uniformly available across
  OSes. NTP daemon configuration lives outside the recorder;
  VLAN tagging is interface-specific and rarely exposed at the
  Go-stdlib level.
- Gateway field — falls back to bootstrap config. Cross-platform
  routing-table query isn't standardized.
- MS tunnel port row — depends on the pairing client landing.

**Settings.**
- Firmware update flow ("Check for Updates" button) — no update
  endpoint surfaced. Architectural decision pending: binary
  signing, rollback semantics, update channel.
- Auto-update / Telemetry toggles — stored locally only;
  recorder doesn't have the corresponding flags.
- Factory Reset — no recorder endpoint; needs scope decision
  (what state survives?). Button toasts.

**Pairing (whole route).**
- LAN auto-discovery, pair-by-bearer-token, mTLS-cert badges,
  heartbeat / latency / tunnel mini-blocks — every field on
  this route depends on the Management Server tier existing. MS
  is scaffold-only today. Whole route stays mock until ADR 0008
  (FRP broker) lands and the recorder ↔ MS pairing client is
  built.

**SetupWizard step 2 (pair with MS).**
- Same MS dependency as the Pairing route. The wizard's other
  steps (Welcome / Network preflight / Add cameras / Finish)
  are bounded — Network preflight could wire to
  `/v1/recorder/network-info` + `/v1/diagnostics/ping` for real
  checks, Add cameras to `/v1/cameras/probe` — small follow-ups.

## Mock data source

`web/src/lib/mockdata.ts` carries the remaining mock generators —
`INITIAL_CAMERAS` (cameras seed before the live list arrives),
`seedEvents` / `nextEvent` (still used by the wizard's preview),
and `seedLogs` / `nextLog` / `MockLog` (now unused; deletable in a
follow-up cleanup since Logs is wired). Each route file imports
only what it still needs from this module so future cleanups can
drop blocks as more wires up.
