# Canonical Divergences

This document lists every place the recorder's current HTTP API
implementation — described faithfully in
[`../api/openapi.yaml`](../api/openapi.yaml) — diverges from the
canonical domain model in
[`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md).

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
   masked correctly relative to `../../platform/docs/data-classification.md`.
4. **Cosmetic / structural** — tracked for completeness but not
   blocking anything.

---

## 1. Canonical-rule violations

### D1. No `tenant_id` anywhere on the API surface

> **Status: resolved.** Closed by the D1 commit on 2026-04-26. See
> the Resolved section at the bottom of this document.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

> **Status: response-side mitigation resolved.** Closed by the D4
> commit on 2026-04-26. The full canonical split (`Camera.source_url`
> + `Camera.credentials_ref`) still requires ADR 0009; this entry
> now tracks only the canonical-shape gap, not the cleartext-leak
> risk. See the Resolved section.

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
  (`rtsp://redacted:redacted@host/path`). Full split arrives with
  ADR 0009. This is the only entry in this section recommended for
  short-term mitigation independent of the ADR.

### D5. `remoteAddr` (PII) returned to every authorized session viewer without masking

> **Status: resolved 2026-04-27.** Closed by the PII-unmasking-gate
> commit on `main`, Change-Id `2026-04-27-adr-0010-pii-gate`. ADR
> 0010 (Accepted) defines `session.pii.read`; ADR 0011 (Accepted)
> defines the JWT `scope` claim that carries it. See the Resolved
> section at the bottom of this document.

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

> **Status: resolved.** Closed by the D6 commit on 2026-04-26. The
> code path is removed; the spec's `bearerUserPass` security scheme
> is gone; clients that relied on the non-standard form must use
> HTTP Basic. See the Resolved section.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

> **Status: resolved.** Closed by the D12 commit on 2026-04-26. See
> the Resolved section.

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

> **Status: closed by ADR 0009 implementation on 2026-04-26.** See
> the Resolved section at the bottom of this document.

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

---

## Resolved

### D5. `remoteAddr` PII gated on `session.pii.read`

**Resolved 2026-04-27.** Closed by the PII-unmasking-gate commit on
`main`, Change-Id `2026-04-27-adr-0010-pii-gate`.

The fix gates Stream-level PII redaction on the per-request
Principal's scope claim per ADR 0011 D2 / D7. The Principal-on-context
plumbing landed earlier with the JWT-middleware commit; this commit
wires its `HasPermission(PermSessionPIIRead)` check into
`internal/api/api_v1_streams.go`'s `redactStream`. Callers without the
permission see the existing redaction (`remote_addr` →
`"redacted"`, RTSP `transport_connections[].remote_addr` → `"redacted"`,
WebRTC `local_candidates[]` and `remote_candidates[]` → `"redacted"`);
callers with the permission see the raw values. Sensitive
`query_string` redaction (D12 closure) remains unconditional —
Sensitive is value-pattern redaction, not principal-gated.

Pre-OQ10 deployments using the recorder's HTTP Basic / static
authentication paths produce a service-account Principal with empty
Scope, so they fail-closed by default. A new bootstrap-config flag
`globalPIIReadGrant: bool` (default false) is the operator escape
hatch: when set, `principalFromAuthClaims` injects
`session.pii.read` into the static-auth Principal so legacy admin UIs
continue to see unmasked Stream PII without requiring an immediate
JWT migration. JWT-authed requests carry their own scope claim and
ignore the flag.

Files touched:

- `internal/conf/conf.go` — adds `GlobalPIIReadGrant` to `Conf`.
- `internal/api/principal.go` — adds `PermSessionPIIRead` constant;
  threads `globalPIIReadGrant bool` through `principalFromAuthClaims`.
- `internal/api/tenant.go` — adds `globalPIIReadGrant()` accessor
  alongside `tenantID()`, mutex-protected like its sibling.
- `internal/api/api.go` — passes the conf flag into the principal
  builder at middleware time.
- `internal/api/api_v1_streams.go` — `redactStream` takes a
  `*Principal` and skips PII rewrites when the permission is held;
  list / get handlers fetch the Principal once and pass it through.
- `internal/api/api_v1_streams_test.go` — four new test cases:
  unauthenticated, service-account-without-permission,
  principal-with-permission, and globalPIIReadGrant escape-hatch.
- `internal/api/principal_test.go` — adds the
  `globalPIIReadGrant` injection test; existing call sites updated
  for the new signature.

### D12. `query` field credential-pattern redaction

**Resolved 2026-04-26.** Closed by the D12 commit on `main`.

The fix adds `redactQueryString` in `internal/api/redact.go` and
applies it in every handler that returns a session / connection type
with a `query` field: `onHLSSessionsList/Get`,
`onRTSPSessionsList/Get` and `onRTSPSSessionsList/Get`,
`onRTMPConnsList/Get` and `onRTMPSConnsList/Get`,
`onSRTConnsList/Get`, `onWebRTCSessionsList/Get`.

The helper parses the query string, walks the keys, and replaces
values for any key whose name contains `token`, `password`, `key`,
or `secret` (case-insensitive substring match) with the literal
`redacted`. Other keys pass through unchanged. The keys themselves
remain visible so a reviewer can see that a sensitive parameter was
present without seeing what it was — defense-in-depth, not a
guarantee.

Unit tests cover empty input, non-sensitive passthrough, each of the
four trigger keywords individually, all four together, mixed
sensitive / non-sensitive, case-insensitive matching, substring
match (`access_token`, `apikey`), and an unparseable-fallback case.
Existing handler tests had query fixtures using `key=val` and
`token=abc` — those collided with the new redaction patterns, so
the fixtures were renamed to `q=val` and `p=abc` (non-trigger keys)
to keep the round-trip semantics of those tests unchanged.

OpenAPI spec gains a top-level `query` field redaction note in
`info.description`, alongside the `tenantId` convention.

### D6. Non-standard `Authorization: Bearer user:pass` removed

**Resolved 2026-04-26.** Closed by the D6 commit on `main`.

`internal/protocols/httpp/credentials.go` no longer interprets
`Authorization: Bearer user:pass` as colon-separated credentials.
Anything after `Bearer ` is now treated opaquely as a token, validated
downstream per the platform's chosen credential mechanism (see ADR
0002 OQ10). Clients that previously relied on the non-standard form
must switch to HTTP Basic — same primitive, standards-compliant.

