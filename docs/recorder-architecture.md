# Recorder Architecture (Implementation)

This document describes the **internal architecture** of the Recording
Server: process model, package layout, threading, hot-paths, and the
boundary between MediaMTX-inherited code and Raikada-specific code.

For higher-level orientation, see:

- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — what this repo is and is not.
- [`../../platform/docs/system-blueprint.md`](../../platform/docs/system-blueprint.md) — the
  five-tier platform picture.
- [`../../platform/docs/service-boundaries.md`](../../platform/docs/service-boundaries.md) —
  what the recorder owns, platform-wide.

---

## Status

> **Scaffold.** This document is a placeholder for the deeper internal
> architecture treatment. Content will land as the implementation
> stabilizes. Until then, treat the package layout under `internal/` and
> the high-level summary in [`../ARCHITECTURE.md`](../ARCHITECTURE.md) as
> authoritative.

## Topics planned for this document

- Process model: single binary, supervisor goroutines, signal handling.
- Package layout: where MediaMTX boundaries are vs. where Raikada code
  lives, and the rules for crossing them.
- Hot-path vs. control-path separation: media frames vs. configuration
  reconciliation, audit emission, telemetry.
- Concurrency model: per-stream goroutines, locking discipline, channel
  conventions.
- Failure isolation: how a single misbehaving camera or volume is
  prevented from taking down the recorder.
- Startup sequence: bootstrap → identity load → MS reconnect → config
  apply → ingest → playback.
- Graceful shutdown: in-flight segment seal, telemetry flush, identity
  save.
