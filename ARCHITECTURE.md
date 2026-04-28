# ARCHITECTURE.md — Raikada Recording Server

This document describes what **this repository** is, what it is not, and
how it implements its slice of the Raikada platform.

> **Platform-wide context lives in the parent workspace, not here.**
> Before reading on, skim:
> - [`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) — the
>   five-tier architecture and how the recorder fits in.
> - [`../platform/docs/domain-model.md`](../platform/docs/domain-model.md) — canonical
>   entities (`Camera`, `Stream`, `RecordingPolicy`, `RecordingSegment`,
>   `Clip`, etc.).
> - [`../platform/docs/service-boundaries.md`](../platform/docs/service-boundaries.md) —
>   what every tier owns, including this one.
> - [`../platform/docs/trust-model.md`](../platform/docs/trust-model.md),
>   [`../platform/docs/pairing-flows.md`](../platform/docs/pairing-flows.md),
>   [`../platform/docs/authentication-flows.md`](../platform/docs/authentication-flows.md) —
>   how trust and identity flow across the platform.
>
> This file describes only what is true **inside this repository**. Where
> a topic is platform-wide (entity shapes, tier ownership, connectivity
> modes), it is referenced, not duplicated.

---

## 1. What this repository is

This repository contains the **Raikada Recording Server** ("the recorder").

The recorder is a long-lived process that runs **on customer premises**,
typically on a small appliance (NUC, industrial PC, ARM box) that sits on
the same LAN as the cameras it records. It is the component that:

- ingests media from cameras (RTSP / RTMP / WebRTC / SRT / HLS / etc.),
- persists media to local storage as bounded segments,
- exposes those segments for live playback, playback of past footage, and
  download to authorized clients,
- prepares clip exports (stitched files) when requested by the Management
  Server,
- emits health, status, and event telemetry,
- enforces recording policies handed down by the Management Server.

It is forked from MediaMTX. The MediaMTX core (publishing, reading,
segmenting, muxing) remains the load-bearing engine and is treated as a
stable foundation, not a thing to refactor.

## 2. What this repository is not

The recorder is **not**:

- a billing system,
- a multi-tenant identity provider,
- a fleet manager (it does not manage *other* recorders),
- a cross-site analytics platform,
- a UI host for end users (web and Flutter clients live in other repos),
- the source of truth for tenants, organizations, users, roles, sites, or
  camera inventory across the platform.

If a feature request implies any of the above, it almost certainly belongs
in **Cloud** or the **Management Server** — see
[`../platform/docs/service-boundaries.md`](../platform/docs/service-boundaries.md).

## 3. Where the recorder sits in the larger system

For the platform-wide picture (tiers, diagram, connectivity modes), see
[`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md).

In one sentence: **a Recording Server is one on-prem appliance, registered
with exactly one Management Server, recording cameras at exactly one
Site.** It does not know about other recorders.

## 4. Independence from Cloud and Management Server

> The recorder must operate independently if disconnected from Cloud or
> the Management Server.

This is a **load-bearing requirement**, not a nice-to-have. Sites lose
internet for hours or days. The recorder must keep recording.

When Cloud and the Management Server are unreachable, the recorder must
still:

- accept media from configured cameras and write segments to disk,
- enforce its **last-known** recording policy and retention rules,
- serve live streams and recorded segments to LAN clients that can
  authenticate against locally cached credentials,
- enforce storage limits (rotate / prune segments per policy),
- queue health, event, and audit records for later upload,
- return cleanly to normal operation when connectivity returns, without
  losing buffered telemetry and without double-uploading segments.

Things the recorder does **not** need to do offline:

- prove identity to a brand-new user it has never seen,
- bill, license-check, or verify entitlement,
- update its own configuration based on a Management Server change made
  while offline (it picks that up on reconnect).