OpenAPI spec changes:
- The `bearerUserPass` security scheme is removed from
  `components.securitySchemes`.
- Top-level `security` lists only `basicAuth` and `bearerJWT`.
- The `bearerJWT` description gains a note explaining that a
  colon-containing bearer is now opaque, with a back-reference to
  this entry.

The unit test `TestCredentials/user_and_pass_in_bearer` was renamed
to `TestCredentials/colon-separated_bearer_is_now_opaque_(D6)` and
flipped to assert the new behavior (the input becomes
`Token: "myuser:mypass"`, not `User`/`Pass`).

This closes step 1 of the three-step deprecation plan in the
original D6 entry. Steps 2 and 3 (server-side log warnings and final
removal coordinated with ADR 0002 OQ10) are now moot — the path is
already gone.

### D4. `PathConf.source` userinfo leaked in cleartext on responses

**Resolved 2026-04-26 (response-side mitigation).** Closed by the D4
commit on `main`.

The fix adds `redactSourceURL` in `internal/api/redact.go` and applies
it in three handlers: `onConfigPathsList`, `onConfigPathsGet`, and
`onConfigPathDefaultsGet`. URLs of the form
`scheme://user:pass@host/path` are returned as
`scheme://redacted:redacted@host/path`; URLs without userinfo and
empty / unparseable strings pass through unchanged.

The recorder **still accepts** full credential-bearing source URLs in
PATCH / POST bodies — write-side parsing is unchanged. The full
canonical split (`Camera.source_url` + `Camera.credentials_ref`) is
the subject of ADR 0009 and remains a separate workstream; this fix
addresses only the response-side cleartext-leak risk.

