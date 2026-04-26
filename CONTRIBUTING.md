# Contributing to the Raikada Recording Server

This document is for humans (and AI agents) making changes to **this
repository**. Workspace-wide rules live one level up — see
[`../platform/CLAUDE.md`](../platform/CLAUDE.md). Repo-specific rules live in
[`AGENTS.md`](AGENTS.md).

If you've never worked in this workspace before, read in this order:

1. [`../platform/CLAUDE.md`](../platform/CLAUDE.md) — workspace-level orientation.
2. [`../platform/docs/system-blueprint.md`](../platform/docs/system-blueprint.md) — the
   five-tier architecture.
3. [`../platform/docs/domain-model.md`](../platform/docs/domain-model.md) — canonical
   entities. Do not redefine these locally.
4. [`../platform/docs/service-boundaries.md`](../platform/docs/service-boundaries.md) —
   what every tier owns.
5. [`ARCHITECTURE.md`](ARCHITECTURE.md) — what this repo specifically does.
6. [`AGENTS.md`](AGENTS.md) — repo rules.

---

## Where things live

| What                                         | Where                              |
|----------------------------------------------|------------------------------------|
| Platform-wide truth (canonical entities,     | `../platform/docs/`                         |
| ownership, trust, pairing, auth, ADRs,       |                                    |
| API contracts)                               |                                    |
| Repo-level orientation                       | `ARCHITECTURE.md`                  |
| Repo agent rules                             | `AGENTS.md`                        |
| Recorder implementation docs                 | `docs/recorder-*.md`               |
| Recorder-specific ADRs                       | `docs/adr/`                        |
| Inherited MediaMTX user-facing docs          | `docs/1-kickoff/`, etc.            |
| Go source                                    | `internal/`, `api/`, `main.go`     |
| Configuration                                | `mediamtx.yml` (and its successor) |

When you change recorder behavior:

- Touching a canonical entity? → Edit `../platform/docs/domain-model.md` first.
- Changing ownership of a concern? → Edit `../platform/docs/service-boundaries.md`.
- Touching auth / pairing / trust? → Edit the relevant `../platform/docs/*.md`
  before the code.
- Touching media pipeline? → Read `AGENTS.md` §6 first; you probably
  don't want to.
- Changing a wire contract? → Edit `../platform/docs/api-contracts/` (once
  populated) before the wire change.

---

## Build and test

This repository inherits MediaMTX's build tooling. Run `make help` to see
the full list of targets. Most targets are Docker-based by default so the
toolchain version is pinned; local equivalents exist where useful.

```sh
make help              # list available targets

# tests
make test              # full test suite (Docker-based)
make test-nodocker     # same tests, locally; runs test-internal + test-core
make test-32           # tests on a 32-bit system (Docker-based)
make test-e2e          # end-to-end tests

# lint and format (both Docker-based)
make lint              # runs all eight lint-* sub-targets
make format            # runs gofumpt + prettier on docs

# binaries
make binaries          # builds release binaries for all supported platforms
```

A change is not ready for review until `make test` (or `make test-nodocker`
locally) and `make lint` pass on a clean checkout.

---

## Pull requests

- Keep PRs small and focused. One concern per PR.
- Update docs in the same PR as the code. If your PR has no doc updates
  but changes a public surface, that is a sign you need to update docs.
- Run the [`AGENTS.md`](AGENTS.md) "Quick checklist before opening a PR"
  before submitting.
- Reviewers will check both the code and the docs. A code change that
  contradicts the docs is rejected; reconcile both.

---

## Style

- Go: standard library first, idiomatic Go. Format with `gofumpt` (run
  `make format-go` or `make format`).
- Comments: only where the *why* is non-obvious. Default to no comment.
- Commits: present tense, imperative; follow the `area: short summary`
  convention used in `git log` (e.g. `metrics: ...`, `hls: ...`,
  `docs: ...`); body explains the *why*.
- Branches: short, descriptive (`feat/clip-export`, `fix/segment-rotation`).

---

## Asking for help

If a task seems to require breaking workspace rules (duplicating an
entity, redefining ownership, changing a contract without docs), open a
discussion before writing code. The cost of a clarifying question is
much lower than the cost of an unwanted change.
