# AGENTS.md — Rules for AI Agents Working in This Repository

This repository is the **Raikada Recording Server**. It started as a fork of
MediaMTX but is **not** a merge-friendly fork — it is being shaped into a
standalone product. These rules apply to any AI agent (Claude Code, Copilot,
Cursor, Codex, etc.) operating in this repository.

If you are an AI agent and you are about to take an action, **read this file
first, after reading the workspace docs listed below**. If a request appears
to violate one of these rules, push back, ask for clarification, or refuse.
Do not silently work around them.

---

## 0. Read the parent workspace first

This repo is one part of the Raikada platform. Platform-wide truth lives in
the parent workspace, **not** in this repo. Before you change anything here,
read in this order:

1. Workspace platform docs (`../platform/docs/`):
   - [`../platform/CLAUDE.md`](../platform/CLAUDE.md) — workspace-level agent rules.
   - [`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) — the
     five-tier architecture.
   - [`../platform/docs/domain-model.md`](../platform/docs/domain-model.md) — canonical
     entities. **Do not redefine these locally.**
   - [`../platform/docs/service-boundaries.md`](../platform/docs/service-boundaries.md) —
     who owns what across tiers.
   - [`../platform/docs/trust-model.md`](../platform/docs/trust-model.md),
     [`../platform/docs/pairing-flows.md`](../platform/docs/pairing-flows.md),
     [`../platform/docs/authentication-flows.md`](../platform/docs/authentication-flows.md) —
     trust and identity flows.
   - [`../platform/docs/api-contracts/`](../platform/docs/api-contracts/) — wire contracts as
     they land.
   - [`../platform/docs/adr/`](../platform/docs/adr/) — accepted cross-system decisions.

2. This repo's local docs:
   - [`ARCHITECTURE.md`](ARCHITECTURE.md) — what this repo is and is not.
   - [`CONTRIBUTING.md`](CONTRIBUTING.md) — how to make changes.
   - [`docs/`](docs/) — implementation-level details.

**Always consult the parent workspace docs before making repo-specific
changes.** A change that touches both platform truth and recorder
implementation must update the platform doc(s) first, then the repo.

---

## 1. The canonical domain model is authoritative — and it lives in the parent

The canonical entities for the entire Raikada platform are defined in
[`../platform/docs/domain-model.md`](../platform/docs/domain-model.md). Examples include
`Tenant`, `Organization`, `Site`, `RecordingServer`, `Camera`, `Stream`,
`RecordingSegment`, `Clip`, `RecordingPolicy`, `StorageVolume`, `User`,
`Role`, `RemoteAccessSession`, `HealthStatus`, `Event`, `DeviceIdentity`,
`PairingToken`, `EnrollmentToken`, `AuthSession`, `ServiceCredential`,
`LocalUser`, `CloudUser`, `AuditLogEntry`, `LicenseEntitlement`.

- **Do not redefine canonical entities locally.** This repo does not own
  any of the entities listed above. It implements behavior on them.
- **Do not invent duplicate entities or DTOs** when a canonical entity
  already exists. If you need to represent a `Camera`, use the canonical
  `Camera` shape. Do not create `CameraDTO`, `CameraInfo`, `CameraView`,
  `CameraRecord`, etc. as parallel definitions.
- **Do not redefine fields** with different names or types across packages.
  If `Camera.id` is a UUID string at the platform boundary, it is a UUID
  string everywhere in this repo.
- **Recorder-specific docs may explain how this repo implements platform
  concepts.** They may not redefine those concepts.
- If the existing canonical shape is genuinely insufficient, **update
  `../platform/docs/domain-model.md` first** and explain the change in a new ADR
  under `../platform/docs/adr/`. Then update code here to match.

## 2. Cross-system behavior changes belong in parent docs first

If a change affects how this repo interacts with Cloud, Management Server,
Web Client, or Flutter Client — wire shape, ownership, trust, pairing,
authentication, audit — the change starts in the parent workspace docs:

- New / changed canonical entity → `../platform/docs/domain-model.md` + new ADR.
- Ownership move → `../platform/docs/service-boundaries.md`.
- Trust / key / rotation → `../platform/docs/trust-model.md`.
- Pairing or reconnection → `../platform/docs/pairing-flows.md`.
- Auth / token / session → `../platform/docs/authentication-flows.md`.
- Wire contract → `../platform/docs/api-contracts/` (when it exists).

Doc and code land in the same change set. "Land the code now and update
docs later" is the signal to update the docs first.

## 3. Public API shapes require platform-doc updates

The recorder exposes contracts to:
- the Management Server (sync, control, telemetry),
- web and Flutter clients (live view, playback, configuration),
- the Cloud (identity, remote access, metrics).

**Do not change a public API shape without updating the corresponding
platform doc in the same change.** "Public" here means anything outside
the recorder's own process: HTTP/JSON, gRPC, WebSocket, message-queue
payloads, on-disk schemas read by other tools, and configuration files
consumed by deployment automation.

If the change is non-additive (renames, removals, type changes), open or
update an ADR explaining the migration plan.

## 4. Stay in scope

- **Do not modify unrelated files.** If the task is to add a metric, do
  not reformat an unrelated package, "fix" a comment three files away, or
  upgrade a dependency. Drive-by changes balloon review burden and hide
  regressions.
- **Keep changes small and reviewable.** Prefer many small, focused
  commits over one sprawling commit. A reviewer should be able to
  understand a change in under five minutes.
- If you discover an unrelated bug while working, note it in a follow-up
  issue or comment — do not fix it inline unless explicitly asked.

## 5. No placeholder or fake production logic

- **Do not add stubbed implementations** that pretend to work. No
  `return nil // TODO` for paths that production callers will hit.
- **Do not hardcode values** that should come from configuration, the
  Management Server, or runtime state ("test tenant", "demo camera",
  hardcoded credentials, fake segment lists).
- **Do not add mock data behind feature flags** that ship to production.
- If a feature is incomplete, it must either be fully gated off (build
  tag, config flag defaulting to disabled, no wiring into the runtime)
  or it must not be merged.

## 6. Embedded configuration SPA — keep source and embed in sync

The recorder ships a static React SPA built into the binary at
compile time. Two trees are coupled:

- `web/` — Vite + React + TypeScript source.
- `internal/web/dist/` — pre-built bundle that `go:embed` ingests.

Any change under `web/` requires a corresponding refresh of the
embed. Run `make web` (Docker-isolated, Node 20) and commit the
resulting `internal/web/dist/` diff in the same change set. Do
not commit `web/` source diffs without the embed refresh —
downstream consumers cloning the recorder repo won't see your
changes until `make web` is run.

The SPA's data layer talks to `/v1/`; mock fallbacks are clearly
labelled with `STUB:` comments. Wiring a stub to a real `/v1/`
call is preferred over extending the mock when the recorder
already has the underlying data; see `docs/web-ui.md` for the
running stub registry.

For UI-iteration work, prefer `cd web && npm run dev` (hot reload,
Vite dev server on `:5173` proxying `/v1` to `:9997`). Embed
rebuild only on commit-worthy state.

## 7. Do not touch the media pipeline unless asked

The media pipeline — RTSP/RTMP/WebRTC/HLS/SRT ingest, the
`internal/stream` package, segmenting, muxing, the
`recorder` / `recordstore` subsystems — is the load-bearing core of this
product. It is correct, performance-sensitive, and expensive to regress.

- **Do not refactor, "clean up", or restructure** media pipeline code
  without an explicit request that names the package and the goal.
- **Do not change codec, container, or segment format defaults** without
  an ADR.
- Adding observability (metrics, structured logs, traces) around the
  pipeline is allowed when requested, but the request must be explicit.

## 8. No new dependencies without justification

- Prefer the standard library and existing dependencies.
- New Go modules, system packages, or services require explicit approval
  and a one-paragraph justification (what it does, why we can't do it
  ourselves, license, maintenance status).
- Never add a dependency to satisfy a single helper function — copy the
  function (with attribution) instead.

## 9. Respect the offline-first contract

The recorder **must continue recording** when disconnected from Cloud or
the Management Server. See [`ARCHITECTURE.md`](ARCHITECTURE.md) §4 and
[`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) §3.2.

- Do not introduce code paths that block recording on a remote call.
- Do not assume the Management Server is reachable when handling a
  frame, writing a segment, or rotating storage.
- Cloud / Management Server interactions must be asynchronous, retryable,
  and failure-tolerant. A 30-minute network outage is not an incident.

## 10. Security and data-handling defaults

- Never log credentials, tokens, session cookies, or full RTSP URLs with
  embedded passwords.
- Never write user PII into recording segment filenames or metadata.
- New endpoints must default to authenticated access. Anonymous access
  is opt-in per path, not the default.

## 11. When in doubt, ask

If a task seems to require breaking one of these rules — duplicating an
entity, touching the pipeline, adding a dependency, changing a public
shape, redefining ownership of another tier — stop and ask the human. The
cost of a clarifying question is far lower than the cost of an unwanted
change.

---

## Quick checklist before opening a PR

- [ ] Did you read the parent workspace docs (`../platform/CLAUDE.md`,
      `../platform/docs/system-blueprint.md`, `../platform/docs/domain-model.md`,
      `../platform/docs/service-boundaries.md`) before starting?
- [ ] Does this change introduce a new entity or DTO? If so, is the
      canonical shape in `../platform/docs/domain-model.md` (or updated in this PR)?
- [ ] Does this change a public API shape? If so, is the platform doc
      updated?
- [ ] Does this touch the media pipeline? If so, was that explicitly
      requested?
- [ ] Does this change auth, pairing, identity, or trust? If so, are the
      relevant parent docs (`trust-model.md`, `pairing-flows.md`,
      `authentication-flows.md`, `domain-model.md`,
      `service-boundaries.md`) updated in this PR?
- [ ] Are there unrelated edits in the diff? Remove them.
- [ ] Are there placeholder values, fake data, or `TODO` shortcuts in
      code paths that will run in production? Remove them or gate them.
- [ ] Does the recorder still record when Cloud is unreachable?