OpenAPI spec `PathConf.source.description` updated to document the
asymmetric input/output behavior. Unit tests for the helper cover
empty input, no-scheme input, no-userinfo URL, with-userinfo URL,
userinfo-with-port, variant scheme (`udp+rtp`), and the
unparseable-fallback case.

### D1. No `tenant_id` anywhere on the API surface

**Resolved 2026-04-26.** Closed by the D1 commit on `main`.

The fix added `tenantId` to:

- `conf.Conf` (the recorder's bootstrap config struct), required by
  `Conf.Validate()` — the recorder will not start without it.
- `mediamtx.yml` — the bootstrap config file gains a `tenantId:`
  example with a comment about the ADR 0002 pairing path.
- Every detail-level API response type
  (`APIInfo`, `APIPath`, `APIRecording`, `APIHLSMuxer`,
  `APIHLSSession`, `APIRTSPConn`, `APIRTSPSession`, `APIRTMPConn`,
  `APISRTConn`, `APIWebRTCSession`).
- `conf.Path` (per-path configuration shape returned by
  `/v3/config/paths/get/*name`) — auto-flows into `OptionalPath`
  via the existing reflection-derived patch shape.
- The OpenAPI spec at `recorder/api/openapi.yaml`.

Handlers stamp `tenantId` on responses at the API boundary using a
small `tenantID()` helper in `internal/api/tenant.go`, which reads
the recorder's `Conf.TenantID` under the existing config mutex. The
producer packages (core, servers/{hls,rtsp,rtmp,srt,webrtc}) were
not modified — the field is filled at the API boundary.

Patch / add / replace bodies on `/v3/config/*` endpoints validate an
incoming `tenantId` (if present) against the recorder's bound tenant
via the same helper file's `readTenantScopedBody` shim; mismatches
return 403, omissions are accepted (the recorder uses its own
implicitly).

Test fixtures that loaded `Conf` outside the production path were
updated to inject a sentinel `tenantId`: `tempConf` in
`internal/api/api_test.go`, `createTempFile` and a new `TestMain` in
`internal/conf/conf_test.go`, and `newInstance` in
`internal/core/core_test.go`. The last is a one-line test-helper
edit outside the trio originally scoped, surfaced explicitly in the
commit message.

### D2. `/v3/config/paths/*` exposed `conf.Path` directly

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` (canonical types + translation layer) and
Phase 2 commit `78de5d5f` (handlers, escape hatch, deletion of old
endpoints).

The MediaMTX-lineage `/v3/config/paths/*` surface is gone. Camera
configuration now lives at `/v1/cameras` returning canonical
`defs.Camera` (in `internal/defs/camera.go`), with the conf↔canonical
translation in `internal/defs/camera_translate.go`. Recording rules
were carved out into a separate canonical `RecordingPolicy` entity
exposed at `/v1/recording-policies`. The `conf.Path` shape still
lives internally as the recorder's persistence layer, but is no
longer exposed at the API boundary; the handlers in
`internal/api/api_v1_cameras.go` translate at the edge.

The four `conf.Path` content categories that did not fit cleanly
into canonical `Camera` were carved out into the recorder-localized
escape hatch per ADR 0009 §D6: operational/global config under
`/v1/recorder/config`, camera-creation defaults (with recording-
policy fields explicitly stripped) under
`/v1/recorder/camera-defaults`, the ~30-field RPi-camera sub-shape
under `/v1/recorder/cameras/{id}/source-config`, and the per-camera
shell hooks under `/v1/recorder/cameras/{id}/hooks`. Wire shapes for
those four are documented in `recorder/api/openapi.yaml` and live in
`internal/api/escape_hatch_shapes.go`,
`internal/api/api_v1_recorder_camera_source_config.go`, and
`internal/api/api_v1_recorder_camera_hooks.go`.

There is no deprecation window — the `/v3` surface is removed
outright per ADR 0009 §D1. The legacy handlers and tests were
deleted in commit `78de5d5f`.

### D3. `/v3/paths/{list,get}` MediaMTX vocabulary in runtime view

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` and Phase 2 commit `78de5d5f`.

The MediaMTX `Path` runtime aggregate is split per ADR 0009 §D5:
configuration lives on `Camera` (with the small `runtime` block per
§D3 of the ADR — `online`, `available`, `last_online_at`), and active
runtime sessions live on the new canonical `Stream` entity at
`/v1/streams`. The `Stream` entity (`internal/defs/stream.go`) folds
in 25 protocol-specific endpoints from the legacy /v3 surface — the
seven session/conn lists, the seven session/conn gets, the seven
session/conn kicks, plus the four RTSP/RTSPS conn endpoints — into
three canonical endpoints (`GET /v1/streams`, `GET /v1/streams/{id}`,
`DELETE /v1/streams/{id}`).

Per-protocol detail is preserved on the response in
`Stream.protocol_specific`, a discriminated union by `Stream.protocol`
covering RTSP/RTSPS, RTMP/RTMPS, SRT, WebRTC, and HLS variants
(`internal/defs/stream_protocol_specific.go`). The RTSP-only
conn-vs-session split is preserved in
`protocol_specific.transport_connections[]`. The unified handlers in
`internal/api/api_v1_streams.go` walk every protocol cluster, run the
Phase 1 translators, apply optional filters
(`protocol`, `camera_id`, `direction`, `state`), paginate, and redact
PII / Sensitive fields before responding.

`MediaMTX-vocabulary` enums (`PathSourceType` with its 15 values,
`PathReaderType` with its 10 values) are gone from the OpenAPI
spec; only canonical `StreamProtocol`, `StreamDirection`, and
`StreamState` remain.

### D7. String path-name identifiers replaced by UUIDs

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` and Phase 2 commit `78de5d5f`.

Every `/v1` route keys on a canonical UUID per ADR 0009 §D4. For
on-disk paths that already exist (which key by string-name in the
recorder's persistence layer), a deterministic UUIDv5 in the OID
namespace is derived from the path-name, giving a stable id across
recorder restarts (see `cameraIDFromPathName` in
`internal/api/api_v1_camera_id.go`). The recorder maintains a
forward (uuid → name) and reverse (name → uuid) lookup that the
handlers use to round-trip between canonical and persistence
identifiers.

The same scheme is applied to `RecordingSegment.id` (UUIDv5 from
`(camera_id, segment_path)`), `Recording.id` (UUIDv5 from
`(camera_id, started_at)`), and `StorageVolume.id` (UUIDv5 from the
absolute mount path). All identifiers are stable across recorder
restarts as long as the underlying tuple does not change.

Limitation: per ADR 0009 §D4, UUID provisioning is recorder-issued
in the pre-MS phase. Once the Management Server pairs and starts
issuing identifiers authoritatively, the recorder must re-key its
in-memory tables to match; that handoff is tracked separately.

### D8. `RecordingSegment` 2-field stub replaced by canonical 17-field shape

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` and Phase 2 commit `78de5d5f`.

The MediaMTX-lineage two-field `Recording` schema is gone. In its
place:

- `defs.RecordingSegment` (`internal/defs/recording_segment.go`)
  carries the full canonical 17 fields including the new
  `recording_id` link added by ADR 0009. The recorder-local on-disk
  `path` field is held internally but scrubbed from API responses
  (`scrubSegmentForResponse` in
  `internal/api/api_v1_recordings.go`).
- `defs.Recording` (`internal/defs/recording.go`) is a new canonical
  entity introduced inline by ADR 0009 — a continuous span of
  recorded footage on one camera, composed of one or more
  RecordingSegments. The list endpoint (`GET /v1/recordings`) omits
  the embedded segments array; the GET-by-id endpoint
  (`GET /v1/recordings/{id}`) includes it.
- `GET /v1/recordings/{id}/playback` returns a canonical
  `PlaybackHandle` (URL + token + expires_at).

The recorder synthesizes Recordings lazily on first
`/v1/recordings*` request by walking `recordstore` and grouping
contiguous segments by a 60-second gap heuristic
(`recordingGapThreshold`); the registry is in-memory and
re-synthesizes on recorder restart per ADR 0009 §D8 closure notes.

Limitations / Phase-2 follow-ups:

- The playback `token` returned today is a deterministic placeholder
  (`placeholder-<recording_id>`); real bearer issuance requires
  touching `internal/playback`, which was off-limits in Phase 2C.
  Tracked as a Phase-2 follow-up.
- `RecordingSegment.ended_at` is left zero pre-Phase-2-followup;
  `recordstore`'s public surface doesn't expose per-segment end
  timestamps, and using the next segment's `started_at` as a
  stand-in would collapse the gap-detection heuristic.
- `RecordingSegment.volume_id`, `policy_id`, `recording_server_id`,
  and `site_id` are emitted as empty strings pre-MS; storage volumes
  Phase 2D and policies Phase 2A wire them up, but the cross-tier
  pairing to MS-issued identifiers waits for ADR 0002.

### D9. `user` free-form string replaced by `user_id` UUID

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` and Phase 2 commit `78de5d5f`.

The canonical `Stream` entity carries `user_id: ref<User>?`
(UUID-formatted) per ADR 0009 §"Stream amendments"; the legacy
free-form `user` login-string field is gone from the /v1 surface
along with its parent per-protocol session/conn endpoints (D3).
Stream translators (`internal/defs/stream_translate.go`) populate
`user_id` when an authenticated user is associated with the
session.

Limitation: pre-MS the recorder doesn't have a canonical `User`
catalog wired up yet — `user_id` is populated when present but the
upstream sources don't always have one to give. Cross-tier joins
to identity remain a Phase-2 follow-up tied to ADR 0002 OQ10.

### D10. `path` string replaced by `camera_id` UUID on streams

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` and Phase 2 commit `78de5d5f`.

Every canonical `Stream` carries `camera_id: ref<Camera>` per
ADR 0009 §D2. The translators in
`internal/defs/stream_translate.go` map MediaMTX session/conn
path-name strings to canonical Camera UUIDs via the same
deterministic UUIDv5-from-path-name scheme used for
`Camera.id` (D7). Same approach for `RecordingSegment.camera_id`,
`Recording.camera_id`, and Event subject references.

The legacy `path` string field is gone from all /v1 response
shapes. The HLS muxer escape-hatch endpoint
(`/v1/recorder/hls-muxers/{id}`) keys on canonical Camera UUID and
internally resolves it back to the recorder-internal path-name to
call into the existing HLS server.

### D13. Restricted-classification upgrade path documented

**Resolved 2026-04-26.** Closed by the ADR 0009 implementation —
Phase 1 commit `9c7bac3e` and Phase 2 commit `78de5d5f`.

ADR 0009 lands the canonical `Camera` and `Site` references
(through `Camera.tenant_id` / `Camera.site_id`); per the inline
canonical model amendments in §"Canonical model amendments" the
recorder now models the Restricted-upgrade fields the canonical
data-classification rules call for:

- `Camera` carries `position` (Sensitive → Restricted at
  residential precision) per `domain-model.md`. The Phase 1
  `defs.Camera` shape does not yet expose `position` directly (the
  recorder doesn't compute camera position today), but the field
  has a documented home in canonical `Camera` and lands when the MS
  starts issuing it. The OpenAPI spec annotates `Camera.name` and
  `Camera.source_url` as Sensitive, which is the upgrade-eligible
  classification per `data-classification.md`.
- `Site.coordinates` and `Site.address` upgrade rules are now
  documented in `data-classification.md`; the recorder doesn't
  surface Site directly (Site is MS-owned), but the per-recorder
  `tenant_id` + `site_id` references land on every canonical
  entity the recorder emits.

Limitations / follow-ups:

- The full `Camera.position` field arrives at the recorder when MS
  pairing lands per ADR 0002; the recorder will translate it
  through unchanged. No additional code is needed in the recorder
  for the Restricted upgrade because the recorder is not the
  authoritative source for that data.
- The OpenAPI spec's `x-classification` annotations follow the
  conventions described in the spec's `info.description`:
  Operational is the default and stays unannotated; PII / Sensitive
  / Credential get explicit annotations. Restricted has no
  in-tree fields today; when one arrives the annotation set will
  extend.
