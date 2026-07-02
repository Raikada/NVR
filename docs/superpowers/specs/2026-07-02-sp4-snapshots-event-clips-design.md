---
title: SP4 — Snapshots on events + event-to-clip linkage
date: 2026-07-02
scope: Sub-project 4 of 4 (consumer NVR)
depends-on: 2026-07-02-sp3-vendor-event-channels-design.md
branch: consumer-foundation
---

# SP4 — Snapshots + event-to-clip

SP3 events are text rows. SP4 makes them evidence: a JPEG captured at
the moment of the event, a thumbnail for lists, and a lazily-extracted
clip with pre/post-roll. Delivery URLs are short-TTL signed so `<img>`
tags and webhook consumers work without Authorization headers.

## Out of scope

- Public/long-lived sharing links (signed URLs are minutes-scale, for
  the SPA and notification consumers on the LAN).
- Motion-gated recording, review timeline UI (post-SP4 roadmap).
- Video analytics of any kind.

## Components

### 1. `internal/snapshots` — on-event JPEG capture

Subscribes to `events.Service.Subscribe()` (same bus the notification
dispatcher uses). For each event whose type has `capture_snapshot=1`
(new column, migration 0025, default 1; 0 for `camera_online`,
`camera_offline`): fetch → store → record, bounded by a per-camera
single-flight (a motion burst yields one snapshot per event but fetches
don't pile up: while one fetch runs, later events in the same burst
reuse its JPEG when they arrive within 2s; else fetch fresh).

Fetch ladder (first success wins):
1. **Amcrest CGI**: `http://<host>/cgi-bin/snapshot.cgi?channel=1`
   (digest auth; reuses amcrestchannel's transport) — when the camera's
   channel resolved to amcrest.
2. **ONVIF snapshot URI** from the SP2 capability probe (vendor JSON),
   digest/basic auth.
3. **Live frame grab** from the recorder's own stream via the existing
   `/v1/cameras/:id/snapshot` internals (`internal/api/snapshot_jpeg.go`)
   — works whenever the camera streams, needs no camera round-trip.

Storage: `<snapshot_root>/<camera_name>/<yyyy-mm-dd>/<event_id>-full.jpg`
and `-thumb.jpg` (320px-wide nearest-neighbor downscale, JPEG q70, pure
stdlib). `snapshot_root` comes from the bootstrap-seeded system_settings
row. Two `event_snapshots` rows per capture (`kind` full|thumb) with
width/height/size.

### 2. Signed media URLs — `internal/mediasign` + API

`mediasign.Signer` (HMAC-SHA256, key = 32 random bytes persisted at
`<identityDir>/media-sign.key`, mode 0600, generated on first use):
`Sign(path string, ttl) → exp, sig` / `Verify(path, exp, sig) → bool`.

Endpoints (anonymous-with-signature — added to the pre-auth bypass list
with mandatory verify):
- `GET /v1/media/snapshots/:event_id/:kind?exp&sig` → JPEG
- `GET /v1/media/clips/:clip_id?exp&sig` → MP4 download

Event wire objects (`GET /v1/events`, SSE, webhook payload) gain
`snapshot_url` / `thumbnail_url` (signed, 15 min TTL) when snapshots
exist. The webhook dispatcher already has the payload fields — they go
live.

### 3. Event-to-clip — lazy extraction

`POST /v1/events/:id/clip` (perm `clip.create`):
1. Resolve the event's camera + policy pre/post-roll (policy fields if
   present; else 5s/5s defaults).
2. Create a `clips` row spanning `[occurred_at−pre, occurred_at+post]`
   + `clip_segments` linkage, then extract the MP4 through the existing
   clip pipeline (`internal/api/clip_pipeline.go` remux machinery — no
   re-encode).
3. Idempotent: a second POST for the same event returns the existing
   clip. Response: clip row + signed `download_url`.
4. 409 when the recording segments for the window no longer exist
   (already swept).

`GET /v1/events/:id` includes `clip_id` when one exists.

### 4. Retention

The event retention sweeper currently deletes expired event rows (FK
cascades take the `event_snapshots` rows) — SP4 adds the file pass:
collect snapshot paths before the delete, unlink after. Clip files
follow the existing clips sweeper. A startup orphan sweep
reconciles files on disk against `event_snapshots` (crash between
unlink batches).

### 5. SPA

- **Events page** (new route `/events`): reverse-chron list with
  thumbnails, camera + type + severity filters, acknowledge button,
  "Export clip" per event (POST clip → download link), SSE live prepend.
- **Camera drawer**: "Recent events" strip (last 5 with thumbnails) +
  the SP3 events-channel selector (batched into this SPA pass).

## Error handling

- Fetch ladder failures degrade: no snapshot ≠ no event. The failure is
  logged and the event simply has no snapshot rows.
- Snapshot fetches carry 5s timeouts; the subscriber must never lag the
  events bus (bounded queue, drop-oldest with a warn log).
- Signed URL verification failures are 403 with no detail; expired is
  403 `{"error":"expired"}` so the SPA can re-fetch fresh URLs.
- Clip extraction failure deletes the placeholder clip row (no zombie
  rows) and returns 502 with the remux error.

## Testing

- `snapshots`: fetch-ladder selection with fake HTTP servers (amcrest
  digest → onvif URI → frame-grab fallback), burst single-flight,
  storage layout + thumbnail dimensions, capture_snapshot=0 skip.
- `mediasign`: round-trip, tamper, expiry.
- API: signed snapshot/clip fetch (200/403/expired), event clip create
  (idempotency, 409 after sweep), event wire URLs present.
- Retention: expired event → files gone; orphan sweep.
- Live acceptance (LAN): walk-test → event with real JPEG snapshot +
  thumbnail on disk; webhook payload carries working signed URLs; export
  clip from the event → MP4 plays with pre/post-roll; AD410 doorbell
  press → snapshot of the porch.
