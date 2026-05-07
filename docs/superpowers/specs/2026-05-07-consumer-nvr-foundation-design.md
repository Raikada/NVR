---
title: Consumer NVR — Foundation Design
date: 2026-05-07
status: Approved
scope: Sub-project 1 of 4 (the consumer NVR fork)
related:
  - sub-project 2: camera lifecycle (separate spec)
  - sub-project 3: events & vendor channels (separate spec)
  - sub-project 4: snapshots + event-to-clip linkage (separate spec)
---

# Consumer NVR — Foundation Design

## Summary

Adapt the existing Raikada enterprise Recording Server (a MediaMTX fork in
`internal/`) into a **standalone, single-tenant, single-site** NVR appliance
targeting SMB and residential customers. This spec covers the **foundation**
sub-project: stripping out the Management Server (MS) integration, expanding
the local SQLite store into the canonical source of truth for cameras /
policies / users / events, adding role-based access control, bring-your-own
TLS, simple weekly recording schedules, a webhook + SMTP notification
pipeline, an append-only audit log, and a cloud-outbox seam that the future
multi-site cloud product will plug into.

Three sibling sub-projects (camera lifecycle; event subscriptions and vendor
channels; snapshots and event-to-clip linkage) build on top of this
foundation and ship as their own spec → plan → implementation cycles.

## Goals

- **Standalone operation.** One binary on customer premises serves cameras at
  one physical location with no Management Server, no cloud dependency, and
  no external identity provider in v1.
- **DB-canonical configuration.** Cameras, recording policies, schedules,
  users, roles, events, clips, retention, notifications, and audit live in
  SQLite. YAML keeps only bootstrap (TLS paths, listen addresses, identity
  dir, log level, db path).
- **Operator-friendly bootstrap.** Self-signed TLS by default, mDNS
  discoverability, first-run wizard with auto-generated initial admin
  password, headless install via env var.
- **Future-cloud-ready.** A cloud-outbox table accumulates events, health,
  and audit transitions; processor ships disabled. Plugging in the future
  multi-site cloud product is a config flip, not a refactor.
- **Preserve the media pipeline.** The MediaMTX-derived recorder, recordstore,
  playback, RTSP/RTMP/WebRTC/HLS/SRT servers stay unchanged on the hot path.

## Non-goals

- **Multi-tenant or multi-site shape.** One device = one customer at one
  location; multi-site rollup is a separate cloud product.
- **Camera discovery, pairing flows, capability probe, health collector,
  firmware tracking, and camera-side config mirroring.** All deferred to
  Sub-project 2.
- **Vendor event-channel adapters** (Amcrest CGI, Hikvision ISAPI, Reolink),
  ONVIF Base subscription, and the canonical CameraEvent normalizer beyond
  the storage shape. Deferred to Sub-project 3.
- **On-event snapshot fetching, thumbnail generation, lazy clip extraction,
  signed clip URLs, pre/post-roll clip stitching.** Deferred to Sub-project 4.
- **ACME / Let's Encrypt automation, TPM / OS-keyring credential
  protection, mobile push notifications.** Hooks reserved; implementations
  not in v1.

## Locked-in decisions

| # | Decision | Source |
|---|---|---|
| D1 | Surgical removal of MS code in this repo (`raikada-consumer/NVR/`); keep ONVIF, mDNS, motion, recorder pipeline, SPA, store. | Brainstorm Q1 |
| D2 | Single tenant, single site. Drop `TenantID` and `SiteID` from the schema. Multi-site is a separate cloud product. | Brainstorm Q2 |
| D3 | Two roles only: Admin, Viewer. Role→permission map in code; promote to DB later if custom roles are needed. | Brainstorm Q3 |
| D4 | Recording schedules are time-of-day windows per day-of-week; site-level timezone. | Brainstorm Q4 |
| D5 | Notifications: outbound webhook (HMAC-signed) + optional SMTP. Native push is the future cloud product's job. | Brainstorm Q5 |
| D6 | Retention: per-policy duration governs segments; per-event-type duration governs events / event-attached clips. | Brainstorm Q6 |
| D7 | Big-bang rewrite (Approach C) on a feature branch / worktree, not a phased landing. Reviewed and merged as a single milestone. | Brainstorm Q7 |

## Architecture overview

```text
                           ┌──────────────────────────────────────┐
                           │           Web SPA (existing)         │
                           └────────────────┬─────────────────────┘
                                            │ HTTPS (BYO TLS or self-signed)
                                            ▼
                  ┌──────────────────────────────────────────────────┐
                  │  HTTP API (gin) — RBAC middleware on every route │
                  │  + SSE: /v1/events/stream                        │
                  └─┬────────────┬───────────────┬───────┬───────────┘
                    │            │               │       │
                    ▼            ▼               ▼       ▼
              cameras      recording_policies  events  audit
              service      service             service service
                    │            │               │       │
                    ▼            ▼               ▼       ▼
                  ┌──────────────────────────────────────────────┐
                  │      SQLite (modernc, WAL, goose mig.)       │
                  │  cameras / policies / events / users / ...   │
                  │  + cloud_outbox + notification_outbox        │
                  └──────────────────────────────────────────────┘
                    │            │               │
                    ▼            ▼               ▼
                pathManager   retention      notifications
                (RTSP src     sweepers       outbox processor
                 register)    (segments,     (webhook + SMTP,
                              events, clips)  HMAC-signed)
                    │
                    ▼
                  ┌──────────────────────────────────────────────┐
                  │       MediaMTX-derived media pipeline        │
                  │  recorder / recordstore / playback / servers │
                  │              (unchanged hot path)            │
                  └──────────────────────────────────────────────┘
```

In-process buses (`cameras`, `events`) connect mutation surfaces to
consumers. Outbox tables (`notification_outbox`, `cloud_outbox`) decouple
durability from delivery.

---

## Section 1 — Code-tree changes

### Delete entirely

| Path | Reason |
|---|---|
| `internal/pairing/` | MS-pairing flow, no consumer analogue |
| `internal/policysync/` | MS-driven policy poll, replaced by local CRUD |
| `internal/camerasync/` | MS-driven camera poll, replaced by local CRUD |
| `internal/crl/` | CRL polling against MS, irrelevant offline |
| `internal/api/api_v1_recorder_pair*.go`, `api_v1_recorder_unpair.go`, the pairing-state bits of `api_v1_recorder_lifecycle.go`, `api_camerasync.go`, `api_policysync.go`, `camera_lockdown_gate.go`, `policy_lockdown_gate.go` | Pairing endpoints + canonical-source lockdown gates |
| `internal/identity/refresh_ms_metadata*.go`, the MS-clear bits of `identity_clear_test.go` | MS metadata persistence |

### Trim significantly

