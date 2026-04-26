# Canonical Divergences

This document lists every place the recorder's current HTTP API
implementation — described faithfully in
[`../api/openapi.yaml`](../api/openapi.yaml) — diverges from the
canonical domain model in
[`../../docs/domain-model.md`](../../docs/domain-model.md).

Each entry is **tracked debt**, not a unilateral repair. Resolutions
are out of scope for this run. Each one will be resolved either by a
code change (PR), a canonical-model change (ADR), or — most often —
both, coordinated. Until then, the spec describes the divergent
reality and this document tracks the gap.

The authoritative spec is the OpenAPI document. This file is the
running list of "things the spec describes truthfully but should
eventually not have to describe at all."

Entries are grouped by severity:

1. **Canonical-rule violations** — exposing internal types at the
   public boundary, missing required canonical context like
   `tenant_id`, security-shape divergences. These are the worst.
2. **Name / shape mismatches** — string identifiers where canonical
   expects UUIDs, schemas that are subsets of their canonical
   counterparts.
3. **Data-classification gaps** — fields that aren't redacted or
   masked correctly relative to `../../docs/data-classification.md`.
4. **Cosmetic / structural** — tracked for completeness but not
   blocking anything.

---

## 1. Canonical-rule violations

### D1. No `tenant_id` anywhere on the API surface

> **Highest priority.** Resolution unblocks every cross-tier
> projection.

- **Where.** Every endpoint, every list and detail response, every
  request body.
- **What.** Per ADR 0001 cross-cutting rule 3, every canonical
  entity carries `tenant_id`. The recorder's API surface omits it
  entirely. Today the recorder is single-tenant per appliance so
  the field is implicit, but the moment cross-tier consumers (MS,
  Cloud) project from this surface the missing field becomes a
  silent translation gap.
- **Spec today.** No `tenant_id` field on any schema.
- **Canonical.** Mandatory `tenant_id: id` on every entity.
- **Proposed resolution.** Add `tenant_id` to every list and detail
  response. Recorder populates from a per-appliance value bound at
  pairing time per ADR 0002 (`PairingToken` carries `tenant_id`).
  Additive — no consumer-breaking change.

### D2. `/v3/config/paths/*` exposes `conf.Path` directly at the public surface

- **Where.**
  `/v3/config/paths/{list,get,add,patch,replace,delete}`,
  `/v3/config/pathdefaults/{get,patch}`. Schema `PathConf`.
- **What.** ADR 0001 R5 forbids exposing MediaMTX-internal types as
  Raikada canonical types. `conf.Path` is a MediaMTX-internal type
  with ~80 fields whose vocabulary (`source`, `sourceFingerprint`,
  `record`, `runOnReady`, etc.) has no canonical equivalent. The
  canonical replacement is `Camera` plus `RecordingPolicy`.
- **Spec today.** `PathConf` exposed in full.
- **Canonical.** `Camera` + `RecordingPolicy` per `domain-model.md`.
- **Proposed resolution.** **ADR 0009 (Recorder configuration:
  `conf.Path` to canonical `Camera` + `RecordingPolicy`).** The
  translation is non-trivial: not all `conf.Path` fields map cleanly
  (`sourceOnDemand`, `runOnReady*`, segmenting knobs); some
  canonical fields (`Camera.tenant_id`, `Camera.site_id`,
  `RecordingPolicy.id`) have no `conf.Path` equivalent. ADR 0009
  must propose the migration path, the deprecation window for the
  current `/v3/config/paths/*` endpoints, and whether the new
  endpoints (`/v3/cameras/*`, `/v3/recording-policies/*`) live
  alongside the old ones during transition. D3, D4, D7, D8, D9,
  D10, and D13 are bundled under the same ADR because they share
  the same translation mechanism.

### D3. `/v3/paths/{list,get}` runtime view exposes MediaMTX vocabulary

- **Where.** `/v3/paths/list`, `/v3/paths/get/*name`. Schemas
  `Path`, `PathSource`, `PathReader`, `PathSourceType`,
  `PathReaderType`.
- **What.** `PathSource.type` (15 values) and `PathReader.type`
  (10 values) carry MediaMTX runtime concepts (`rtmpSource`,
  `webRTCSession`, `rpiCameraSource`, etc.) with no canonical
  analog. The canonical analog is `Camera.transport` (one enum
  value) plus `Stream` as a separate runtime entity.
