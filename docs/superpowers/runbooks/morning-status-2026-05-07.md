# Overnight Foundation Build — Morning Status Report

**Generated:** 2026-05-07 ~05:45 local
**Branch:** `consumer-foundation` in worktree `../NVR-consumer-foundation`
**Spec:** `docs/superpowers/specs/2026-05-07-consumer-nvr-foundation-design.md`
**Plan:** `docs/superpowers/plans/2026-05-07-consumer-nvr-foundation.md`

## TL;DR

**Foundation Phases 1–6 are COMPLETE and the binary boots cleanly.** That's the schema, all 21 DAOs, all 9 domain packages, MS code removal, the full new API surface, and the bootstrap/integration wiring. The Go SPA build (Phase 7) and Cypress E2E (Phase 8) did not run overnight — they're scoped, planned, and ready for you to dispatch (or do by hand) when you're up.

A live smoke test confirmed the binary boots, generates the identity dir, seeds the bootstrap admin, prints the initial password, opens HTTPS+RTSP+RTMP+HLS+WebRTC+SRT listeners, and serves anonymous endpoints (`/v1/system/info`, `/v1/system/setup-status`) correctly.

## Status by phase

| # | Phase | Status | Tag |
|---|---|---|---|
| 0 | Worktree + branch | DONE | — |
| 1 | Schema migrations (0003–0023) | DONE | — |
| 2 | DAOs (21 repos) | DONE | `phase-2-dao-complete` |
| 3 | Domain packages (9 packages) | DONE | `phase-3-domain-complete` |
| 4 | MS code removal (~31 files, ~10k lines) | DONE | `phase-4-ms-removal-complete` |
| 5 | API handlers (10 task families) | DONE | `phase-5-api-complete` |
| 6 | Bootstrap + integration wiring | DONE | `phase-6-bootstrap-complete` |
| 7 | Embedded SPA updates + `make web` | NOT STARTED | — |
| 8 | Cypress E2E + final acceptance | PARTIAL (runbook only) | — |

## What works right now

A live boot of the binary (positional `confpath` argument, not `--confpath`) produces:

```text
RECORDER BOOTSTRAP ADMIN CREATED
  username:        admin
  initial password: <24-char random>
  must_change_password: true (forced rotation on first login)
  also written to: <identityDir>/initial-admin-password.txt (mode 0600)
```

```text
[API] listener opened on :9997 (TCP/HTTPS)
[RTSP] listener opened on :8554 (TCP/RTSP), :8000 (UDP/RTP), :8001 (UDP/RTCP)
[RTMP] listener opened on :1935 (TCP/RTMP)
[HLS] listener opened on :8888 (TCP/HTTP)
[WebRTC] listener opened on :8889 (TCP/HTTP), :8189 (UDP/ICE)
[SRT] listener opened on :8890 (UDP)
```

Anonymous endpoints respond:

```bash
curl -sk https://localhost:9997/v1/system/info
# {"recorder_id":"019e020a-160c-7b23-92a3-3a6a26bc7dc2","version":"v0.0.0"}

curl -sk https://localhost:9997/v1/system/setup-status
# {"setup_required":true}
```

Identity dir is fully populated:

```text
cred.key                       (32B, mode 0600)
id                             (UUIDv7-ish, 37B)
initial-admin-password.txt     (mode 0600, removed on first password change)
recorder-local-jwt.key         (Ed25519 private)
recorder-local-jwt.kid         (key id)
recorder.db                    (SQLite DB, 21+ migrations applied)
recorder.db-shm + db-wal       (WAL mode)
recorder.key + recorder.pub    (ECDSA P-256, identity keypair)
tls.crt + tls.key              (self-signed; SANs: hostname, <id>.local, localhost, NIC IPs)
```

All 13 foundation packages pass tests:

```text
ok    internal/store          (21 repos × table-driven tests)
ok    internal/cameracred     (vault round-trip + key 0600 perms)
ok    internal/rbac           (granular permissions, fail-closed)
ok    internal/schedule       (wrap-midnight, union, DST docs)
ok    internal/outbox         (claim/dispatch/mark, exp backoff w/ jitter)
ok    internal/events         (Service, in-process Bus, expires_at materialization)
ok    internal/notifications  (HMAC webhook, SMTP, dispatcher)
ok    internal/cameras        (Service, Bus, PathBridge w/ vault integration)
ok    internal/retention      (segments, events, clips sweepers)
ok    internal/cloudbridge    (nop processor + horizon sweeper)
ok    internal/identity       (jwt + cred + self-signed TLS gen)
ok    internal/audit          (per-domain emitter + redaction)
ok    internal/bootstrap      (admin seeding, env-var override)
```

## Database schema (21 new migrations)

Applied via `pressly/goose` on every `store.Open`. New tables:

```text
roles                      camera_groups          cameras
camera_credentials         camera_capabilities    camera_health
recording_policies         recording_schedules    event_types
event_retention            events                 event_snapshots
clips                      clip_segments          notification_targets
notification_subscriptions notification_outbox    audit_log
cloud_outbox               system_settings        (existing: local_users, onvif_subscriptions extended)
```

