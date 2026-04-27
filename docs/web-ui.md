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

## Data-layer follow-up

The SPA's live-data screens (Overview vitals, recent events, logs,
diagnostics) currently animate against mock generators in
`web/src/lib/mockdata.ts`. The handoff plan was: faithful pixel-
perfect port first, real `/v1/` wiring as a second swing.

Routes ranked by ease of real-API wiring:

| Route        | Real source                                | Effort |
|--------------|--------------------------------------------|--------|
| Overview     | `/v1/health`, `/v1/cameras`, `/v1/events`  | Small  |
| Cameras      | `/v1/cameras`, `/v1/streams`               | Small  |
| Logs         | `/v1/audit` + `/v1/events`                 | Small  |
| Storage      | `/v1/storage-volumes`                      | Small  |
| Network      | `/v1/health.network` + recorder-config     | Small  |
| Settings     | `/v1/recorder/config`                      | Small  |
| Pairing      | (needs MS to exist)                        | Big    |
| Diagnostics  | (needs runner subsystem)                   | Big    |

The first six are independent local engineering against the
existing API. The last two depend on subsystems that don't exist
yet (Management Server, diagnostic runner) and stay mock-only
until those land.