- **Spec today.** `Path` schema with full MediaMTX vocabulary.
- **Canonical.** `Camera.transport: enum<rtsp, rtsps, rtmp, srt,
  webrtc, hls, file>` + `Stream` (separate canonical entity for
  runtime instances).
- **Proposed resolution.** Bundled under **ADR 0009**. Introduce
  `/v3/cameras/{list,get}` returning canonical `Camera`, plus
  `/v3/streams/{list,get}` for active streams. Keep
  `/v3/paths/{list,get}` as deprecated alongside until consumers
  migrate.

### D4. `PathConf.source` may contain embedded camera credentials in cleartext

- **Where.** `/v3/config/paths/{list,get,add,patch,replace}`,
  `/v3/config/pathdefaults/{get,patch}`. Schema `PathConf.source`.
- **What.** The `source` URL accepts the form
  `rtsp://user:pass@host/path`. The canonical model splits this
  into `Camera.source_url` (Sensitive, credential-free) plus
  `Camera.credentials_ref` (Operational pointer into a
  Credential-classified secret store). The recorder currently
  returns the credential-bearing URL in API responses without
  redaction.
- **Spec today.** `PathConf.source: string`, annotated
  `x-classification: credential` (Phase 3) but still returned in
  raw form.
- **Canonical.** `Camera.source_url` + `Camera.credentials_ref`.
- **Proposed resolution.** Even before ADR 0009 lands, redact the
  userinfo portion of `source` URLs in responses
  (`rtsp://***:***@host/path`). Full split arrives with ADR 0009.
  This is the only entry in this section recommended for
  short-term mitigation independent of the ADR.

### D5. `remoteAddr` (PII) returned to every authorized session viewer without masking

> **Resolution depends on a permission-catalog change** that is
> itself a separate workstream (canonical `Role` catalog
> definition). This entry cannot be cleanly closed before that
> catalog gains a `session.pii.read` permission or equivalent.

- **Where.** `RTSPConn.remoteAddr`, `RTSPSession.remoteAddr`,
  `RTMPConn.remoteAddr`, `WebRTCSession.remoteAddr`,
  `SRTConn.remoteAddr`, `HLSSession.remoteAddr`.
- **What.** Per `data-classification.md` (the cross-cutting rule
  for IP-with-session and the `AuthSession.source_ip` precedent),
  an IP address paired with an authenticated session is PII. The
  recorder currently returns full `remoteAddr` to any caller
  authorized to read sessions, regardless of whether they have an
  explicit PII-read permission.
- **Spec today.** Annotated `x-classification: pii` (Phase 3) but
  returned in cleartext to all viewers.
- **Canonical.** PII handling per `data-classification.md`, gated
  by a fine-grained role permission.
- **Proposed resolution.** Add a viewer-permission check; mask
  `remoteAddr` (last-octet redaction or `/24` aggregation) for
  callers without `session.pii.read`. **Dependency:** the canonical
  `Role` permission catalog (per ADR 0002 OQ and the broader
  identity workstream) must define `session.pii.read` as an
  explicit permission first. Until then, the spec is honest about
  what's exposed; the recorder cannot enforce a permission the
  catalog does not define.

### D6. Non-standard `Authorization: Bearer user:pass` accepted

- **Where.** `internal/protocols/httpp/credentials.go`; spec
  security scheme `bearerUserPass`.
- **What.** The recorder accepts `Authorization: Bearer user:pass`
  (literal colon-separated credentials inside a Bearer token).
  Inherited from upstream MediaMTX. Conflicts with the OAuth /
  RFC 6750 meaning of `Bearer` (opaque token), risks tokens being
  mishandled by middleware that assumes JWT-shaped bearers, and
  broadens the credential-extraction surface for no functional
  gain — `basicAuth` covers the same use case in a standards-
  compliant way.
- **Spec today.** Documented as `bearerUserPass` security scheme
  with a description noting it is non-standard.
- **Canonical.** ADR 0002 OQ10 (concrete credential mechanism,
  deferred). The mechanism eventually selected — JWT/JWKS, mTLS,
  opaque-bearer-with-introspection, short-lived service tokens, or
  a hybrid — does not include `Bearer user:pass`.