Plus seeded data: 2 roles (admin, viewer), 12 well-known event types, default 7-day event retention with 90-day overrides for motion/doorbell/tamper, default `policy_default` recording policy with 14-day retention.

## API surface

Anonymous: `/v1/auth/login`, `/v1/system/info`, `/v1/system/setup-status`, `/v1/health` (auth-gated but permission-free).

Authenticated (granular `requirePermission("…")` strings, satisfied by either admin or viewer per `internal/rbac/perms.go`):

| Family | Endpoints |
|---|---|
| auth | `POST /v1/auth/{login,logout,password}`, `GET /v1/auth/me` |
| cameras | `GET/POST/PATCH/DELETE /v1/cameras[/:id]`, `PUT /v1/cameras/:id/credentials`, `POST /v1/cameras/:id/probe`, `GET /v1/cameras/:id/health`, `GET /v1/cameras/:id/snapshot` |
| camera-groups | `GET/POST/PATCH/DELETE /v1/camera-groups[/:id]` |
| recording-policies | `GET/POST/PATCH/DELETE /v1/recording-policies[/:id]`, `GET/PUT /v1/recording-policies/:id/schedules`, `GET /v1/cameras/:id/recording-state` |
| events | `GET /v1/events[/:id]`, `POST /v1/events/:id/acknowledge`, `GET /v1/events/stream` (SSE), `GET/POST/PATCH /v1/event-types[/:id]`, `GET/PUT /v1/event-retention[/:type]` |
| notifications | `GET/POST/PATCH/DELETE /v1/notification-targets[/:id]`, `POST /v1/notification-targets/:id/test`, `GET/POST/DELETE /v1/notification-subscriptions[/:id]`, `GET /v1/notification-outbox`, `POST /v1/notification-outbox/:id/retry` |
| system | `GET/PATCH /v1/system/settings`, `PUT /v1/system/tls`, `POST /v1/system/retention/sweep` |
| audit | `GET /v1/audit[/:id]`, `POST /v1/audit/export`, `POST /v1/audit/purge` |
| users | `GET/POST/PATCH/DELETE /v1/users[/:id]`, `POST /v1/users/:id/role`, `POST /v1/users/:id/password` |

Existing `/v1/recordings`, `/v1/recording-segments`, `/v1/clips`, `/v1/streams`, `/v1/storage-volumes`, ONVIF, diagnostics — all preserved.

## Things you should know about

1. **Build requires `go generate` first.** `internal/core/VERSION` and `internal/servers/hls/hls.min.js` are gitignored generated artifacts:
   ```bash
   cd /Users/ethanflower/raikada-consumer/NVR-consumer-foundation
   go generate ./internal/core/... ./internal/servers/hls/...
   go build ./...
   ```

2. **The CLI uses positional `confpath`, not `--confpath`.** The plan's smoke commands incorrectly used `--confpath`; the runbook is now correct.

3. **Pre-existing flaky tests on macOS.** `internal/core` has a couple of port-collision-flaky tests (`TestPathOverridePublisher/disabled` and similar) that fail when run with the full suite but pass when run individually. Confirmed pre-existing — they failed on `phase-3-domain-complete` and earlier tags too. Phase 4 agent's report documented this.

4. **`/v1/health` is permission-free but still requires authentication.** That's intentional (matches existing convention). If you want a truly anonymous liveness endpoint, see comment at `internal/api/api.go:176`.

5. **The granular permission strings deviated from the plan's `Read/Write` two-permission scheme.** Phase 3 agent followed the existing `camera.list`, `camera.read`, `camera.create`, etc. convention. RoleAdmin grants all; RoleViewer grants the `*.list`, `*.read`, `event.stream`, `clip.download`, `recording.playback` subset. See `internal/rbac/perms.go`.

6. **No real-camera tests were run.** I had no access to your network or camera credentials. Use `docs/superpowers/runbooks/real-camera-smoke.md` to verify against your LAN cameras.

## What didn't happen overnight

### Phase 7 — SPA updates + `make web` (NOT STARTED)

Why: The SPA work is substantial (~50 files in `web/src/`), needs `make web` (Docker isolated, ~5-10min), and would have run past my time budget. The existing SPA at HEAD has full Login + PasswordChange + SetupWizard + Cameras/Policies/etc. routes — the foundation API mostly works against it, but the Pairing route and paired-state machinery should be removed, and new routes added for Users / Notification Targets / Notification Subscriptions / Schedule Editor.

Plan section 7 has the per-task breakdown. To dispatch:

```text
You are implementing Phase 7 (SPA updates) in /Users/ethanflower/raikada-consumer/NVR-consumer-foundation
on branch consumer-foundation.

Read docs/superpowers/plans/2026-05-07-consumer-nvr-foundation.md Phase 7 (Tasks 7.1–7.8).

Tasks: inventory existing SPA, delete Pairing route + paired-state from App.tsx,
add Users page (admin only), add Notifications targets/subscriptions pages,
add Schedule editor (weekly grid), refresh embed via `make web`.

Constraint: every SPA change must be followed by `make web` and a commit
that includes the resulting `internal/web/dist/` diff.

Work autonomously. Commit per the message at the bottom of each task body.
```