This shapes the architecture: every interaction with Cloud or the
Management Server is asynchronous, retryable, and never on the recording
hot path. Connectivity modes (cloud-connected, LAN-only, temporarily
disconnected, air-gapped) are defined platform-wide in
[`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) §2.

## 5. What the recorder owns (recorder-specific implementation)

These are the concerns this repository implements. The platform-level
ownership claims live in
[`../platform/docs/service-boundaries.md`](../platform/docs/service-boundaries.md); the
items below are the **how** for that **what**.

1. **Media ingest.** Accept streams from cameras over the supported
   protocols. Handle reconnects, codec changes, and clock drift.
   Implementation details: [`docs/media-pipeline-notes.md`](docs/media-pipeline-notes.md).
2. **Local recording.** Segment incoming media into fMP4 (or MPEG-TS)
   files on local storage with deterministic naming and a queryable
   index. Details: [`docs/recorder-recording.md`](docs/recorder-recording.md).
3. **Segment indexing.** Maintain the authoritative local index of
   `RecordingSegment` rows; project a subset to the Management Server.
   Details: [`docs/recorder-recording.md`](docs/recorder-recording.md).
4. **Storage management.** Track `StorageVolume` capacity, fill order,
   pruning by `RecordingPolicy.retention_duration`, refusing writes when
   exhausted. Details: [`docs/recorder-storage.md`](docs/recorder-storage.md).
5. **Playback and live distribution.** Serve historical segments
   (gapless seek across segments) and re-serve live streams in the
   requested protocol to LAN and broker-tunnelled clients.
6. **Clip export preparation.** Stitch the source segments referenced by
   a `Clip` into a single export artifact, write it to local storage,
   compute its checksum, and serve the download. Pin the source segments
   while the clip is active.
7. **Locally applied configuration.** Apply the active `RecordingPolicy`,
   `Camera` set, role / signing-key cache, and license-derived feature
   flags as pushed by the Management Server. Operate from the
   last-known-good snapshot when the MS is unreachable. Details:
   [`docs/recorder-config.md`](docs/recorder-config.md).
8. **Health and capabilities.** Emit `HealthStatus` heartbeats and
   `Event` records. Surface capability and version info to the
   Management Server.
9. **Management Server pairing client.** Implement the recorder side of
   the pairing, reconciliation, and reconnection flows defined in
   [`../platform/docs/pairing-flows.md`](../platform/docs/pairing-flows.md).
10. **Local authentication enforcement.** Validate Cloud- or
    MS-issued tokens locally against cached issuer material; enforce
    permissions on media access. Recorder is **not** the primary
    user-auth authority — see
    [`../platform/docs/authentication-flows.md`](../platform/docs/authentication-flows.md) §3.2.
11. **Remote-access participation.** Terminate sessions brokered by the
    Cloud so off-LAN clients can reach this recorder securely.
12. **Embedded configuration UI.** Ship a static React SPA built into the
    recorder binary at compile time and serve it from the API listener
    (`/`, `/assets/*`, hash-routed sub-paths). The UI is the operator
    surface for first-run setup, single-recorder management, and (over
    same-LAN MS connections per ADR 0003) the consolidated dashboard
    target. Source under [`web/`](web/), embed wiring under
    [`internal/web/`](internal/web/), refresh workflow documented in
    [`docs/web-ui.md`](docs/web-ui.md).

## 6. What the recorder does not own

(Cross-reference; the canonical list is
[`../platform/docs/service-boundaries.md`](../platform/docs/service-boundaries.md) §3.)

- The canonical list of tenants, organizations, sites, users, or roles.
- Billing, metering, or licensing enforcement.
- Cross-recorder fleet view.
- Camera discovery and onboarding flows beyond accepting a configured
  endpoint.
- AI / analytics inference.
- Long-term cold archival storage.
- User-auth authority (recorder validates tokens issued upstream;
  diagnostic local-admin and physical break-glass paths are tightly
  scoped — see
  [`../platform/docs/authentication-flows.md`](../platform/docs/authentication-flows.md) §3.2).

## 7. Trust boundaries inside the recorder

(Platform trust model: [`../platform/docs/trust-model.md`](../platform/docs/trust-model.md).)

- **Camera ↔ Recorder** — local network. Camera credentials are stored
  on the recorder, encrypted at rest. Cameras are not trusted to be
  well-behaved (malformed RTP, jitter, codec changes mid-stream are all
  expected and tolerated).
- **Recorder ↔ Management Server** — authenticated, transport-encrypted.
  The MS is trusted to send valid configuration. The recorder validates
  configuration shape but trusts the *semantics*.
- **Recorder ↔ Cloud** — only via the Management Server, except for the
  remote-access broker, where the Cloud terminates a tunnel and the
  recorder treats the far end as an untrusted client until it
  authenticates.
- **Recorder ↔ Web/Flutter Client** — untrusted until the client
  presents a valid token. Tokens are issued by Cloud or the Management
  Server; the recorder validates them locally using cached issuer
  material (concrete credential mechanism per the security/API ADR; see
  [`../platform/docs/adr/0002-trust-pairing-authentication.md`](../platform/docs/adr/0002-trust-pairing-authentication.md)
  OQ10).

## 8. Configuration sources, in priority order

1. **Local config file** (`mediamtx.yml` or its successor) — bootstrap
   only: how to find the Management Server, where to put recordings,
   what TLS material to use.
2. **Management Server push** — runtime configuration: cameras, paths,
   recording policies, retention, users, roles. Overrides local config
   for everything except bootstrap fields.
3. **Cloud-driven overrides** — fleet-wide settings (e.g. emergency
   read-only mode) delivered through the Management Server.

The recorder never accepts runtime configuration from a web or Flutter
client directly. Clients ask the Management Server, which then pushes
to the recorder. Details: [`docs/recorder-config.md`](docs/recorder-config.md).

## 9. Why this matters

The recorder is the only component that touches actual video. If it is
wrong, footage is lost, and footage cannot be recovered after the fact.
Every architectural decision in this repository should be evaluated
against: *does this make it more or less likely that we lose recordings?*
That is the north star, restated from the platform-wide commitment in
[`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) §3.1.