- `internal/conf/conf.go`: drop `TenantID`, `ManagementServerEndpoint`,
  `CloudEndpoint`, `MSPollInterval`, `CanonicalSource`. Keep recording-policy
  / motion / volume maps as bootstrap fallback that is logged-and-ignored at
  runtime (no auto-import — there are no legacy users to migrate).
- `internal/auth/manager.go`: drop the JWKS-from-MS path and
  `JWTJWKSRootCAs`. Keep `LocalJWT` (recorder-issued) + `InternalUsers`
  modes.
- `internal/identity/identity.go`: keep `id` + ECDSA keypair. Drop
  `device.crt`, `chain.crt`, `pinned-roots.json`, `ms-metadata.json`,
  `canonical-source` files.
- `internal/core/core.go`: remove pairing / sync / CRL / updatepoll wiring
  from the orchestration sequence.
- `internal/mdns/mdns.go`: repurpose to advertise `_raikada-nvr._tcp.local`
  for operator / phone discovery. Drop the MS-listening half.

### Keep intact

The entire media pipeline (`recorder`, `recordstore`, `recordingmeta`,
`playback`, `recordcleaner` *(renamed to `retention/`)*, `staticsources`,
`servers/{rtsp,rtmp,hls,webrtc,srt}`), the existing ONVIF stack
(`internal/onvif`), motion (`internal/motion`), `localauth`, the SQLite
store wrapper, the Web SPA (`internal/web` + `web/`), and the bulk of
`internal/api` handlers (cameras, recordings, segments, clips, events
listing, snapshots, recorder config, ONVIF, diagnostics).

### Add (new packages)

- `internal/cameras/` — DB-backed Service + in-process bus + path-manager
  bridge.
- `internal/cameracred/` — AES-GCM credential vault, key in identity dir.
- `internal/events/` — canonical `Event` type, persistence, in-process bus,
  retention enforcer.
- `internal/notifications/` — webhook + SMTP delivery, signed payloads,
  exponential backoff with jitter, dead-letter after N retries.
- `internal/outbox/` — generic outbox processor used by `notifications` now
  and the cloud bridge later.
- `internal/schedule/` — weekly time-window evaluator
  (`Resolver.IsActive(cameraID, t)`).
- `internal/rbac/` — `Role` and `Permission` enums + role→permission map +
  Gin middleware.
- `internal/retention/` — sweepers for segments, events, clips. (Renames
  the existing `recordcleaner/` for focus.)
- `internal/cloudbridge/` — Service interface + nop processor + horizon
  sweeper for unconfigured installs.

### Defer / open

- **Module path rename** (`github.com/bluenviron/mediamtx` →
  product-specific). Mechanical find/replace; separate "rebrand" PR after
  the foundation lands.
- **Config file rename** (`mediamtx.yml` → `raikada.yml`). Same — defer.
- **`internal/softwareupdate` + `internal/updatepoll`**: keep wired but
  point at a static feed URL the consumer product owns; opt-in via conf.

---

## Section 2 — Database schema

SQLite STRICT tables with `pressly/goose` migrations under
`internal/store/migrations/`. New migrations land as `0003_…`, `0004_…` and
upward; the existing `0001_local_users.sql` and `0002_onvif_subscriptions.sql`
stay.

