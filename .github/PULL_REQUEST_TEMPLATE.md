<!--
Thanks for contributing to the Raikada Recording Server. Before opening
this PR, please read AGENTS.md and CONTRIBUTING.md at the repo root, and
CLAUDE.md / docs/ at the workspace root if your change touches anything
cross-system.

Keep PRs small and focused: one concern per PR.
-->

## Summary

<!-- One or two sentences. What does this change do, and why? -->

## Scope

- **Files touched:**
  <!-- list, or "see diff" for trivial PRs -->
- **Files explicitly NOT touched:**
  <!-- if the task could plausibly have spread further, name what you
       deliberately left alone. Drive-by edits balloon review burden. -->
- **Why this scope is correct:**
  <!-- one sentence -->

## Domain-model impact

- [ ] No change to any public API shape (HTTP / gRPC / WebSocket /
      message-queue / on-disk schema / config consumed by deployment
      automation).
- [ ] **OR** this PR changes a public shape, and the platform doc
      `../platform/docs/domain-model.md` (and a new ADR under `../platform/docs/adr/` if
      non-additive) is updated in this same PR. Doc commits referenced:
      <!-- e.g. "see commit abc1234 / docs: add Foo to domain-model" -->

## AGENTS.md checklist

- [ ] Read the parent workspace docs (`../platform/CLAUDE.md`,
      `../platform/docs/system-blueprint.md`, `../platform/docs/domain-model.md`,
      `../platform/docs/service-boundaries.md`) before starting?
- [ ] Does this change introduce a new entity or DTO? If so, is the
      canonical shape in `../platform/docs/domain-model.md` (or updated in
      this PR)?
- [ ] Does this change a public API shape? If so, is the platform doc
      updated?
- [ ] Does this touch the media pipeline? If so, was that explicitly
      requested?
- [ ] Does this change auth, pairing, identity, or trust? If so, are
      the relevant parent docs (`trust-model.md`, `pairing-flows.md`,
      `authentication-flows.md`, `domain-model.md`,
      `service-boundaries.md`) updated in this PR?
- [ ] Are there unrelated edits in the diff? Remove them.
- [ ] Are there placeholder values, fake data, or `TODO` shortcuts in
      code paths that will run in production? Remove them or gate them.
- [ ] Does the recorder still record when Cloud is unreachable?

## Test plan

<!-- How was this tested? Include `make test-nodocker` / `make lint`
results, manual reproduction steps, or "N/A — docs-only" if applicable. -->
