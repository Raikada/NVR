<h1 align="center">Raikada Recording Server</h1>

The **Raikada Recording Server** is the on-premises component of the
Raikada platform. It runs on a customer-LAN appliance, ingests media
from cameras (RTSP, RTMP, WebRTC, SRT, HLS), records to local storage as
bounded segments, serves live and historical streams to authorized
clients, and prepares clip exports.

This repository is a Raikada-specific fork of
[MediaMTX](https://github.com/bluenviron/mediamtx); upstream remains the
load-bearing engine for the media pipeline, but this fork is a separate
product and is not merge-friendly.

## Offline-first invariant

The recorder must continue recording when disconnected from Cloud or
the Management Server. Every interaction with those tiers is
asynchronous, retryable, and off the recording hot path. Footage loss
is the worst outcome the system can produce, and the architecture is
optimized to prevent it.

## Where to start

- [`AGENTS.md`](AGENTS.md) — rules for AI agents working in this repo.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — what this repo is and is not.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — how to make changes.
- [`SECURITY.md`](SECURITY.md) — reporting vulnerabilities.

This repo is one part of the Raikada platform. Platform-wide truth
lives in the parent workspace:

- [`../platform/CLAUDE.md`](../platform/CLAUDE.md) — workspace-level orientation and rules.
- [`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) — the
  five-tier architecture.
- [`../platform/docs/domain-model.md`](../platform/docs/domain-model.md) — canonical
  entities. Do not redefine these locally.
- [`../platform/docs/adr/`](../platform/docs/adr/) — accepted cross-system decisions.

## Build and test

```sh
make help              # full list of targets
make test-nodocker     # run tests locally
make lint              # run linters (Docker-based)
make binaries          # build release binaries
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full workflow.

## License

See [`LICENSE`](LICENSE).
