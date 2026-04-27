# Recorder Configuration

How the Recording Server is configured: bootstrap config, runtime
configuration pushed from the Management Server, last-known-good
behavior, and the precedence rules between sources.

The platform-level configuration model lives in
[`../../platform/docs/service-boundaries.md`](../../platform/docs/service-boundaries.md)
and the entities involved (`Camera`, `RecordingPolicy`, `StorageVolume`,
`Role`, `LicenseEntitlement`) are defined in
[`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md). This document
is the recorder-specific implementation view.

---

## Status

> **Scaffold.** Content will land as configuration handling solidifies.
> The high-level precedence order is already authoritative — see
> [`../ARCHITECTURE.md`](../ARCHITECTURE.md) §8.

## Hard rules

- The recorder operates from its **last-known-good** MS-issued
  configuration when the Management Server is unreachable. It does not
  block on an MS round-trip on the recording hot path.
- The recorder never accepts runtime configuration directly from a web
  or Flutter client. Clients ask the Management Server.
- Configuration must round-trip the canonical entity shapes unchanged
  (see [`../../platform/docs/domain-model.md`](../../platform/docs/domain-model.md)).

## Topics planned for this document

- Bootstrap config: what `mediamtx.yml` (or its successor) contains, and
  why those fields are bootstrap-only.
- Runtime config: how the MS pushes camera assignments, recording
  policies, role/signing-key cache, and license-derived feature flags.
- Local persistence: where the last-known-good snapshot is stored and
  how it is encrypted.
- Reconciliation: how the recorder applies an MS-pushed config change
  without disrupting in-flight recording.
- Drift detection: what happens if the local snapshot disagrees with
  the MS on reconnect.
