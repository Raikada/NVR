---
title: SP3 — Vendor event channels (ONVIF PullPoint + Amcrest CGI)
date: 2026-07-02
scope: Sub-project 3 of 4 (consumer NVR)
depends-on: 2026-07-02-sp2-camera-lifecycle-design.md
branch: consumer-foundation
---

# SP3 — Vendor event channels

SP2 gave the recorder its first event producers (health transitions).
SP3 wires the cameras' own detection into the events pipeline: ONVIF
PullPoint for any compliant camera, and the Amcrest CGI event stream for
the richer vendor channel the user's fleet (2× IP5M-T1277EW-AI, AD410
doorbell) actually speaks.

User-scoped decision: **ONVIF + Amcrest adapters only.** Hikvision and
Reolink are registry entries returning "not implemented" — the adapter
interface is the contract they'll implement later.

## Out of scope

- Snapshots on events, event→clip linkage (SP4).
- Cross-channel dedup: exactly one channel is active per camera.
- Two-way audio / camera control of any kind.
- Motion-gated recording (post-SP4 roadmap item).

## Components

### 1. `internal/vendorevents` — adapter contract + supervisor

```go
// NormalizedEvent is what every adapter emits: the vendor payload
// reduced to the seeded event_types vocabulary.
type NormalizedEvent struct {
    TypeID     string          // motion|person|vehicle|doorbell|line_cross|tamper|audio_alarm|io_in|package|animal
    Severity   string          // info|warning
    OccurredAt time.Time       // vendor timestamp when provided, else time of receipt
    Payload    json.RawMessage // vendor-native details (code/topic, indexes, raw data)
}

// Adapter is one camera-scoped event channel. Run blocks, delivering
// events to emit until ctx cancels or the channel dies (return error →
// supervisor backoff-restarts).
type Adapter interface {
    Run(ctx context.Context, emit func(NormalizedEvent)) error
}

// AdapterFactory builds an adapter for a camera, or reports the camera
// unsupported (ErrUnsupported → supervisor skips, no retry).
type AdapterFactory func(ctx context.Context, cam ChannelCamera) (Adapter, error)
```

`ChannelCamera` carries what factories need: ID, Name, host (from
source_url), OnvifXAddr, EventsXAddr (from capabilities vendor JSON),
Manufacturer, and a credentials func (wrapping
`cameras.Service.PlaintextCredentials` so plaintext never sits in a
struct field).

**Supervisor** (one per enabled camera with a channel): starts the
selected adapter, restarts on error with exponential backoff (1s → 2s →
… → capped 60s, reset after 10 min of stable run), stops on camera
delete/disable or `event_channel=none`. It owns the funnel:
`NormalizedEvent` → `events.Service.Insert` (source = channel name) +
`camerahealth.Collector.Touch(cameraID, occurredAt)`.

**Manager**: subscribes to the cameras Bus; on Create/Update/Delete it
reconciles the supervisor set (channel selection may change on update —
tear down + restart). At startup it bootstraps from `cameras.List`.

### 2. Channel selection

Per camera, resolved in order:

1. `event_channel` field on the camera (`auto` default | `onvif` |
   `amcrest` | `none`). New nullable TEXT column on `cameras` (migration
   0024), PATCHable via the existing camera update flow, defaulting to
   `auto` when NULL/empty.
2. `auto` resolution: `manufacturer == "Amcrest"` (case-insensitive,
   from the SP2 probe) → amcrest; else `onvif_xaddr` or capabilities
   EventsXAddr present → onvif; else none (idle, logged once).

### 3. ONVIF adapter — `internal/vendorevents/onvifchannel`

Wraps the existing `onvif.Manager` PullPoint machinery
(CreatePullPointSubscription → PullMessages loop → Renew), which
already persists subscriptions and reconnects. The adapter:

- Ensures a subscription exists for the camera (xaddr + creds),
  reusing `onvif.Manager.AddSubscription`; tears it down on stop.
- Consumes the manager's notification callback for its camera, maps
  topics via `onvif.CanonicalEventKindForTopic`, then canonical kind →
  seeded type id:

| canonical kind | type id |
|---|---|
| camera.motion_detected | motion |
| camera.line_crossing | line_cross |
| camera.field_detection | motion |
| camera.tamper_detected / scene_change / signal_loss | tamper |
| camera.audio_detected | audio_alarm |
| camera.digital_input | io_in |
| (classification payloads person/vehicle when present) | person / vehicle |
| unmapped topics | dropped with a debug log (no generic type in the seeded vocabulary) |

- ONVIF property events carry State=true/false; only rising edges
  (false→true) emit, tracked per topic+source token.

### 4. Amcrest adapter — `internal/vendorevents/amcrestchannel`

Ports the attach loop from the user's `amcrest-sdk` (multipart
`eventManager.cgi?action=attach&codes=[...]&heartbeat=5`, digest auth,
`--myboundary` block parsing) into the package — vendored subset, not a
module dependency, matching the repo's no-new-deps posture. Subscribed
codes and mapping:

| Amcrest code | action | type id |
|---|---|---|
| VideoMotion | Start | motion |
| SmartMotionHuman | Start | person |
| SmartMotionVehicle | Start | vehicle |
| CrossLineDetection | Start | line_cross (payload carries object class when present) |
| CrossRegionDetection | Start | motion |
| VideoBlind | Start | tamper |
| AudioMutation | Start | audio_alarm |
| AlarmLocal | Start | io_in |
| CallNoAnswered / _DoTalkAction_ / Invite (AD410) | any | doorbell |
| Heartbeat | — | dropped; refreshes the adapter's liveness deadline |

Only `action=Start` (or the doorbell trio) emits — `Stop` lines are
dropped. A stream with no traffic (no heartbeat either) for 90s is
treated as dead → error return → supervisor backoff.

### 5. API + SPA

- `event_channel` rides the camera wire object (GET/PATCH). PATCH
  triggers the manager reconcile via the existing cameras Bus update.
- SPA camera drawer: an "Events channel" select (Auto / ONVIF /
  Amcrest / Off) + a per-camera "last event" line from health
  `last_event_at`.
- No new endpoints: events surface through the existing
  `/v1/events` list/SSE.

## Error handling

- Adapter errors never propagate past the supervisor: backoff-restart
  with the error logged and `camera_health.last_error` updated via the
  collector's existing row (channel failures are visible but distinct
  from RTSP state).
- `events.Service.Insert` failure (e.g. unknown type id) logs and drops
  the single event — a poison event must not kill the channel.
- Credential rotation: cameras Bus Update → reconcile → adapter restart
  with fresh creds.

## Testing

- `vendorevents`: supervisor lifecycle (start/stop/backoff/reset,
  reconcile on bus events) against a scripted fake adapter; channel
  selection table.
- `amcrestchannel`: block parser against captured multipart fixtures
  (motion start/stop, SmartMotionHuman, doorbell, heartbeat); rising-
  edge/code filtering; dead-stream timeout.
- `onvifchannel`: topic→type mapping table; rising-edge filter against
  synthesized NotificationMessages.
- Live acceptance (LAN): walk in front of amcrest_110 → `motion` (and
  `person` if SmartMotion enabled) event in `/v1/events` within 5s +
  webhook delivery via an existing subscription; AD410 button press →
  `doorbell` event; `last_event_at` visible in health.