All ids are `TEXT` UUIDv7 unless noted; all timestamps are `TEXT`
RFC3339.millis (matches `internal/store/store.go`'s `Now()` helper).

### Tables

```text
local_users              EXTEND  (drop is_admin, add role_id FK, email, language)
roles                    NEW     (admin, viewer — seeded)
camera_groups            NEW     (optional org grouping)
cameras                  NEW     (canonical camera entity)
camera_credentials       NEW     (encrypted vault, FK from camera)
camera_capabilities      NEW     (probed snapshot — populated by sub-project 2)
camera_health            NEW     (one row per camera, upserted — populated by sub-project 2)
recording_policies       NEW     (replaces conf.RecordingPolicies map)
recording_schedules      NEW     (weekly windows, FK to policy)
event_types              NEW     (registry; operator-extensible)
event_retention          NEW     (type → keep duration; '__default__' fallback)
events                   NEW     (canonical CameraEvent records)
event_snapshots          NEW     (disk-path JPEG references — populated by sub-project 4)
clips                    NEW     (lazy extraction handles)
clip_segments            NEW     (clip → recording segment join)
notification_targets     NEW     (webhook URLs + SMTP recipients)
notification_subscriptions NEW   (target ↔ event_type ↔ camera filter, with quiet hours)
notification_outbox      NEW     (pending and in-flight deliveries)
audit_log                NEW     (append-only mutation log; never expires by default)
cloud_outbox             NEW     (events / health / audit pending push to future cloud)
system_settings          NEW     (k/v site-wide config)
onvif_subscriptions      KEEP    (existing — Wave A1)
```

No `user_roles` join table: each user has one role via `local_users.role_id`.
No `sessions` table: JWTs are stateless in v1.

### Key shapes

**`local_users` migration (0003)**:

```sql
ALTER TABLE local_users ADD COLUMN role_id TEXT REFERENCES roles(id);
ALTER TABLE local_users ADD COLUMN email TEXT;
ALTER TABLE local_users ADD COLUMN language TEXT NOT NULL DEFAULT 'en';
-- is_admin column is no longer read; column kept (SQLite limitations on
-- column drop) but role_id is authoritative. Migration backfills:
--   role_id := CASE is_admin WHEN 1 THEN 'role_admin' ELSE 'role_viewer' END
```

**`roles`** (seeded with two rows — `role_admin`, `role_viewer`).

**`cameras`**:

```sql
CREATE TABLE cameras (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,             -- operator-friendly, drives RTSP path name
  display_name TEXT,
  group_id TEXT REFERENCES camera_groups(id),
  manufacturer TEXT, model TEXT, serial_number TEXT, firmware_version TEXT,
  mac_address TEXT, ip_address TEXT, hostname TEXT,
  source_type TEXT NOT NULL,             -- rtsp|rtsps|rtmp|hls|onvif|...
  source_url TEXT NOT NULL,              -- credentials NOT inline
  onvif_xaddr TEXT,                      -- ONVIF device service URL
  recording_policy_id TEXT REFERENCES recording_policies(id),
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  paired_at TEXT, last_capability_probe_at TEXT
) STRICT;
CREATE INDEX idx_cameras_group ON cameras(group_id);
CREATE INDEX idx_cameras_enabled ON cameras(enabled);
```

**`camera_credentials`** (separate row so creds never accidentally leak via
camera serializers):

```sql
CREATE TABLE camera_credentials (
  camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
  username TEXT NOT NULL,
  password_ciphertext BLOB NOT NULL,    -- AES-GCM
  password_nonce BLOB NOT NULL,
  onvif_username TEXT,
  onvif_password_ciphertext BLOB,
  onvif_password_nonce BLOB,
  rotated_at TEXT NOT NULL,
  rotation_due_at TEXT
) STRICT;
```

**`camera_health`** (one row per camera, upserted by Sub-project 2):

```sql
CREATE TABLE camera_health (
  camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
  rtsp_state TEXT NOT NULL,             -- 'connected'|'reconnecting'|'failed'|'idle'
  last_keyframe_at TEXT, last_event_at TEXT, last_seen_at TEXT,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_error TEXT, updated_at TEXT NOT NULL
) STRICT;
```

**`camera_capabilities`** (populated by Sub-project 2):

```sql
CREATE TABLE camera_capabilities (
  camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
  profiles_json TEXT NOT NULL,
  selected_profile_token TEXT,
  has_audio INTEGER NOT NULL DEFAULT 0,
  has_ptz INTEGER NOT NULL DEFAULT 0,
  has_motion INTEGER NOT NULL DEFAULT 0,
  has_io INTEGER NOT NULL DEFAULT 0,
  has_imaging INTEGER NOT NULL DEFAULT 0,
  vendor_capabilities_json TEXT,
  probed_at TEXT NOT NULL
) STRICT;
```

**`recording_policies`** (replaces `conf.RecordingPolicies` map):

```sql
CREATE TABLE recording_policies (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  mode TEXT NOT NULL,                   -- 'continuous'|'motion'|'scheduled'|'off'
  retention_duration_seconds INTEGER NOT NULL,
  container TEXT NOT NULL,              -- 'fmp4'|'mpegts'
  min_segment_duration_seconds INTEGER NOT NULL,
  max_segment_duration_seconds INTEGER NOT NULL,
  part_duration_ms INTEGER NOT NULL,
  max_part_size_bytes INTEGER NOT NULL,
  pre_event_seconds INTEGER NOT NULL DEFAULT 5,
  post_event_seconds INTEGER NOT NULL DEFAULT 5,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
) STRICT;
-- Seeded with one row: id='policy_default', name='Default Policy',
-- mode='continuous'. Cannot be deleted.
```

**`recording_schedules`** (no `timezone` column — site-level setting):

```sql
CREATE TABLE recording_schedules (
  id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL REFERENCES recording_policies(id) ON DELETE CASCADE,
  day_of_week INTEGER NOT NULL,         -- 0=Sun..6=Sat
  start_minute INTEGER NOT NULL,        -- 0..1439 minutes from midnight site-local
  end_minute INTEGER NOT NULL           -- exclusive; if < start, wraps midnight
) STRICT;
CREATE INDEX idx_schedules_policy ON recording_schedules(policy_id);
```

**`events`**:

```sql
CREATE TABLE events (
  id TEXT PRIMARY KEY,                  -- UUIDv7 (sortable)
  camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
  type_id TEXT NOT NULL REFERENCES event_types(id),
  source TEXT NOT NULL,
  occurred_at TEXT NOT NULL,            -- camera-reported (untrusted)
  received_at TEXT NOT NULL,            -- recorder-side (trusted)
  severity TEXT,                        -- 'info'|'warning'|'critical'
  payload_json TEXT,
  region_json TEXT,
  acknowledged_at TEXT,
  acknowledged_by TEXT REFERENCES local_users(id),
  expires_at TEXT NOT NULL              -- materialized at insert; received_at + retention
) STRICT;
CREATE INDEX idx_events_camera_time ON events(camera_id, occurred_at);
CREATE INDEX idx_events_type ON events(type_id);
CREATE INDEX idx_events_expires ON events(expires_at);
```

**`event_snapshots`** (disk-path only, no inline blob):

```sql
CREATE TABLE event_snapshots (
  event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,                   -- 'full'|'thumb'
  width INTEGER, height INTEGER,
  path TEXT NOT NULL,                   -- on-disk path inside snapshot store
  size_bytes INTEGER NOT NULL,
  fetched_at TEXT NOT NULL,
  PRIMARY KEY (event_id, kind)
) STRICT;
```

**`clips`** + **`clip_segments`**:

```sql
CREATE TABLE clips (
  id TEXT PRIMARY KEY,
  event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
  camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
  start_time TEXT NOT NULL, end_time TEXT NOT NULL,
  pre_roll_seconds INTEGER NOT NULL DEFAULT 0,
  post_roll_seconds INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL,                  -- 'requested'|'preparing'|'ready'|'failed'
  format TEXT NOT NULL DEFAULT 'fmp4',
  output_path TEXT, size_bytes INTEGER, checksum TEXT,
  created_by TEXT REFERENCES local_users(id),
  created_at TEXT NOT NULL, ready_at TEXT, expires_at TEXT
) STRICT;
CREATE INDEX idx_clips_camera_time ON clips(camera_id, start_time);
CREATE INDEX idx_clips_event ON clips(event_id);

CREATE TABLE clip_segments (
  clip_id TEXT NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
  segment_path TEXT NOT NULL,
  start_offset_ms INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  PRIMARY KEY (clip_id, segment_path)
) STRICT;
```

**`event_types`** + **`event_retention`** (operator-extensible):

```sql
CREATE TABLE event_types (
  id TEXT PRIMARY KEY,                  -- 'motion','doorbell','line_cross','tamper','io_in','vendor.<name>','custom.<name>'
  display_name TEXT NOT NULL,
  vendor TEXT,                          -- 'onvif'|'amcrest'|'hikvision'|'reolink'|'internal'|'custom'
  description TEXT
) STRICT;
-- Seeded with well-known types; operators add more via POST /v1/event-types.

CREATE TABLE event_retention (
  type_id TEXT PRIMARY KEY REFERENCES event_types(id) ON DELETE CASCADE,
  keep_duration_seconds INTEGER NOT NULL,
  updated_at TEXT NOT NULL
) STRICT;
-- Special row: type_id='__default__' with a 7d default keep_duration.
```

**Notifications**:

```sql
CREATE TABLE notification_targets (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,                   -- 'webhook'|'email'
  name TEXT NOT NULL,
  webhook_url TEXT,
  webhook_secret_ciphertext BLOB,       -- HMAC signing secret, vault-encrypted
  webhook_secret_nonce BLOB,
  email_address TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
) STRICT;

CREATE TABLE notification_subscriptions (
  id TEXT PRIMARY KEY,
  target_id TEXT NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
  event_type_id TEXT REFERENCES event_types(id),  -- nullable = all types
  camera_id TEXT REFERENCES cameras(id),           -- nullable = all cameras
  min_severity TEXT,                               -- nullable = all severities
  quiet_hours_start_minute INTEGER,
  quiet_hours_end_minute INTEGER
) STRICT;

CREATE TABLE notification_outbox (
  id TEXT PRIMARY KEY,
  target_id TEXT NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
  event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  payload_json TEXT NOT NULL,
  state TEXT NOT NULL,                  -- 'pending'|'in_flight'|'delivered'|'failed'|'dead'
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  last_error TEXT,
  created_at TEXT NOT NULL,
  delivered_at TEXT
) STRICT;
CREATE INDEX idx_outbox_state_next ON notification_outbox(state, next_attempt_at);
```

**`audit_log`**:

```sql
CREATE TABLE audit_log (
  id TEXT PRIMARY KEY,                  -- UUIDv7
  occurred_at TEXT NOT NULL,
  actor_user_id TEXT REFERENCES local_users(id),
  actor_username TEXT,                  -- denormalized snapshot at write time
  actor_ip TEXT,
  action TEXT NOT NULL,
  target_kind TEXT,
  target_id TEXT,
  before_json TEXT,                     -- redacted; see Section 7
  after_json TEXT,
  details TEXT
) STRICT;
CREATE INDEX idx_audit_occurred ON audit_log(occurred_at);
CREATE INDEX idx_audit_actor ON audit_log(actor_user_id, occurred_at);
```

**`cloud_outbox`** (table ships, processor disabled in v1):

```sql
CREATE TABLE cloud_outbox (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,                   -- 'event'|'health'|'audit'
  payload_json TEXT NOT NULL,
  state TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  last_error TEXT,
  created_at TEXT NOT NULL,
  delivered_at TEXT
) STRICT;
CREATE INDEX idx_cloud_outbox_state ON cloud_outbox(state, next_attempt_at);
```

**`system_settings`** (site-wide k/v):

```sql
CREATE TABLE system_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  updated_by TEXT REFERENCES local_users(id)
) STRICT;
```

Seeded keys: `site_name`, `timezone` (host-derived at first boot),
`language`, `lockout_threshold`, `lockout_duration_minutes`,
`capability_probe_interval_hours`, `health_debounce_seconds`,
`default_policy_id`, `outbox_workers`, `outbox_max_attempts`,
`outbox_backoff_seconds`, `cloud_endpoint`, `cloud_token_ciphertext`,
`cloud_token_nonce`, `cloud_outbox_horizon_hours`, `smtp_*`, etc.

### Key choices

- **DB-canonical for cameras + recording_policies + schedules + users**.
  YAML keeps only bootstrap-only fields. Path manager learns its paths from
  the DB at startup and on mutation events. YAML `paths:` entries are
  preserved for non-camera uses (system paths, always-available files); on
  conflict, DB wins and the YAML entry is logged-and-ignored.
- **AES-GCM envelope on credentials**. Key file at `<identityDir>/cred.key`
  (mode 0600). Threat model: protects against backup leaks and casual
  disk-image theft. Not a substitute for full-disk encryption against
  physical seizure.
- **`expires_at` materialized at insert time on events**. Lookups for
  retention sweep are fast (`WHERE expires_at < ?`) and changing retention
  policy doesn't retroactively expire historical events.
- **Snapshots: disk path only**, no inline `BLOB`. Storage layout under a
  configurable snapshot dir (defaults to `<recording_root>/snapshots/<camera>/<yyyy-mm-dd>/<event_id>-<kind>.jpg`).
- **Outbox pattern for both notifications and cloud**. Same processor
  shape, two tables. Cloud bridge is a config flip later.
- **No `sessions` table for v1**. JWTs are stateless; revoke happens by
  rotating the recorder-local signing key and forcing re-login.
- **No multi-role-per-user join in v1**. Each user has one role via
  `local_users.role_id`.
- **Audit retention**: never expires by default. Manual purge endpoint
  exists for operators who need it.
- **Pre/post-roll**: configurable per policy, default 5s for both.

---

## Section 3 — Authentication, RBAC, TLS, bootstrap, discovery

### Authentication

- **Issuer**. Existing `localauth.Manager` (ED25519 signing key at
  `<identityDir>/jwt.key`, mode 0600). Issuer / audience:
  `urn:raikada:recorder:<id>`. Access-token TTL: 15 min (existing).
- **Login**. `POST /v1/auth/login {username, password}` →
  `{access_token, expires_at, user: {id, username, display_name, role, must_change_password}}`.
  Browser also gets the same token in an `HttpOnly; Secure; SameSite=Lax`
  cookie; programmatic clients use `Authorization: Bearer …`.
- **Logout**. `POST /v1/auth/logout` clears the cookie. JWT is stateless;
  no server-side session table in v1.
- **Password change**. `POST /v1/auth/password {current, new}`. Argon2id
  via existing `internal/auth/password.go`. Clears `must_change_password`.
- **Lockout**. Existing `failed_login_attempts` + `locked_until` columns
  retained. Threshold and duration come from `system_settings`; defaults
  5 attempts → 15 min lock.

### RBAC

`internal/rbac/` package:

```go
type Role string
const (
    RoleAdmin  Role = "admin"
    RoleViewer Role = "viewer"
)

type Permission string
const (
    PermCameraRead   Permission = "camera.read"
    PermCameraWrite  Permission = "camera.write"
    PermPolicyRead   Permission = "policy.read"
    PermPolicyWrite  Permission = "policy.write"
    PermEventRead    Permission = "event.read"
    PermEventAck     Permission = "event.ack"
    PermClipRead     Permission = "clip.read"
    PermClipExport   Permission = "clip.export"
    PermNotifManage  Permission = "notification.manage"
    PermUserManage   Permission = "user.manage"
    PermSystemConfig Permission = "system.config"
    PermAuditRead    Permission = "audit.read"
)
```

Role → permission map in code (not in DB):

| Role | Permissions |
|---|---|
| `admin` | all |
| `viewer` | `camera.read`, `policy.read`, `event.read`, `clip.read` |

Gin middleware: `RequirePerm(PermCameraWrite)` decorates each handler.
Reads claims, looks up role in the in-code map, returns 403 on miss.
Audit-logs every denial as `auth.permission_denied`.

Promotion path to DB-defined custom roles: add a `roles_permissions` table,
swap the in-code map for a DB lookup. Out of scope for v1.

### Bootstrap admin

On first start, if `count(local_users) == 0`:

1. Generate a 24-char URL-safe random password.
2. Create the admin row (`role_id='role_admin'`, `must_change_password=1`).
3. Write `<identityDir>/initial-admin-password.txt` (mode 0600) and log a
   single warning line:
   `INITIAL ADMIN PASSWORD: … — change immediately, this line is not redacted`.
4. On first successful password change, delete the file.

For headless / fleet installs: also honor `RAIKADA_BOOTSTRAP_PASSWORD` env
var — if set, use it instead of generating, set `must_change_password=0`,
skip the file write.

### TLS

Three layers:

1. **Self-signed default**. First start generates cert at
   `<identityDir>/tls.crt` + `<identityDir>/tls.key` (mode 0600 on key).
   SANs: hostname, `<id>.local`, `localhost`, primary IPs. Cert covers HTTPS
   API + RTSPS publish. Recorder will not start with plaintext HTTP for the
   API; RTSP can stay plaintext on LAN by operator choice.
2. **BYO via conf paths**. `tls.cert_path`, `tls.key_path` in `raikada.yml`.
   `fsnotify` watches both; in-place reload on change, no restart. If either
   file is missing or fails to parse on reload, the recorder retains the
   prior cert/key pair, logs the error, and emits an
   `system.tls_reload_failed` audit row — never falls back to no-TLS.
3. **BYO via API**. `PUT /v1/system/tls {cert_pem, key_pem}` writes to
   identity dir paths, swaps. Audit-logged as `system.tls_replaced`.

ACME / Let's Encrypt automation: leave `tls.acme: { enabled: false, ... }`
config block reserved; implementation deferred.

### Discovery + first-run UX

- **mDNS**: `_raikada-nvr._tcp.local`, port from conf, TXT records:
  `v=<version>`, `id=<recorder-id>`, `setup=required|complete`. Repurposes
  the existing `internal/mdns` advertiser.
- **First visit**. SPA detects `setup=required` via
  `GET /v1/system/setup-status` (anonymous endpoint), shows wizard:
  1. Enter the printed initial password
  2. Set new admin password (forced)
  3. Site name, time zone, language
  4. Optional: upload TLS cert / key, configure SMTP
  5. Tour: discover cameras → add cameras (defers to Sub-project 2)
- After completion, `setup=complete` and the dashboard takes over.

### Anonymous endpoints

Only:

- `GET /v1/system/info`
- `GET /v1/system/setup-status`
- `GET /v1/health` (liveness only)
- `POST /v1/auth/login`

Everything else: `RequirePerm(...)`. Non-authenticated requests return 401
with no body.

---

## Section 4 — Cameras canonical model + credential vault

### Bridge: DB → path manager

```text
                       (writes)
  POST/PATCH/DELETE  ───────────►  cameras / camera_credentials  (SQLite)
  /v1/cameras/...                            │
                                             │ emits camera.changed
                                             ▼
                                      camera bus (in-process)
                                             │
                       ┌─────────────────────┼─────────────────────┐
                       ▼                     ▼                     ▼
                pathManager           healthCollector       onvifManager
                (register/             (subscribe RTSP/      (add/remove
                 update/remove         keyframe/event         pullpoint sub
                 RTSP path)            taps; sub-project 2)   if onvif_xaddr;
                                                              sub-project 2)
```

`internal/cameras/` package owns the bus and a `Service` that:

- At startup: reads all `enabled=1` rows, materializes each into a
  `pathManager` registration.
- On `Create`: writes the row, then emits.
- On `Update`: writes, diffs, emits with the changed-fields set.
- On `Delete`: emits remove first (path drained), then deletes the row.

`pathManager.UpsertCameraPath(id, name, sourceURL, sourceConfig)` and
`RemovePath(name)` helpers wrap the existing `pathManager.SetConf()`.

### Credential vault

`internal/cameracred/`:

```go
type Vault struct{ key [32]byte }

func Open(identityDir string) (*Vault, error)         // creates cred.key on first run
func (v *Vault) Encrypt(plain []byte) (ct, nonce []byte, err error)
func (v *Vault) Decrypt(ct, nonce []byte) ([]byte, error)
func (v *Vault) MaterializeRTSPURL(template string, username, password string) string
```

- AES-GCM (256-bit key, 12-byte random nonce per encrypt). Key persisted at
  `<identityDir>/cred.key` mode 0600.
- `MaterializeRTSPURL` is the only place plaintext password meets a string.
  Result is handed to the path manager by-value, never logged. Existing
  redaction in `internal/auth/credentials.go` is extended to also redact
  RTSP URLs with embedded userinfo.
- `cameracred.Service.GetForCamera(id)` is the only consumer-facing accessor;
  logs `credential read for camera <id> by <subsystem>` at debug level for
  traceability without leaking the value.

### Camera shape on the wire

`GET /v1/cameras/{id}` returns the row joined with health + capability
summary, **no credentials**:

```json
{
  "id": "018f...", "name": "front_door", "display_name": "Front Door",
  "manufacturer": "Amcrest", "model": "IP5M-T1179EW",
  "firmware_version": "2.840.0000000.34", "mac_address": "9c:8e:cd:...",
  "ip_address": "192.168.1.42", "hostname": "amc-frontdoor.local",
  "source_type": "rtsp",
  "source_url": "rtsp://192.168.1.42/cam/realmonitor?channel=1&subtype=0",
  "onvif_xaddr": "http://192.168.1.42/onvif/device_service",
  "credentials": { "username": "raikada", "password_set": true, "rotated_at": "..." },
  "recording_policy_id": "policy_default", "group_id": null, "enabled": true,
  "health": { "rtsp_state": "connected", "last_keyframe_at": "...", "last_event_at": "...", "consecutive_failures": 0 },
  "capabilities": { "selected_profile_token": "Profile_1", "has_audio": true, "has_motion": true, "has_ptz": false, "probed_at": "..." },
  "created_at": "...", "updated_at": "...", "paired_at": "..."
}
```

`source_url` is the camera-supplied template (no userinfo). The internal
materialized URL with credentials never leaves the recorder process.

### Mutation API (foundation slice)

| Method | Path | Perm | Notes |
|---|---|---|---|
| GET | `/v1/cameras` | `camera.read` | Paginated; default 50, max 500 |
| GET | `/v1/cameras/{id}` | `camera.read` | Single |
| POST | `/v1/cameras` | `camera.write` | Body excludes credentials |
| PATCH | `/v1/cameras/{id}` | `camera.write` | Partial; cannot include credentials |
| DELETE | `/v1/cameras/{id}` | `camera.write` | Schema cascade: events, clips, snapshots, credentials, capabilities, health. Application-level teardown for ONVIF subs (see "Cascade vs deactivate" below). |
| PUT | `/v1/cameras/{id}/credentials` | `camera.write` | Atomic encrypt + write; emits `camera.credentials_rotated` |
| POST | `/v1/cameras/{id}/probe` | `camera.write` | Force capability re-probe (Sub-project 2) |
| GET | `/v1/cameras/{id}/health` | `camera.read` | Current health snapshot (Sub-project 2) |
| GET | `/v1/cameras/{id}/snapshot` | `camera.read` | Live snapshot (existing handler) |

Discovery, pairing, capability probe, and credential issuance flows are
**Sub-project 2**. Foundation gives operators a manual-add path: enter IP +
creds, recorder probes, registers, starts recording.

### YAML `paths:` policy

Camera paths come **only** from the DB. The YAML `paths:` map is preserved
for non-camera entries (always-available file paths, hooks-only test paths,
MediaMTX-style publisher paths). On startup, if a YAML `paths:` entry has
the same name as a camera row, the DB wins and the YAML entry is
logged-and-ignored.

### Source-URL changes

Immediate teardown + re-init on `source_url` mutation. The existing
`recorder` seals in-flight segments on path drain — no recording loss.

### Cascade vs deactivate

`DELETE /v1/cameras/{id}` cascades. Schema-level (FK `ON DELETE CASCADE`):
events, clips, snapshots, credentials, capabilities, health. Application-
level (the existing `onvif_subscriptions` table predates the `cameras`
table and lacks a FK): `cameras.Service.Delete` first calls
`onvifManager.RemoveSubscription` for each active subscription bound to
the camera, then deletes the camera row. No `deactivate` endpoint in v1 —
operators preserve history by setting `enabled=0` via PATCH instead.

---

## Section 5 — Recording policies, schedules, retention sweepers

### Mode resolver

`internal/schedule/`:

```go
type Resolver interface {
    IsActive(ctx context.Context, cameraID string, t time.Time) (active bool, reason string, err error)
}
```

Algorithm:

1. Look up `cameras.recording_policy_id`; null → `policy_default`.
2. Load policy. If `enabled=0` or `mode='off'` → false ("policy disabled").
3. `mode='continuous'` → true ("continuous").
4. `mode='motion'` → true if motion controller has the camera in
   `motion_active` state OR within `(motion_end_at + post_event_seconds)`.
5. `mode='scheduled'` → evaluate weekly windows in site timezone (below);
   true if any window covers t.

Called from: the camera bus consumer that gates the recorder; the retention
sweeper's overlap pass; the API
(`GET /v1/cameras/{id}/recording-state`).

### Schedule semantics

- **Site timezone is global**, stored in `system_settings['timezone']`. The
  resolver computes against the site timezone's wall-clock minutes.
- **Wrap midnight** allowed: `end_minute < start_minute` means the window
  crosses into the next day.
- **Overlap is union**: multiple windows on the same day → camera is
  recording if any covers t.
- **DST**: the "spring forward" hour is unrecordable; the "fall back" hour
  records twice. Documented trade-off; no clever logic.

### Default policy seeding

```text
id:                            policy_default     (cannot be deleted)
name:                          Default Policy
mode:                          continuous
retention_duration_seconds:    1209600 (14d)
container:                     fmp4
min_segment_duration_seconds:  60
max_segment_duration_seconds:  300
part_duration_ms:              1000
max_part_size_bytes:           1048576 (1MiB)
pre_event_seconds:             5
post_event_seconds:            5
enabled:                       true
```

`DELETE /v1/recording-policies/policy_default` returns 409 with a clear
message.

### Pre/post-roll mechanics (motion mode)

Recorder reads the stream continuously regardless of mode. Segments are
written continuously into the recordstore. Retention sweepers decide what
to keep:

- `mode=continuous` → keep all segments up to retention age.
- `mode=motion` → keep only segments overlapping
  `[motion_start - pre_event_seconds, motion_end + post_event_seconds]`
  for any motion event on that camera; delete the rest at the next sweep
  (default cadence: 5 minutes).
- `mode=scheduled` → keep only segments overlapping a scheduled window
  (intersected with retention age).

Trade-off: motion mode writes-then-deletes is more I/O than gating writes
upfront. Accepted in v1; alternative (pre-buffer in RAM with partial-segment
splicing) is not worth the complexity for SMB scale.

### Retention enforcement (three sweepers)

`internal/retention/` (renamed from `internal/recordcleaner/`). Three
goroutines:

| Sweeper | Cadence | Rule |
|---|---|---|
| `segments` | 5 min | Delete segments older than `policy.retention_duration` AND with no retention reason |
| `events` | 30 min | Delete `events WHERE expires_at < now()` (cascade carries `event_snapshots`) |
| `clips` | 30 min | Delete `clips WHERE expires_at IS NOT NULL AND expires_at < now() AND state IN ('ready','failed')`; remove `output_path` from disk |

All sweepers idempotent. `POST /v1/system/retention/sweep` (admin) for
diagnostics. `audit_log` is never swept by default.

### API surface

| Method | Path | Perm |
|---|---|---|
| GET | `/v1/recording-policies` | `policy.read` |
| GET | `/v1/recording-policies/{id}` | `policy.read` |
| POST | `/v1/recording-policies` | `policy.write` |
| PATCH | `/v1/recording-policies/{id}` | `policy.write` |
| DELETE | `/v1/recording-policies/{id}` | `policy.write` (409 for `policy_default`) |
| GET | `/v1/recording-policies/{id}/schedules` | `policy.read` |
| PUT | `/v1/recording-policies/{id}/schedules` | `policy.write` (atomic replace-all of the policy's schedule rows) |
| GET | `/v1/cameras/{id}/recording-state` | `camera.read` |
| GET | `/v1/system/settings` | `system.config` |
| PATCH | `/v1/system/settings` | `system.config` (partial map of key→value) |
| POST | `/v1/system/retention/sweep` | `system.config` (diagnostic trigger) |

Schedule API translates between minutes (DB) and HH:MM strings (wire) for
operator readability.

---

## Section 6 — Events, notifications, outbox

### Canonical event type

`internal/events/types.go`:

```go
type Event struct {
    ID            string
    CameraID      string
    TypeID        string
    Source        string
    OccurredAt    time.Time
    ReceivedAt    time.Time
    Severity      string
    PayloadJSON   json.RawMessage
    RegionJSON    json.RawMessage
    AcknowledgedAt *time.Time
    AcknowledgedBy *string
    ExpiresAt     time.Time
}
```

### Service shape

```go
type Service interface {
    Insert(ctx context.Context, ev *Event) error
    Get(ctx context.Context, id string) (*Event, error)
    List(ctx context.Context, f ListFilter) (page []Event, nextCursor string, err error)
    Acknowledge(ctx context.Context, id, userID string) error
    Subscribe() (<-chan Event, func())
}
```

### Insert flow

```text
vendor adapter (sub-projects 2-3)
        │ Insert(ev)
        ▼
events.Service
   1. lookup keep_duration in event_retention[ev.TypeID]
      (fallback __default__: 7d)
   2. ev.ExpiresAt = ev.ReceivedAt + keep_duration
   3. INSERT into events (transaction; also writes cloud_outbox row)
   4. publish to in-process bus
        │
        ├─► notification dispatcher
        ├─► snapshot fetcher (sub-project 4)
        ├─► clip linker (sub-project 4)
        └─► SSE fan-out for /v1/events/stream
```

`expires_at` is computed from `received_at`, not `occurred_at`, so a
camera with a busted clock cannot keep events forever or expire them
instantly.

### Notification matching

For each event published, the dispatcher loads:

```sql
SELECT s.id, s.target_id, s.event_type_id, s.camera_id, s.min_severity,
       s.quiet_hours_start_minute, s.quiet_hours_end_minute, t.kind, t.enabled
FROM notification_subscriptions s
JOIN notification_targets t ON t.id = s.target_id
WHERE t.enabled = 1
```

(Cached, refreshed on `notification_*` mutations.) For each subscription:

- `event_type_id IS NULL OR matches`
- `camera_id IS NULL OR matches`
- `min_severity` rank check (info < warning < critical)
- Quiet-hours check against current site time

Matches → `INSERT INTO notification_outbox (id, target_id, event_id, payload_json, state='pending', next_attempt_at=now())`.

**Quiet hours** live on the subscription (not target).

### Outbox processor

Worker pool (default 4, `system_settings['outbox_workers']`):

```text
loop:
  pick batch (UPDATE … WHERE state='pending' AND next_attempt_at<=now() RETURNING id)
  for each row:
    state='in_flight'
    dispatch (webhook | email)
    on 2xx:        state='delivered', delivered_at=now()
    on 4xx:        state='dead', last_error='...', audit log entry
    on 5xx/timeout: attempts++; if attempts >= max_attempts: 'dead'
                                else: 'pending', next_attempt_at = now() + backoff(attempts)
```

Backoff: exponential with jitter — `30s, 1m, 2m, 5m, 15m, 30m, 1h, 2h`
(default 8 attempts ≈ 4h22m total retry window). Tunable via
`system_settings`.

### Webhook payload (`raikada.event.v1`)

```json
{
  "schema": "raikada.event.v1",
  "delivery_id": "01HXY...",
  "site": { "id": "01H...", "name": "Front House" },
  // site.id == the recorder's UUIDv7 (identity dir); site.name == system_settings['site_name']
  "event": {
    "id": "01HXY...", "type": "motion", "type_display_name": "Motion",
    "camera": { "id": "01H...", "name": "front_door", "display_name": "Front Door" },
    "source": "onvif_pullpoint", "severity": "info",
    "occurred_at": "...", "received_at": "...",
    "region": { "boxes": [{ "x": 120, "y": 80, "w": 64, "h": 96 }] },
    "payload": { "...vendor passthrough..." }
  },
  "snapshot_url": "https://nvr.local/v1/events/01HXY.../snapshot/full?sig=...",
  "thumbnail_url": "https://nvr.local/v1/events/01HXY.../snapshot/thumb?sig=...",
  "clip_url": null
}
```

Headers:

- `X-Raikada-Signature: hmac-sha256-hex(target.secret, body)`
- `X-Raikada-Delivery: <delivery_id>`
- `X-Raikada-Event: <event_type>`
- `Content-Type: application/json`
- 5s default request timeout

`snapshot_url` / `thumbnail_url` are signed (HMAC + short TTL in query
string). `clip_url` is `null` until Sub-project 4 lands.

### SMTP

Optional, off by default. Settings in `system_settings`:

| key | meaning |
|---|---|
| `smtp_host`, `smtp_port` | server |
| `smtp_username`, `smtp_password_ciphertext`, `smtp_password_nonce` | auth (vault-encrypted) |
| `smtp_from_address` | envelope from |
| `smtp_use_tls` | STARTTLS / implicit |

Email body: small HTML template via `html/template`. Site name, camera,
event type, occurred_at, embedded thumbnail (`data:` URL), link back to
recorder.

### Realtime to SPA

`GET /v1/events/stream` (SSE). Permission: `event.read`. Streams the
in-process bus, filtered by user's permission and by query params
(`?camera_id=…&type=motion`). SSE chosen over WebSocket: one-way, automatic
reconnect, JWT in query string.

### API surface

| Method | Path | Perm |
|---|---|---|
| GET | `/v1/events` | `event.read` |
| GET | `/v1/events/{id}` | `event.read` |
| GET | `/v1/events/stream` | `event.read` (SSE) |
| POST | `/v1/events/{id}/acknowledge` | `event.ack` |
| GET | `/v1/event-types` | `event.read` |
| POST | `/v1/event-types` | `system.config` |
| PATCH | `/v1/event-types/{id}` | `system.config` |
| GET | `/v1/event-retention` | `event.read` |
| PUT | `/v1/event-retention/{type_id}` | `system.config` |
| GET / POST / PATCH / DELETE | `/v1/notification-targets[/...]` | `notification.manage` |
| POST | `/v1/notification-targets/{id}/test` | `notification.manage` |
| GET / POST / DELETE | `/v1/notification-subscriptions[/...]` | `notification.manage` |
| GET | `/v1/notification-outbox` | `notification.manage` |
| POST | `/v1/notification-outbox/{id}/retry` | `notification.manage` |

---

## Section 7 — Audit log + cloud outbox seam

### Audit log

Every mutation is logged. Reads are not. Logged actions:

| Domain | Actions |
|---|---|
| auth | `auth.login.success`, `auth.login.failure`, `auth.logout`, `auth.password_changed`, `auth.locked_out`, `auth.permission_denied` |
| user | `user.created`, `user.updated`, `user.role_changed`, `user.deactivated`, `user.deleted` |
| camera | `camera.created`, `camera.updated`, `camera.deleted`, `camera.credentials_rotated`, `camera.probe_triggered` |
| policy | `policy.created`, `policy.updated`, `policy.deleted`, `policy.schedules_replaced` |
| clip | `clip.exported`, `clip.deleted` |
| event | `event.acknowledged` |
| notification | `notification_target.{created,updated,deleted}`, `notification_subscription.{created,deleted}`, `notification.dead_letter` |
| system | `system.tls_replaced`, `system.settings_updated`, `system.retention_swept`, `system.bootstrap_completed` |

**Redaction (per-domain explicit allow-list, enforced at write time)**:
password hashes, raw passwords, JWT signing key, AES-GCM key, SMTP
password, webhook secrets, RTSP credentials never appear in
`before_json` / `after_json` / `details`. The audit emit helpers carry an
explicit allow-list of fields per domain; everything else is dropped before
serialization. A `redacted: ["password", "secret"]` array can appear in
`before_json` / `after_json` to indicate "yes, this field changed, but its
value is not recorded."

**API**:

| Method | Path | Perm |
|---|---|---|
| GET | `/v1/audit` | `audit.read` |
| GET | `/v1/audit/{id}` | `audit.read` |
| POST | `/v1/audit/export` | `audit.read` (streams CSV / NDJSON) |
| POST | `/v1/audit/purge?older_than=<duration>` | `system.config` (admin manual purge; emits its own audit row first) |

Retention: never expires by default.

### Cloud outbox seam

Pattern:

1. Schema (`cloud_outbox`, Section 2): kind, payload, state, attempts,
   next_attempt_at.
2. Insert paths: `events.Service.Insert`, `healthCollector` on state
   change, `audit.Emit` on every row → each ALSO writes a `cloud_outbox`
   row in the same transaction. Cheap (one extra insert), idempotent.
3. Processor: `internal/cloudbridge/` runs a goroutine **only when**
   `system_settings['cloud_endpoint']` is non-empty. Otherwise, a separate
   sweeper trims rows older than `cloud_outbox_horizon_hours` (default
   168h = 7d) so unconfigured installs do not grow unboundedly.

For v1 foundation, ships:

- The schema
- The dual-insert hooks
- The unconfigured-install sweeper
- A `cloudbridge.Service` interface with a `nopProcessor` implementation as
  the only binding

Reserved settings (ignored by nop): `cloud_endpoint`,
`cloud_token_ciphertext`, `cloud_token_nonce`, `cloud_outbox_horizon_hours`.

---

## Open items / explicit deferrals

| # | Item | Disposition |
|---|---|---|
| O1 | Webhook signature header style (simple vs Stripe-style `t=…,v1=…`) | Use simple `X-Raikada-Signature` for v1; Stripe-style is a future option |
| O2 | Outbox max attempts and total retry window | 8 attempts, ~4h22m; tunable via `system_settings` |
| O3 | Severity scale granularity | 3-tier (info / warning / critical) for v1; numeric scale future |
| O4 | Audit redaction strategy | Per-domain explicit allow-list (safer than deny-list); maintenance burden accepted |
| O5 | Cloud-bridge dual-insert cost | Accepted at consumer scale; no "cloud disabled" guard in v1 |
| O6 | Module path rename | Defer to a separate "rebrand" PR after foundation lands |
| O7 | Config file rename (`mediamtx.yml` → `raikada.yml`) | Defer with module path rename |
| O8 | TPM / OS-keyring credential protection | Future v2 |
| O9 | ACME / Let's Encrypt automation | Hook reserved; implementation deferred |
| O10 | DB-defined custom roles | Foundation provides code-defined roles; promotion to DB is a later migration |
| O11 | Server-side JWT revocation | Future; add `revoked_jti` table when needed |

---

## Out of scope (sub-project pointers)

These features are in the consumer NVR roadmap but **not** in this
foundation spec; each gets its own brainstorm → spec → plan → impl cycle.

- **Sub-project 2 — Camera lifecycle.** ONVIF WS-Discovery + mDNS sweep
  for camera-side discovery; pairing flows; credential issuance UX;
  capability probe + profile selection; health monitoring (RTSP / event /
  keyframe taps); firmware version tracking; camera-side config mirroring
  (motion zones, schedules pushed to the camera). Populates
  `camera_capabilities`, `camera_health`, `cameras.firmware_version`,
  `cameras.paired_at`.
- **Sub-project 3 — Events and vendor channels.** ONVIF Base subscription
  (PullPoint already exists). Vendor adapters via the sibling
  `amcrest-sdk/` and `onvif-go/` modules: Amcrest CGI, Hikvision ISAPI,
  Reolink. Canonical `Event.PayloadJSON` / `RegionJSON` normalization
  rules per vendor. Supervised reconnection per source.
- **Sub-project 4 — Snapshots + event-to-clip linkage.** On-event JPEG
  fetch (writes `event_snapshots`). Thumbnail generation. Lazy clip
  extraction via `clips` and `clip_segments` tables. Signed clip URLs in
  webhook payloads. Pre/post-roll stitching at clip export time
  (separate from segment-retention pre/post-roll).

---

## Migration / rollout

- **Branch.** Develop on a feature branch / git worktree off the current
  enterprise tip. No phased landing.
- **Test.** Strong unit coverage per package + integration tests for the
  API + a single end-to-end smoke test:
  *boot → first-run wizard → set admin password → add camera (manual) →
  see live → record → playback → ack synthetic event → webhook delivered →
  log out / log back in.*
- **Parallel implementation.** The new packages are independent enough that
  the implementation plan can dispatch parallel agents per subsystem
  (events, notifications, schedule, RBAC, vault) and merge into the single
  milestone. The implementation plan (next step, via `writing-plans` skill)
  will lay this out.
- **Merge.** Single milestone PR back to main once tests pass. Tag a
  `v1.0.0-foundation` after merge.

---

## Test strategy

- **Unit**: each new package (`cameras`, `cameracred`, `events`,
  `notifications`, `outbox`, `schedule`, `rbac`, `retention`, `cloudbridge`)
  ships with table-driven unit tests covering happy path, error path, and
  one edge case per public method. Vault tests cover key generation, round
  trip, and corruption detection. Schedule tests cover wrap-midnight, DST
  spring-forward / fall-back, and union-of-overlapping-windows.
- **Integration**: `internal/store` migration tests (apply 0001..NNN against
  in-memory SQLite; verify schema; insert one row per table; round-trip
  through repos). API integration tests using the existing test harness
  (boot a `Core`, drive HTTP requests, assert DB state).
- **End-to-end smoke**: a single `teste2e/foundation_test.go` that exercises
  the full bootstrap → camera-add → record → event → webhook flow against
  a mocked camera RTSP source (or the existing test fixture).
- **Backwards compatibility**: none required (no shipped consumers).
- **Performance**: spot-check that the dual-insert into `cloud_outbox` does
  not degrade event ingestion at 10 events/sec sustained per camera × 16
  cameras.

---

## Acceptance criteria

The foundation is "done" when:

1. The MS-coupling files listed in Section 1 are removed and the binary
   builds, lints, and tests cleanly.
2. The full schema in Section 2 is applied via goose migrations on a fresh
   start.
3. First-run wizard completes from a clean install, leaving an active admin
   user, a self-signed cert, the seeded default policy, and `setup=complete`
   in mDNS TXT.
4. An operator can manually add a camera via API + UI, see live RTSP, see
   recorded segments, ack a synthetic event, and receive a webhook
   delivery within the configured backoff window.
5. RBAC denies a viewer-role user attempting to mutate a camera; the
   denial is in the audit log.
6. `cloud_outbox` accumulates rows, processor is the nop, sweeper trims
   rows older than 7d.
7. End-to-end smoke test passes in CI.
