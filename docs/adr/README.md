# Recorder-specific ADRs

Platform-wide ADRs (decisions binding Cloud, Management Server,
Recording Server, Web Client, and Flutter Client together) live at
[`../../../docs/adr/`](../../../docs/adr/). That is the right place for
any decision whose consequences cross system boundaries.

This directory is reserved for **recorder-only** ADRs: decisions whose
scope is confined to this repository (e.g. an internal package layout,
a recorder-internal performance trade-off, a choice about which Go
library to use for a recorder-local concern that has no contract
implications upstream).

There are none yet.

## Adding a recorder-only ADR

1. Confirm the decision is genuinely repo-local. If it touches a
   canonical entity, a public API surface, or another tier, it belongs
   in [`../../../docs/adr/`](../../../docs/adr/) instead.
2. Pick the next free number (`NNNN-<short-kebab-title>.md`).
3. Copy the template from
   [`../../../docs/adr/0000-template.md`](../../../docs/adr/0000-template.md)
   and adapt it.
4. Add an entry to a future index here (when there is more than one).