- **Proposed resolution.** Three-step deprecation, sequenced and
  bounded so the divergence does not linger:
  1. **Now:** mark `bearerUserPass` deprecated in
     [`../api/openapi.yaml`](../api/openapi.yaml); have the
     recorder emit a `Deprecation` HTTP response header on
     requests authenticated via the scheme.
  2. **At the moment ADR 0002 OQ10 is decided:** add a server-
     side log warning on every `bearerUserPass` use, with the
     intent that the chosen successor (JWT, mTLS, …) is the
     migration target.
  3. **One release after OQ10's mechanism ships in production:**
     remove `bearerUserPass` acceptance from
     `internal/protocols/httpp/credentials.go`. Step 3 is the only
     step that touches code; steps 1 and 2 are doc / config.
  Migration guidance for clients: move to `basicAuth` (same
  primitive, standards-compliant) immediately, then to whatever
  OQ10's successor lands.

## 2. Name / shape mismatches

All entries below are bundled under the same migration —
**ADR 0009** — because they share the canonical-translation
mechanism with D2 / D3. They are listed individually so each can
be tracked, but they will resolve together.

### D7. String path-name identifiers where canonical expects UUIDs

- **Where.** `*name` route params on `/v3/config/paths/*`,
  `/v3/paths/*`, `/v3/recordings/*`, `/v3/hlsmuxers/*`. Fields
  `Path.name`, `Recording.name`, `PathConf.name`,
  `HLSMuxer.path`.
- **What.** Routes use recorder-local string identifiers; canonical
  entities use UUIDs (`Camera.id`, `RecordingSegment.id`).
  Cross-tier consumers cannot project recorder-local strings into
  the canonical id space.
- **Spec today.** `*name` wildcard params with string values.
- **Canonical.** UUID identifiers per ADR 0001.
- **Proposed resolution.** Under ADR 0009: introduce UUID-keyed
  endpoints; recorder maintains a string-path → `Camera.id`
  translation table. String routes deprecated.

### D8. `RecordingSegment` schema is a 2-field stub (`{ start }`)

- **Where.** Schema `RecordingSegment`; endpoints
  `/v3/recordings/{list,get}`.
- **What.** Canonical `RecordingSegment` has 17 fields. The spec
  returns only `start`. Missing: `id`, `tenant_id`, `site_id`,
  `recording_server_id`, `volume_id`, `policy_id`, `started_at`
  (vs the current `start`), `ended_at`, `duration`, `container`,
  `size_bytes`, `tracks`, `checksum`, `state`, `created_at`. The
  canonical `path` field is recorder-local and per
  `data-classification.md` correctly should *not* appear in
  client-facing responses.
- **Spec today.** `RecordingSegment { start: string }`.
- **Canonical.** Full 17-field shape.
- **Proposed resolution.** Under ADR 0009: expand additively, then
  rename `start` → `started_at` in a separate non-additive change
  once consumers migrate.

### D9. `user` field on sessions is a free-form login string, not a `User.id`

- **Where.** `RTSPSession.user`, `WebRTCSession.user`,
  `RTMPConn.user`, `SRTConn.user`, `HLSSession.user`.
- **What.** Carries the username from credentials, not a canonical
  UUID reference. Cross-tier consumers cannot join sessions to
  identity.
- **Canonical.** `user_id: ref<User>` (or `LocalUser`) per
  `domain-model.md`.
- **Proposed resolution.** Under ADR 0009: add `user_id: uuid`
  alongside; deprecate `user` as a display-name alias.

### D10. `path` field on sessions is a string path-name, not a `camera_id`

- **Where.** `RTSPSession.path`, `WebRTCSession.path`,
  `RTMPConn.path`, `SRTConn.path`, `HLSSession.path`,
  `HLSMuxer.path`.
- **What.** Same root cause as D7. Cross-tier consumers cannot
  join sessions to a canonical `Camera`.
- **Canonical.** `camera_id: ref<Camera>`.
- **Proposed resolution.** Under ADR 0009: add `camera_id: uuid`
  alongside; deprecate `path` as an alias.

## 3. Data-classification gaps

