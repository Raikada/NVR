# Recorder API (Implementation Notes)

How the Recording Server exposes its public surface: the HTTP / gRPC /
streaming endpoints it serves, their authentication expectations, and
the local-implementation details that the platform-level contracts do
not cover.

> **Wire-format contracts are platform-wide.** Once API contracts are
> agreed and written, they will live under
> [`../../docs/api-contracts/`](../../docs/api-contracts/), not here.
> This document is for the recorder-specific implementation view —
> handler organization, request lifecycle, error mapping, etc.

For platform-level context on which surfaces the recorder exposes and
to whom, see
[`../../docs/service-boundaries.md`](../../docs/service-boundaries.md)
§3 and [`../../docs/authentication-flows.md`](../../docs/authentication-flows.md).

---

## Authoritative spec

The HTTP API surface is described authoritatively by the OpenAPI 3.0
document at [`../api/openapi.yaml`](../api/openapi.yaml). Every
endpoint, request/response shape, and security scheme the recorder
exposes today lives there, with `x-classification` annotations on
PII and Sensitive fields and pointers at
[`canonical-divergences.md`](canonical-divergences.md) for every
place the implementation diverges from the canonical domain model.
This document is the recorder-side overview; the spec is the contract.

## Status

> **Scaffold.** This document is a placeholder for the recorder-side
> implementation view of the API — handler organization, request
> lifecycle, error mapping, performance notes. The wire-shape
> contract lives in [`../api/openapi.yaml`](../api/openapi.yaml) and
> is already populated; cross-tier contracts will land under
> `../../docs/api-contracts/` as they are agreed.

## Hard rules

- **Do not change a public surface without updating the corresponding
  platform doc** (canonical entity, service boundary, or API contract).
  See [`../AGENTS.md`](../AGENTS.md) §3.
- The recorder validates inbound tokens locally against cached issuer
  material. It does not call Cloud or the MS per request. See
  [`../../docs/adr/0002-trust-pairing-authentication.md`](../../docs/adr/0002-trust-pairing-authentication.md)
  OQ10 required property 1.
- Endpoints default to authenticated access. Anonymous access is opt-in
  per path, never the default.
- Recorder-local paths (e.g. `path` on `RecordingSegment`) **must
  never** appear in client-facing responses.

## Topics planned for this document

- Surface map: which endpoints exist, who calls them (MS / web /
  Flutter / broker), and which canonical entities they expose.
- Auth integration: how token validation hooks into request handlers,
  and how cached issuer material is refreshed.
- Error mapping: how internal errors translate to HTTP / gRPC status
  codes consistently across handlers.
- Streaming endpoints: live and playback delivery (RTSP, RTMP, WebRTC,
  HLS, SRT) and how they interact with the auth layer.
- Pagination, filtering, sorting: conventions where they apply.
- Versioning: how non-additive contract changes are rolled out
  (header, path, or content negotiation — TBD with the platform API
  contracts).