### Phase 8 — Cypress + final acceptance (PARTIAL)

Done: real-camera smoke runbook at `docs/superpowers/runbooks/real-camera-smoke.md`.

Not done: Cypress scaffolding (Tasks 8.1–8.4) — no point writing UI test specs against a SPA that hasn't been updated yet. Once Phase 7 lands, dispatch:

```text
You are implementing Phase 8 (Cypress E2E) in /Users/ethanflower/raikada-consumer/NVR-consumer-foundation
on branch consumer-foundation.

Read docs/superpowers/plans/2026-05-07-consumer-nvr-foundation.md Phase 8 (Tasks 8.1–8.6).

Add cypress + @testing-library/cypress dev deps to web/package.json.
Write specs: setup-wizard.cy.ts, cameras.cy.ts (mocked source), notifications.cy.ts (mocked target).
Provide an `npm run cy:run` script. Do NOT attempt to drive against real cameras.

The real-camera smoke procedure for the user is already at
docs/superpowers/runbooks/real-camera-smoke.md.

Final task: tag v1.0.0-foundation and prepare PR description.
```

## Recommended next moves on wake

1. **`go generate ./internal/...` then `go build ./...` and verify it still builds.** (One-time setup after pulling the branch.)
2. **Boot smoke** with the runbook config:
   ```bash
   cd /Users/ethanflower/raikada-consumer/NVR-consumer-foundation
   /tmp/raikada-test-identity=$(mktemp -d)
   cat > /tmp/raikada-test.yml <<EOF
   identityDir: $/tmp/raikada-test-identity
   logLevel: info
   api: yes
   apiAddress: :9997
   apiEncryption: yes
   rtspAddress: :8554
   mdns: false
   EOF
   go run . /tmp/raikada-test.yml
   ```
   In another terminal, `curl -sk https://localhost:9997/v1/system/setup-status` → `{"setup_required":true}`.

3. **Real-camera smoke** per `docs/superpowers/runbooks/real-camera-smoke.md`. This is the bit I genuinely couldn't do without your network.

4. **Dispatch Phase 7 + Phase 8** (prompts above) if you want to land the SPA + Cypress work. Or do them by hand — the existing SPA already has Login + SetupWizard + Cameras/Policies routes wired against the API, so the diff is smaller than starting from scratch.

5. **Open the PR** when satisfied:
   ```bash
   git push -u origin consumer-foundation
   gh pr create --title "Consumer NVR foundation (sub-project 1 of 4)" \
                --body-file docs/superpowers/specs/2026-05-07-consumer-nvr-foundation-design.md
   ```

## Sub-projects 2, 3, 4 (NOT started)

These are the foundation's downstream features. Each gets its own brainstorm → spec → plan → impl cycle. The foundation's tables (`camera_capabilities`, `camera_health`, `events`, `event_snapshots`, `clips`, `clip_segments`) and packages (`internal/cameras/`, `internal/events/`) are the load-bearing seams.

- **SP2 (camera lifecycle):** discovery, pairing, capability probe, health monitoring, firmware tracking, camera-side mirroring. I'd recommend brainstorming this first since it unblocks 3 and 4.
- **SP3 (events + vendor channels):** ONVIF Base subscription, Amcrest CGI / Hikvision ISAPI / Reolink adapters, supervised reconnection. Wires vendor adapters into `events.Service.Insert`.
- **SP4 (snapshots + event-to-clip):** on-event JPEG fetch, thumbnails, lazy clip extraction, signed clip URLs, pre/post-roll stitching at clip export time.

## What I deliberately did NOT do

- **No real-camera tests.** No LAN access, no camera credentials. Runbook exists for you to run.
- **No `gh pr create` or `git push`.** Branch is local-only on `consumer-foundation`.
- **No edits to `main` after the foundational spec + initial plan commit.** All foundation work landed on `consumer-foundation`.
- **No deletion of the original `NVR/` repo.** Your enterprise tree is untouched on `main`.
- **No SPA updates.** Phase 7 not run; existing SPA still has the pairing UI + paired-state machinery, which now points at deleted endpoints. The SPA will partially work (Login, Setup, Cameras, Policies) but Pairing and the management-server status will throw on those routes until Phase 7 cleans them up.

## Commit summary

44 commits on `consumer-foundation` since branching from `main` (`668faaf0`).

```text
phase-1: 1 commit (migrations)
phase-2: 21 commits (one per DAO)
phase-3: 9 commits (one per package)
phase-4: 7 commits (deletes + trims)
phase-5: 10 commits (one per handler family)
phase-6: 8 commits (identity, bootstrap, settings, core wire, TLS reload, mDNS, audit)
docs/runbooks: 2 commits
```

Plus the 2 doc commits before the branch (`668faaf0` spec, `0572b63b` initial plan, `823949f6` plan completion).

---

Run when you wake; let me know how the real-camera smoke goes. The plan + runbook should be enough to drive Phase 7 + 8 to completion in one focused session.