### D11. SRT statistics fields carry no per-field classification annotation

> **Convention adopted, applied to the spec already.** Operational
> is the default; schemas and fields without an explicit
> `x-classification` are Operational. Only divergent classifications
> (PII, Sensitive, Credential, Restricted) are annotated. The spec's
> `info.description` declares this convention; this divergence is
> already discharged by adopting it.

- **Where.** Schema `SRTConn`, ~50 statistics fields after `user`.
- **What.** Uniformly Operational. Under the convention above, no
  annotation is required. This entry stays in the document to
  record the decision, not as outstanding debt.

### D12. `query` field on sessions may carry Sensitive / PII / Credential content

- **Where.** `RTSPSession.query`, `WebRTCSession.query`,
  `RTMPConn.query`, `SRTConn.query`, `HLSSession.query`.
- **What.** Annotated `x-classification: sensitive` (Phase 3) — but
  a customer who passes `?token=...` or `?password=...` in their
  stream URL has those values land in API responses. The recorder
  does not currently redact.
- **Spec today.** Annotated Sensitive; values returned raw.
- **Proposed resolution.** Redact known credential patterns
  (`token=`, `password=`, `key=`, `secret=`) in `query` before
  returning. Optionally return a hash or `has_query: true` marker
  rather than the full contents. Independent of ADR 0009 — can
  ship as a small recorder-side change.

### D13. No upgrade path for Restricted-classification fields when canonical `Camera` / `Site` arrive

- **Where.** Forward-looking; applies when ADR 0009 lands.
- **What.** `data-classification.md` upgrades `Site.coordinates`
  and `Camera.position` to Restricted at residential precision.
  The current spec has no facility for the upgrade rule.
- **Proposed resolution.** Bundle the upgrade rule and its storage
  / transmission constraints into ADR 0009.

## 4. Cosmetic / structural

### D14. `APIInfo` is the one parallel-shape suffix grandfathered in the `canonicalnames` lint baseline

> Renumbered from earlier draft; the original D14 (`deprecated`
> field tags missing from spec) was investigated and confirmed
> already correct in the spec — no fix needed.

- **Where.** Schema `Info` (Go type `APIInfo` in
  `internal/defs/api.go`).
- **What.** Per ADR 0001 the `*Info` suffix is forbidden as a
  parallel-shape pattern; this one predates the lint and is
  grandfathered in
  `internal/linters/canonicalnames/grandfathered.txt`.
- **Proposed resolution.** Rename in a future cleanup; until then,
  grandfathered.

### D15. `OK` / `Error` envelopes are an in-house `{status, error}` shape, not RFC 7807

- **Where.** Schemas `OK`, `Error`; every endpoint that uses them.
- **What.** Cross-tier consumers must recognize the in-house
  envelope. Differs from `application/problem+json` (RFC 7807) and
  from common industry conventions.
- **Proposed resolution.** Keep as-is for v1 to avoid breaking
  existing clients. **Forward reference: a future platform-API-
  contracts ADR will decide whether to migrate the entire platform
  to RFC 7807 or to standardize on the in-house shape across
  tiers.** Until then, this stays as-is and the recorder's idiom
  is documented in the cross-tier API contract.

---

## Out of scope for this document

Operational hardening items surfaced during the audit but not
canonical-model divergences:

- **CORS allow-list defaults.** `internal/api/api.go`'s
  `middlewarePreflightRequests` and `httpp.Server.AllowOrigins`
  control which origins can reach the API. This is a deployment-
  configuration concern, not a divergence between the API surface
  and the canonical domain model. Production deployment guidance
  lives in the recorder's deployment notes (TBD).

---

## Resolution tracking

Resolution of each entry is **out of scope for this run**. Each
entry above will be closed by:

- a code change (small, mechanical fixes — D4 redaction, D6 step 1,
  D12 redaction);
- an ADR (D2 / D3 / D7 / D8 / D9 / D10 / D13 → **ADR 0009**;
  D5 → role-catalog ADR; D6 step 3 → coordinated with
  ADR 0002 OQ10);
- a future platform-API-contracts ADR (D15 forward reference).

When an entry is closed, this document updates: the entry stays
for the historical record, with a `**Status: closed by <ADR / PR>
on <date>**` line at the top of its body.
