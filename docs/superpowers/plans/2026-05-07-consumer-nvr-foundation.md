# Consumer NVR Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adapt the existing Raikada enterprise Recording Server fork into a standalone, single-tenant, single-site NVR for SMB and residential customers. Strip Management Server (MS) integration, expand the local SQLite schema, add RBAC, BYO TLS, weekly recording schedules, webhook+SMTP notifications, audit log, and a no-op cloud-bridge seam.

**Architecture:** Big-bang implementation on a feature branch / git worktree (Approach C from the spec). DB-canonical for cameras, policies, schedules, users, events. Nine new Go packages (`cameras`, `cameracred`, `events`, `notifications`, `outbox`, `schedule`, `rbac`, `retention`, `cloudbridge`) compose against the existing media pipeline (`recorder`, `recordstore`, `playback`, ONVIF stack) which stays unchanged on the hot path. Two outbox tables (`notification_outbox`, `cloud_outbox`) decouple delivery from durability.

**Tech Stack:** Go 1.25, SQLite via `modernc.org/sqlite` (pure Go), `pressly/goose/v3` migrations, `gin-gonic/gin` HTTP, `golang-jwt/jwt/v5` + ED25519, `matthewhartstonge/argon2`, `fsnotify` for cert hot-reload, `crypto/aes` GCM for credential vault, `html/template` for SMTP, existing MediaMTX-derived path manager / RTSP / HLS / WebRTC servers.

**Spec:** [`../specs/2026-05-07-consumer-nvr-foundation-design.md`](../specs/2026-05-07-consumer-nvr-foundation-design.md). When in doubt, the spec wins.

**Phase checkpoint commits:** Each phase ends with a verified-green commit; you can pause between phases for human review.

---

## Phase 0 — Worktree + branch setup

### Task 0.1: Create the foundation feature worktree

**Files:**
- Create branch: `consumer-foundation`
- Create worktree: `../NVR-consumer-foundation` (sibling of NVR/)

- [ ] **Step 1: Confirm clean working tree**

```bash
cd /Users/ethanflower/raikada-consumer/NVR
git status
```
Expected: `nothing to commit, working tree clean`. The spec commit `668faaf0` should be `HEAD`.

- [ ] **Step 2: Create the worktree on a new branch**

```bash
git worktree add -b consumer-foundation ../NVR-consumer-foundation main
cd ../NVR-consumer-foundation
git status
```
Expected: `On branch consumer-foundation`, working tree clean. All subsequent tasks operate inside `../NVR-consumer-foundation`.

- [ ] **Step 3: Verify the build still passes from the new worktree**

```bash
go build ./...
```
Expected: no output (success). If `astiav` or other CGO deps fail, that's a separate environment issue — surface and stop.

- [ ] **Step 4: Initial empty commit anchoring the branch**

```bash
git commit --allow-empty -m "chore: open consumer foundation branch (sub-project 1 of 4)"
```
Expected: commit created on `consumer-foundation`.

---

## Phase 1 — Schema migrations

All migrations live in `internal/store/migrations/`. The existing `0001_local_users.sql` and `0002_onvif_subscriptions.sql` are kept untouched. We add `0003` through `0022` in this phase. After all SQL files land, one smoke test confirms migrations apply cleanly and every new table is queryable.

Migrations follow the existing pattern: `-- +goose Up` and `-- +goose Down` sections, STRICT tables, `TEXT` UUIDv7 ids, `TEXT` RFC3339.millis timestamps. Reference: `internal/store/migrations/0001_local_users.sql` and `internal/store/store.go` (helpers `Now()`, `FormatTime()`, `ParseTime()`).

### Task 1.1: Auth migrations (roles + local_users extension)

**Files:**
- Create: `internal/store/migrations/0003_roles.sql`
- Create: `internal/store/migrations/0004_local_users_extend.sql`

- [ ] **Step 1: Write `0003_roles.sql`**

```sql
-- +goose Up

-- Two-role RBAC seed: admin (all permissions) and viewer (read-only).
-- The role -> permission map is in code (internal/rbac/), not in DB.
-- Promotion to DB-defined custom roles is a future migration.

CREATE TABLE roles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at TEXT NOT NULL
) STRICT;

INSERT INTO roles (id, name, description, created_at) VALUES
    ('role_admin',  'admin',  'Full control of cameras, policies, users, settings',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('role_viewer', 'viewer', 'Read-only access to cameras, recordings, events, clips',
     strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

-- +goose Down
DROP TABLE IF EXISTS roles;
```

- [ ] **Step 2: Write `0004_local_users_extend.sql`**

```sql
-- +goose Up

-- Extend local_users with role_id (FK to roles), email (for SMTP recipient),
-- language preference. The legacy is_admin column is no longer read; we keep
-- the column (SQLite has limited DROP COLUMN support before 3.35) and treat
-- role_id as authoritative. Backfill role_id from is_admin so any existing
-- bootstrap admins continue to work.

ALTER TABLE local_users ADD COLUMN role_id TEXT REFERENCES roles(id);
ALTER TABLE local_users ADD COLUMN email TEXT;
ALTER TABLE local_users ADD COLUMN language TEXT NOT NULL DEFAULT 'en';

UPDATE local_users
   SET role_id = CASE is_admin WHEN 1 THEN 'role_admin' ELSE 'role_viewer' END
 WHERE role_id IS NULL;

-- +goose Down
-- Cannot drop columns on older SQLite; leave columns in place on rollback.
UPDATE local_users SET role_id = NULL, email = NULL, language = 'en';
```

- [ ] **Step 3: Stage but do not commit yet (other migrations follow in Phase 1)**

```bash
git add internal/store/migrations/0003_roles.sql internal/store/migrations/0004_local_users_extend.sql
```

### Task 1.2: Camera migrations

**Files:**
- Create: `internal/store/migrations/0005_camera_groups.sql`
- Create: `internal/store/migrations/0006_cameras.sql`
- Create: `internal/store/migrations/0007_camera_credentials.sql`
- Create: `internal/store/migrations/0008_camera_capabilities.sql`
- Create: `internal/store/migrations/0009_camera_health.sql`

- [ ] **Step 1: Write `0005_camera_groups.sql`**

```sql
-- +goose Up

-- Optional organizational grouping (front, back, garage, ...). Cameras
-- have a nullable group_id; a camera with no group renders as ungrouped
-- in the UI. No constraint that groups must be non-empty.

CREATE TABLE camera_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_camera_groups_order ON camera_groups(display_order);

-- +goose Down
DROP INDEX IF EXISTS idx_camera_groups_order;
DROP TABLE IF EXISTS camera_groups;
```

- [ ] **Step 2: Write `0006_cameras.sql`**

```sql
-- +goose Up

-- Canonical camera entity. credentials live in camera_credentials (separate
-- row, encrypted at rest). source_url here is the camera-supplied template
-- WITHOUT userinfo; the recorder materializes the userinfo at use time via
-- internal/cameracred/Vault.MaterializeRTSPURL.

CREATE TABLE cameras (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT,
    group_id TEXT REFERENCES camera_groups(id),
    manufacturer TEXT,
    model TEXT,
    serial_number TEXT,
    firmware_version TEXT,
    mac_address TEXT,
    ip_address TEXT,
    hostname TEXT,
    source_type TEXT NOT NULL,
    source_url TEXT NOT NULL,
    onvif_xaddr TEXT,
    recording_policy_id TEXT REFERENCES recording_policies(id) DEFERRABLE INITIALLY DEFERRED,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    paired_at TEXT,
    last_capability_probe_at TEXT
) STRICT;

CREATE INDEX idx_cameras_group ON cameras(group_id);
CREATE INDEX idx_cameras_enabled ON cameras(enabled);

-- +goose Down
DROP INDEX IF EXISTS idx_cameras_enabled;
DROP INDEX IF EXISTS idx_cameras_group;
DROP TABLE IF EXISTS cameras;
```

Note the `DEFERRABLE INITIALLY DEFERRED` on `recording_policy_id` — `recording_policies` is created in 0010, after this migration. Deferred FK validation lets the migration land in dependency order without forcing us to reorder.

- [ ] **Step 3: Write `0007_camera_credentials.sql`**

```sql
-- +goose Up

-- Per-camera credentials, AES-GCM encrypted. The encryption key lives at
-- <identityDir>/cred.key (mode 0600) and is loaded by internal/cameracred.
-- One row per camera; cascades on camera deletion. ONVIF credentials are
-- separate columns because some cameras issue distinct accounts for
-- ONVIF vs RTSP.

CREATE TABLE camera_credentials (
    camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
    username TEXT NOT NULL,
    password_ciphertext BLOB NOT NULL,
    password_nonce BLOB NOT NULL,
    onvif_username TEXT,
    onvif_password_ciphertext BLOB,
    onvif_password_nonce BLOB,
    rotated_at TEXT NOT NULL,
    rotation_due_at TEXT
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS camera_credentials;
```

- [ ] **Step 4: Write `0008_camera_capabilities.sql`**

```sql
-- +goose Up

-- ONVIF / vendor capability snapshot. Populated by the capability probe
-- background worker (sub-project 2). profiles_json is the raw ONVIF
-- GetProfiles response, JSON-encoded; selected_profile_token is the one
-- the recorder is recording from.

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

-- +goose Down
DROP TABLE IF EXISTS camera_capabilities;
```

- [ ] **Step 5: Write `0009_camera_health.sql`**

```sql
-- +goose Up

-- One row per camera, upserted by internal/cameras/healthCollector
-- (sub-project 2). rtsp_state is the current path-manager source state.
-- last_keyframe_at and last_event_at are taps the recorder publishes to
-- the in-process bus.

CREATE TABLE camera_health (
    camera_id TEXT PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
    rtsp_state TEXT NOT NULL,
    last_keyframe_at TEXT,
    last_event_at TEXT,
    last_seen_at TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    updated_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS camera_health;
```

- [ ] **Step 6: Stage all five files**

```bash
git add internal/store/migrations/0005_camera_groups.sql \
        internal/store/migrations/0006_cameras.sql \
        internal/store/migrations/0007_camera_credentials.sql \
        internal/store/migrations/0008_camera_capabilities.sql \
        internal/store/migrations/0009_camera_health.sql
```

### Task 1.3: Recording policy migrations

**Files:**
- Create: `internal/store/migrations/0010_recording_policies.sql`
- Create: `internal/store/migrations/0011_recording_schedules.sql`

- [ ] **Step 1: Write `0010_recording_policies.sql`**

```sql
-- +goose Up

-- Canonical recording policies. Replaces the conf.RecordingPolicies map
-- as source of truth (legacy YAML map is logged-and-ignored at runtime).
-- The 'policy_default' row is seeded and cannot be deleted (enforced in
-- the API layer). pre/post_event_seconds default to 5 per Section 5.

CREATE TABLE recording_policies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    mode TEXT NOT NULL,                       -- continuous|motion|scheduled|off
    retention_duration_seconds INTEGER NOT NULL,
    container TEXT NOT NULL,                  -- fmp4|mpegts
    min_segment_duration_seconds INTEGER NOT NULL,
    max_segment_duration_seconds INTEGER NOT NULL,
    part_duration_ms INTEGER NOT NULL,
    max_part_size_bytes INTEGER NOT NULL,
    pre_event_seconds INTEGER NOT NULL DEFAULT 5,
    post_event_seconds INTEGER NOT NULL DEFAULT 5,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

INSERT INTO recording_policies (
    id, name, mode, retention_duration_seconds, container,
    min_segment_duration_seconds, max_segment_duration_seconds,
    part_duration_ms, max_part_size_bytes,
    pre_event_seconds, post_event_seconds, enabled,
    created_at, updated_at
) VALUES (
    'policy_default', 'Default Policy', 'continuous', 1209600, 'fmp4',
    60, 300, 1000, 1048576,
    5, 5, 1,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
);

-- +goose Down
DROP TABLE IF EXISTS recording_policies;
```

- [ ] **Step 2: Write `0011_recording_schedules.sql`**

```sql
-- +goose Up

-- Weekly time-of-day windows for scheduled-mode policies. Site timezone
-- comes from system_settings['timezone'] (not stored per-row). Wrap-
-- midnight is allowed (end_minute < start_minute means the window crosses
-- into the next day); the resolver in internal/schedule handles it.
-- Multiple windows on the same day are unioned.

CREATE TABLE recording_schedules (
    id TEXT PRIMARY KEY,
    policy_id TEXT NOT NULL REFERENCES recording_policies(id) ON DELETE CASCADE,
    day_of_week INTEGER NOT NULL,             -- 0=Sun..6=Sat
    start_minute INTEGER NOT NULL,            -- 0..1439 site-local
    end_minute INTEGER NOT NULL               -- exclusive; if < start, wraps midnight
) STRICT;

CREATE INDEX idx_schedules_policy ON recording_schedules(policy_id);

-- +goose Down
DROP INDEX IF EXISTS idx_schedules_policy;
DROP TABLE IF EXISTS recording_schedules;
```

- [ ] **Step 3: Stage**

```bash
git add internal/store/migrations/0010_recording_policies.sql \
        internal/store/migrations/0011_recording_schedules.sql
```

### Task 1.4: Event migrations (types, retention, events, snapshots)

**Files:**
- Create: `internal/store/migrations/0012_event_types.sql`
- Create: `internal/store/migrations/0013_event_retention.sql`
- Create: `internal/store/migrations/0014_events.sql`
- Create: `internal/store/migrations/0015_event_snapshots.sql`

- [ ] **Step 1: Write `0012_event_types.sql`**

```sql
-- +goose Up

-- Operator-extensible event type registry. Recorder seeds well-known
-- types on first boot via internal/events seedDefaults(); operators add
-- custom types via POST /v1/event-types with vendor='custom'.

CREATE TABLE event_types (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    vendor TEXT,                              -- onvif|amcrest|hikvision|reolink|internal|custom
    description TEXT
) STRICT;

INSERT INTO event_types (id, display_name, vendor, description) VALUES
    ('motion',         'Motion',          'internal', 'Motion detected on camera'),
    ('doorbell',       'Doorbell',        'internal', 'Doorbell pressed'),
    ('line_cross',     'Line Cross',      'onvif',    'ONVIF analytics line crossing'),
    ('tamper',         'Tamper',          'onvif',    'Camera tamper / scene change'),
    ('io_in',          'IO Input',        'onvif',    'Digital input asserted'),
    ('audio_alarm',    'Audio Alarm',     'onvif',    'Audio threshold exceeded'),
    ('person',         'Person',          'onvif',    'Person classification'),
    ('vehicle',        'Vehicle',         'onvif',    'Vehicle classification'),
    ('package',        'Package',         'onvif',    'Package classification'),
    ('animal',         'Animal',          'onvif',    'Animal classification'),
    ('camera_offline', 'Camera Offline',  'internal', 'RTSP source dropped'),
    ('camera_online',  'Camera Online',   'internal', 'RTSP source recovered');

-- +goose Down
DROP TABLE IF EXISTS event_types;
```

- [ ] **Step 2: Write `0013_event_retention.sql`**

```sql
-- +goose Up

-- Per-event-type retention. The special row id='__default__' is the
-- fallback for any type not explicitly listed; it's NOT a row in
-- event_types (NULL FK is fine because the constraint is on event_types
-- by FK from this table — see below: we make the FK nullable via the
-- absence of NOT NULL on type_id... wait, type_id IS the PK so it cannot
-- be NULL. We use a real type_id='__default__' row in event_types via a
-- separate seed in the seedDefaults helper, or we drop the FK on this
-- table). We choose: this table has NO FK to event_types so we can store
-- arbitrary type ids including '__default__'; the API layer enforces
-- "type must exist or be __default__" on writes.

CREATE TABLE event_retention (
    type_id TEXT PRIMARY KEY,
    keep_duration_seconds INTEGER NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

INSERT INTO event_retention (type_id, keep_duration_seconds, updated_at) VALUES
    ('__default__',    604800,  strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 7d
    ('motion',         7776000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 90d
    ('doorbell',       7776000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 90d
    ('line_cross',     2592000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 30d
    ('tamper',         7776000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 90d
    ('camera_offline', 2592000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),  -- 30d
    ('camera_online',  2592000, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));  -- 30d

-- +goose Down
DROP TABLE IF EXISTS event_retention;
```

- [ ] **Step 3: Write `0014_events.sql`**

```sql
-- +goose Up

-- Canonical CameraEvent records. id is UUIDv7 so ORDER BY id DESC equals
-- chronological. expires_at is materialized at insert time from
-- received_at + event_retention[type_id].keep_duration so retention sweep
-- is a fast index scan and changing retention does not retroactively
-- expire historical rows.

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    type_id TEXT NOT NULL REFERENCES event_types(id),
    source TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    severity TEXT,                            -- info|warning|critical
    payload_json TEXT,
    region_json TEXT,
    acknowledged_at TEXT,
    acknowledged_by TEXT REFERENCES local_users(id),
    expires_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_events_camera_time ON events(camera_id, occurred_at);
CREATE INDEX idx_events_type ON events(type_id);
CREATE INDEX idx_events_expires ON events(expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_events_expires;
DROP INDEX IF EXISTS idx_events_type;
DROP INDEX IF EXISTS idx_events_camera_time;
DROP TABLE IF EXISTS events;
```

- [ ] **Step 4: Write `0015_event_snapshots.sql`**

```sql
-- +goose Up

-- Disk-path-only event snapshots (no inline BLOB per spec Section 2).
-- Populated by the snapshot fetcher in sub-project 4. path is relative
-- to system_settings['snapshot_root'] (default <recordings_root>/snapshots).

CREATE TABLE event_snapshots (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,                       -- 'full' | 'thumb'
    width INTEGER,
    height INTEGER,
    path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    fetched_at TEXT NOT NULL,
    PRIMARY KEY (event_id, kind)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS event_snapshots;
```

- [ ] **Step 5: Stage**

```bash
git add internal/store/migrations/0012_event_types.sql \
        internal/store/migrations/0013_event_retention.sql \
        internal/store/migrations/0014_events.sql \
        internal/store/migrations/0015_event_snapshots.sql
```

### Task 1.5: Clip migrations

**Files:**
- Create: `internal/store/migrations/0016_clips.sql`
- Create: `internal/store/migrations/0017_clip_segments.sql`

- [ ] **Step 1: Write `0016_clips.sql`**

```sql
-- +goose Up

-- Clips are lazy-extraction handles. state goes requested -> preparing
-- -> ready (or failed). output_path is populated only on ready. event_id
-- is nullable (ad-hoc clips have no event); on the event being deleted,
-- the clip stays but its event link is nulled (SET NULL). On the camera
-- being deleted, clips cascade.

CREATE TABLE clips (
    id TEXT PRIMARY KEY,
    event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    start_time TEXT NOT NULL,
    end_time TEXT NOT NULL,
    pre_roll_seconds INTEGER NOT NULL DEFAULT 0,
    post_roll_seconds INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL,                      -- requested|preparing|ready|failed
    format TEXT NOT NULL DEFAULT 'fmp4',
    output_path TEXT,
    size_bytes INTEGER,
    checksum TEXT,
    created_by TEXT REFERENCES local_users(id),
    created_at TEXT NOT NULL,
    ready_at TEXT,
    expires_at TEXT
) STRICT;

CREATE INDEX idx_clips_camera_time ON clips(camera_id, start_time);
CREATE INDEX idx_clips_event ON clips(event_id);

-- +goose Down
DROP INDEX IF EXISTS idx_clips_event;
DROP INDEX IF EXISTS idx_clips_camera_time;
DROP TABLE IF EXISTS clips;
```

- [ ] **Step 2: Write `0017_clip_segments.sql`**

```sql
-- +goose Up

-- Many-to-many between clips and source recording segments. Populated by
-- the clip extractor (sub-project 4). segment_path is relative to the
-- recordstore root.

CREATE TABLE clip_segments (
    clip_id TEXT NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
    segment_path TEXT NOT NULL,
    start_offset_ms INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    PRIMARY KEY (clip_id, segment_path)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS clip_segments;
```

- [ ] **Step 3: Stage**

```bash
git add internal/store/migrations/0016_clips.sql \
        internal/store/migrations/0017_clip_segments.sql
```

### Task 1.6: Notification migrations

**Files:**
- Create: `internal/store/migrations/0018_notification_targets.sql`
- Create: `internal/store/migrations/0019_notification_subscriptions.sql`
- Create: `internal/store/migrations/0020_notification_outbox.sql`

- [ ] **Step 1: Write `0018_notification_targets.sql`**

```sql
-- +goose Up

-- Webhook URLs and SMTP recipients. webhook_secret is AES-GCM encrypted
-- (same vault as camera_credentials). For email targets, email_address
-- holds the recipient and the SMTP server config lives in system_settings
-- (one server per recorder; many recipients).

CREATE TABLE notification_targets (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,                       -- 'webhook' | 'email'
    name TEXT NOT NULL,
    webhook_url TEXT,
    webhook_secret_ciphertext BLOB,
    webhook_secret_nonce BLOB,
    email_address TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_notif_targets_enabled ON notification_targets(enabled);

-- +goose Down
DROP INDEX IF EXISTS idx_notif_targets_enabled;
DROP TABLE IF EXISTS notification_targets;
```

- [ ] **Step 2: Write `0019_notification_subscriptions.sql`**

```sql
-- +goose Up

-- Filter rows: a target receives an event iff a matching subscription
-- exists. Quiet hours live on the subscription (not the target) so one
-- target can power both an always-on critical-alerts subscription and a
-- daytime-only info-events subscription.

CREATE TABLE notification_subscriptions (
    id TEXT PRIMARY KEY,
    target_id TEXT NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
    event_type_id TEXT REFERENCES event_types(id),  -- nullable = all types
    camera_id TEXT REFERENCES cameras(id),           -- nullable = all cameras
    min_severity TEXT,                               -- nullable = all severities
    quiet_hours_start_minute INTEGER,
    quiet_hours_end_minute INTEGER
) STRICT;

CREATE INDEX idx_notif_subs_target ON notification_subscriptions(target_id);
CREATE INDEX idx_notif_subs_camera ON notification_subscriptions(camera_id);

-- +goose Down
DROP INDEX IF EXISTS idx_notif_subs_camera;
DROP INDEX IF EXISTS idx_notif_subs_target;
DROP TABLE IF EXISTS notification_subscriptions;
```

- [ ] **Step 3: Write `0020_notification_outbox.sql`**

```sql
-- +goose Up

-- Pending and in-flight deliveries. The outbox processor (internal/outbox)
-- picks rows where state IN ('pending') AND next_attempt_at <= now(). On
-- delivery, state='delivered'. On 4xx, state='dead'. On 5xx/timeout,
-- attempts++ and state stays 'pending' with next_attempt_at = now() +
-- backoff(attempts). After max_attempts (default 8), state='dead'.

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY,
    target_id TEXT NOT NULL REFERENCES notification_targets(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL,                      -- pending|in_flight|delivered|failed|dead
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT
) STRICT;

CREATE INDEX idx_outbox_state_next ON notification_outbox(state, next_attempt_at);

-- +goose Down
DROP INDEX IF EXISTS idx_outbox_state_next;
DROP TABLE IF EXISTS notification_outbox;
```

- [ ] **Step 4: Stage**

```bash
git add internal/store/migrations/0018_notification_targets.sql \
        internal/store/migrations/0019_notification_subscriptions.sql \
        internal/store/migrations/0020_notification_outbox.sql
```

### Task 1.7: Audit log + cloud outbox + system settings

**Files:**
- Create: `internal/store/migrations/0021_audit_log.sql`
- Create: `internal/store/migrations/0022_cloud_outbox.sql`
- Create: `internal/store/migrations/0023_system_settings.sql`

- [ ] **Step 1: Write `0021_audit_log.sql`**

```sql
-- +goose Up

-- Append-only mutation log. Never expires by default; admin-only manual
-- purge endpoint exists. Redaction is enforced at write time by the
-- audit emit helpers (per-domain explicit allow-list).

CREATE TABLE audit_log (
    id TEXT PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    actor_user_id TEXT REFERENCES local_users(id),
    actor_username TEXT,
    actor_ip TEXT,
    action TEXT NOT NULL,
    target_kind TEXT,
    target_id TEXT,
    before_json TEXT,
    after_json TEXT,
    details TEXT
) STRICT;

CREATE INDEX idx_audit_occurred ON audit_log(occurred_at);
CREATE INDEX idx_audit_actor ON audit_log(actor_user_id, occurred_at);
CREATE INDEX idx_audit_action ON audit_log(action, occurred_at);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_action;
DROP INDEX IF EXISTS idx_audit_actor;
DROP INDEX IF EXISTS idx_audit_occurred;
DROP TABLE IF EXISTS audit_log;
```

- [ ] **Step 2: Write `0022_cloud_outbox.sql`**

```sql
-- +goose Up

-- Cloud-bridge seam. Same shape as notification_outbox. Processor in
-- internal/cloudbridge runs only when system_settings['cloud_endpoint']
-- is non-empty; otherwise a sweeper trims rows older than
-- cloud_outbox_horizon_hours (default 168 = 7d) so unconfigured installs
-- do not grow unboundedly.

CREATE TABLE cloud_outbox (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,                       -- event|health|audit
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT
) STRICT;

CREATE INDEX idx_cloud_outbox_state ON cloud_outbox(state, next_attempt_at);
CREATE INDEX idx_cloud_outbox_created ON cloud_outbox(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_cloud_outbox_created;
DROP INDEX IF EXISTS idx_cloud_outbox_state;
DROP TABLE IF EXISTS cloud_outbox;
```

- [ ] **Step 3: Write `0023_system_settings.sql`**

```sql
-- +goose Up

-- Site-wide k/v config. Seeded with sensible defaults; operators override
-- via PATCH /v1/system/settings. timezone is set from time.Local at first
-- boot via internal/core seeding (cannot be done in pure SQL).

CREATE TABLE system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    updated_by TEXT REFERENCES local_users(id)
) STRICT;

INSERT INTO system_settings (key, value, updated_at, updated_by) VALUES
    ('site_name',                       'My NVR',         strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('language',                        'en',             strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('lockout_threshold',               '5',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('lockout_duration_minutes',        '15',             strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('capability_probe_interval_hours', '24',             strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('health_debounce_seconds',         '5',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('default_policy_id',               'policy_default', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('outbox_workers',                  '4',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('outbox_max_attempts',             '8',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('outbox_request_timeout_seconds',  '5',              strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('cloud_outbox_horizon_hours',      '168',            strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('cloud_endpoint',                  '',               strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('snapshot_root',                   '',               strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('clip_root',                       '',               strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL),
    ('webhook_signature_scheme',        'simple',         strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL);

-- timezone, smtp_*, cloud_token_*, mdns_service_name are seeded at
-- runtime by internal/core because they need host introspection or
-- envelope-encryption that pure SQL cannot do.

-- +goose Down
DROP TABLE IF EXISTS system_settings;
```

- [ ] **Step 4: Stage**

```bash
git add internal/store/migrations/0021_audit_log.sql \
        internal/store/migrations/0022_cloud_outbox.sql \
        internal/store/migrations/0023_system_settings.sql
```

### Task 1.8: Migration smoke test

**Files:**
- Modify: `internal/store/store.go` (add accessor placeholders so tests compile — full Repos land in Phase 2; for now we just add a sanity-check method)
- Create: `internal/store/migrations_test.go`

- [ ] **Step 1: Write the failing smoke test**

```go
// internal/store/migrations_test.go
package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigrations_AllTablesExist verifies that opening the DB applies every
// migration through 0023 and creates each expected table. We probe each
// table with `SELECT COUNT(*)` (returns 0 on empty); if a table is missing
// SQLite returns "no such table" which fails the test.
func TestMigrations_AllTablesExist(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "recorder.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	tables := []string{
		// 0001-0002 (existing)
		"local_users",
		"onvif_subscriptions",
		// 0003-0023 (new)
		"roles",
		"camera_groups",
		"cameras",
		"camera_credentials",
		"camera_capabilities",
		"camera_health",
		"recording_policies",
		"recording_schedules",
		"event_types",
		"event_retention",
		"events",
		"event_snapshots",
		"clips",
		"clip_segments",
		"notification_targets",
		"notification_subscriptions",
		"notification_outbox",
		"audit_log",
		"cloud_outbox",
		"system_settings",
	}
	ctx := context.Background()
	for _, table := range tables {
		var n int
		row := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table)
		if err := row.Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
}

// TestMigrations_SeedRows verifies that the migrations that include INSERT
// statements left their rows behind.
func TestMigrations_SeedRows(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "recorder.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	cases := []struct {
		query    string
		expected int
		desc     string
	}{
		{"SELECT COUNT(*) FROM roles", 2, "roles seeded with admin + viewer"},
		{"SELECT COUNT(*) FROM recording_policies WHERE id='policy_default'", 1, "default policy seeded"},
		{"SELECT COUNT(*) FROM event_types", 12, "event types seeded"},
		{"SELECT COUNT(*) FROM event_retention WHERE type_id='__default__'", 1, "default retention seeded"},
		{"SELECT COUNT(*) FROM system_settings WHERE key='lockout_threshold'", 1, "system_settings seeded"},
	}
	for _, tc := range cases {
		var n int
		if err := s.DB.QueryRow(tc.query).Scan(&n); err != nil {
			t.Errorf("%s: %v", tc.desc, err)
			continue
		}
		if n != tc.expected {
			t.Errorf("%s: expected %d rows, got %d", tc.desc, tc.expected, n)
		}
	}
}
```

- [ ] **Step 2: Run the test — it should currently fail until migrations are present**

```bash
cd /Users/ethanflower/raikada-consumer/NVR-consumer-foundation
go test ./internal/store/ -run TestMigrations -v
```
Expected: PASS (since the SQL files were already added in Tasks 1.1–1.7). If FAIL with "no such table", the migration file content is wrong — re-read the task and fix.

- [ ] **Step 3: Stage the test**

```bash
git add internal/store/migrations_test.go
```

### Task 1.9: Phase 1 commit

- [ ] **Step 1: Verify the full store package tests still pass**

```bash
go test ./internal/store/ -v
```
Expected: every existing `TestLocalUsers_*`, `TestOnvifSubscriptions_*` test plus the two new `TestMigrations_*` tests pass.

- [ ] **Step 2: Commit Phase 1**

```bash
git commit -m "$(cat <<'EOF'
feat(store): add foundation schema migrations 0003-0023

Add 21 migrations landing the consumer NVR foundation schema: roles,
camera_groups, cameras, camera_credentials, camera_capabilities,
camera_health, recording_policies (+ seed default), recording_schedules,
event_types (+ seed well-knowns), event_retention (+ seed defaults),
events, event_snapshots, clips, clip_segments, notification_targets,
notification_subscriptions, notification_outbox, audit_log, cloud_outbox,
system_settings (+ seed defaults). Extends local_users with role_id,
email, language.

Migration smoke test verifies all tables exist and seed rows landed.

Spec: docs/superpowers/specs/2026-05-07-consumer-nvr-foundation-design.md
Plan: docs/superpowers/plans/2026-05-07-consumer-nvr-foundation.md
EOF
)"
```

---

## Phase 2 — Store repos (DAOs)

One task per new repo. Pattern follows `internal/store/local_users.go` exactly: define a struct, a `*Repo` with `db *sql.DB`, CRUD methods using `context.Context`, a `scanXxx` helper that takes a `rowScanner`, and a `const xxxSelect` SQL fragment. Each task is TDD: write the table-driven test first, run, implement, run, commit.

For every Repo, accessor wiring happens in `internal/store/store.go` — add a field to `Store{}` and initialize in `Open()`.

### Task 2.1: RolesRepo

**Files:**
- Create: `internal/store/roles.go`
- Create: `internal/store/roles_test.go`
- Modify: `internal/store/store.go` (add `Roles *RolesRepo` field + initializer)

- [ ] **Step 1: Write the failing test**

```go
// internal/store/roles_test.go
package store

import (
	"context"
	"testing"
)

func TestRoles_GetByID(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	got, err := s.Roles.GetByID(ctx, "role_admin")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if got.Name != "admin" {
		t.Errorf("expected name=admin, got %q", got.Name)
	}

	got, err = s.Roles.GetByID(ctx, "role_viewer")
	if err != nil {
		t.Fatalf("get viewer: %v", err)
	}
	if got.Name != "viewer" {
		t.Errorf("expected name=viewer, got %q", got.Name)
	}
}

func TestRoles_GetByID_NotFound(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.Roles.GetByID(context.Background(), "role_nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRoles_List(t *testing.T) {
	s := mustOpenStore(t)
	got, err := s.Roles.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 roles, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run — should fail "undefined: s.Roles"**

```bash
go test ./internal/store/ -run TestRoles -v
```
Expected: build error.

- [ ] **Step 3: Write `internal/store/roles.go`**

```go
package store

import (
	"context"
	"database/sql"
	"time"
)

// Role is a row in roles. The role->permission map lives in internal/rbac,
// not here; this struct is just storage shape.
type Role struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
}

type RolesRepo struct {
	db *sql.DB
}

func (r *RolesRepo) GetByID(ctx context.Context, id string) (*Role, error) {
	row := r.db.QueryRowContext(ctx, roleSelect+` WHERE id = ?`, id)
	return scanRole(row)
}

func (r *RolesRepo) GetByName(ctx context.Context, name string) (*Role, error) {
	row := r.db.QueryRowContext(ctx, roleSelect+` WHERE name = ?`, name)
	return scanRole(row)
}

func (r *RolesRepo) List(ctx context.Context) ([]*Role, error) {
	rows, err := r.db.QueryContext(ctx, roleSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		ro, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, rows.Err()
}

const roleSelect = `SELECT id, name, COALESCE(description, ''), created_at FROM roles`

func scanRole(row rowScanner) (*Role, error) {
	var ro Role
	var createdAt string
	if err := row.Scan(&ro.ID, &ro.Name, &ro.Description, &createdAt); err != nil {
		return nil, err
	}
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	ro.CreatedAt = t
	return &ro, nil
}
```

- [ ] **Step 4: Wire into `Store{}` in `internal/store/store.go`**

Find the existing `Store` struct and the `Open()` initializer. Add the field and initializer line (the surrounding code is in `internal/store/store.go` as shown earlier):

```go
// In the Store struct definition:
Roles *RolesRepo

// In Open(), after `s := &Store{DB: db}`:
s.Roles = &RolesRepo{db: db}
```

Use Edit to insert these two lines beside the existing `LocalUsers` and `OnvifSubscriptions` lines, preserving alphabetical order.

- [ ] **Step 5: Run — should pass**

```bash
go test ./internal/store/ -run TestRoles -v
```
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/store/roles.go internal/store/roles_test.go internal/store/store.go
git commit -m "feat(store): add RolesRepo"
```

### Task 2.2: LocalUsers role/email/language helpers

**Files:**
- Modify: `internal/store/local_users.go` (extend `LocalUser` struct + add three new methods + extend scan helper)
- Modify: `internal/store/local_users_test.go` (extend tests for new fields)

- [ ] **Step 1: Add new fields to `LocalUser` struct**

In `internal/store/local_users.go`, find the `LocalUser` struct definition (lines ~12-25) and add three fields:

```go
type LocalUser struct {
	ID                  string
	Username            string
	DisplayName         string
	PasswordHash        string
	IsAdmin             bool                // legacy; prefer RoleID
	IsActive            bool
	MustChangePassword  bool
	RoleID              string              // new
	Email               string              // new
	Language            string              // new
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastLoginAt         time.Time
	FailedLoginAttempts int
	LockedUntil         time.Time
}
```

- [ ] **Step 2: Update `localUserSelect` and `scanLocalUser` to include new columns**

```go
const localUserSelect = `
SELECT id, username, COALESCE(display_name, ''), password_hash, is_admin,
       is_active, must_change_password,
       COALESCE(role_id, ''), COALESCE(email, ''), language,
       created_at, updated_at, COALESCE(last_login_at, ''),
       failed_login_attempts, COALESCE(locked_until, '')
FROM local_users
`

func scanLocalUser(row rowScanner) (*LocalUser, error) {
	var u LocalUser
	var createdAt, updatedAt, lastLoginAt, lockedUntil string
	var isAdmin, isActive, mustChange int
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&isAdmin, &isActive, &mustChange,
		&u.RoleID, &u.Email, &u.Language,
		&createdAt, &updatedAt, &lastLoginAt,
		&u.FailedLoginAttempts, &lockedUntil); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.IsActive = isActive != 0
	u.MustChangePassword = mustChange != 0
	t, err := ParseTime(createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = t
	t, err = ParseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	u.UpdatedAt = t
	if lastLoginAt != "" {
		t, _ = ParseTime(lastLoginAt)
		u.LastLoginAt = t
	}
	if lockedUntil != "" {
		t, _ = ParseTime(lockedUntil)
		u.LockedUntil = t
	}
	return &u, nil
}
```

- [ ] **Step 3: Extend `Insert` to set `role_id`, `email`, `language`**

```go
func (r *LocalUsersRepo) Insert(ctx context.Context, u *LocalUser) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = now
	}
	if u.Language == "" {
		u.Language = "en"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO local_users (id, username, display_name, password_hash,
		                          is_admin, is_active, must_change_password,
		                          role_id, email, language,
		                          created_at, updated_at,
		                          failed_login_attempts)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, 0)
	`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash,
		boolToInt(u.IsAdmin), boolToInt(u.IsActive), boolToInt(u.MustChangePassword),
		u.RoleID, u.Email, u.Language,
		FormatTime(u.CreatedAt), FormatTime(u.UpdatedAt),
	)
	if err != nil && isConstraintErr(err) {
		return ErrLocalUserExists
	}
	return err
}
```

- [ ] **Step 4: Add three new mutator methods**

Append to `internal/store/local_users.go`:

```go
// SetRole updates role_id (must be a valid roles.id, FK enforced).
func (r *LocalUsersRepo) SetRole(ctx context.Context, id, roleID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET role_id = ?, updated_at = ? WHERE id = ?
	`, roleID, Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

// SetEmail updates the user's email (empty string clears it).
func (r *LocalUsersRepo) SetEmail(ctx context.Context, id, email string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET email = NULLIF(?, ''), updated_at = ? WHERE id = ?
	`, email, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}

// SetLanguage updates the user's language preference.
func (r *LocalUsersRepo) SetLanguage(ctx context.Context, id, lang string) error {
	if lang == "" {
		lang = "en"
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE local_users SET language = ?, updated_at = ? WHERE id = ?
	`, lang, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLocalUserNotFound
	}
	return nil
}
```

- [ ] **Step 5: Extend the existing local_users test**

In `internal/store/local_users_test.go`, append:

```go
func TestLocalUsers_RoleEmailLanguage(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	u := &LocalUser{
		ID:           uuid.NewString(),
		Username:     "alice",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$AAAA$BBBB",
		IsActive:     true,
		RoleID:       "role_admin",
		Email:        "alice@example.com",
		Language:     "en",
	}
	if err := s.LocalUsers.Insert(ctx, u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.LocalUsers.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RoleID != "role_admin" || got.Email != "alice@example.com" || got.Language != "en" {
		t.Errorf("got role=%q email=%q lang=%q", got.RoleID, got.Email, got.Language)
	}

	if err := s.LocalUsers.SetRole(ctx, u.ID, "role_viewer"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := s.LocalUsers.SetEmail(ctx, u.ID, "alice2@example.com"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if err := s.LocalUsers.SetLanguage(ctx, u.ID, "es"); err != nil {
		t.Fatalf("set lang: %v", err)
	}
	got, _ = s.LocalUsers.GetByID(ctx, u.ID)
	if got.RoleID != "role_viewer" || got.Email != "alice2@example.com" || got.Language != "es" {
		t.Errorf("post-update got role=%q email=%q lang=%q", got.RoleID, got.Email, got.Language)
	}
}
```

- [ ] **Step 6: Run**

```bash
go test ./internal/store/ -run TestLocalUsers -v
```
Expected: all `TestLocalUsers_*` (existing + new) pass.

- [ ] **Step 7: Commit**

```bash
git add internal/store/local_users.go internal/store/local_users_test.go
git commit -m "feat(store): extend LocalUser with role_id, email, language"
```

### DAO authoring template (applies to Tasks 2.3 through 2.21)

Every remaining repo follows this exact structure. Each task spells out the table-specific deltas; the boilerplate is identical.

**File `internal/store/<table>.go`:**

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// <Entity> is a row in <table>.
type <Entity> struct {
	// fields matching the table columns; TEXT timestamps land as time.Time
	// via ParseTime; INTEGER booleans use bool with boolToInt on insert.
}

type <Entity>Repo struct {
	db *sql.DB
}

// CRUD methods — at minimum: Insert, GetByID (or composite key), List
// (paginated where the table can grow large), Update (or per-field
// setters where partial update matters), Delete (or SoftDelete).

func (r *<Entity>Repo) Insert(ctx context.Context, e *<Entity>) error { ... }
func (r *<Entity>Repo) GetByID(ctx context.Context, id string) (*<Entity>, error) { ... }
func (r *<Entity>Repo) List(ctx context.Context, /* filter args */) ([]*<Entity>, error) { ... }
func (r *<Entity>Repo) Update(ctx context.Context, e *<Entity>) error { ... }
func (r *<Entity>Repo) Delete(ctx context.Context, id string) error { ... }

const <entity>Select = `SELECT ... FROM <table>`

func scan<Entity>(row rowScanner) (*<Entity>, error) { ... }

// Sentinel errors:
var Err<Entity>NotFound = errors.New("<entity> not found")
var Err<Entity>Exists   = errors.New("<entity> already exists")
```

**File `internal/store/<table>_test.go`** — minimum cases per repo:
- Insert + GetByID round-trip
- GetByID for non-existent id returns sql.ErrNoRows or sentinel
- List returns inserted rows
- Update mutates and re-read sees the change
- Delete removes and subsequent GetByID errors

**Wire-up in `internal/store/store.go`:**
- Add `<Entity>s *<Entity>Repo` to `Store` struct
- In `Open()`: `s.<Entity>s = &<Entity>Repo{db: db}`

Per-task workflow (apply for every Task 2.3 onward):

```text
1. Write the test file (full table-driven cases)
2. Run: go test ./internal/store/ -run Test<Entity> -v   → expect compile/runtime fail
3. Write the repo file (struct + Repo + methods + scan helper + sentinels)
4. Wire field + initializer in store.go
5. Run again → expect PASS
6. git add internal/store/<table>.go internal/store/<table>_test.go internal/store/store.go
   git commit -m "feat(store): add <Entity>Repo"
```

The remaining tasks call out the table-specific shape; everything else inherits this template.

### Task 2.3: CameraGroupsRepo

**Table:** `camera_groups` (migration 0005). Simple lookup table.

```go
type CameraGroup struct {
	ID           string
	Name         string
	DisplayOrder int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

**Methods:** `Insert`, `GetByID`, `GetByName`, `List` (ordered by `display_order`, then `name`), `Update` (replaces name + display_order), `Delete`.

**Sentinels:** `ErrCameraGroupExists` (UNIQUE on name), `ErrCameraGroupNotFound`.

**Test cases:** insert + get + list + rename + delete; insert duplicate name returns `ErrCameraGroupExists`.

Apply the DAO authoring template, commit `feat(store): add CameraGroupsRepo`.

### Task 2.4: CamerasRepo

**Table:** `cameras` (migration 0006). The most-mutated table at runtime.

```go
type Camera struct {
	ID                    string
	Name                  string
	DisplayName           string
	GroupID               string  // empty string when NULL
	Manufacturer          string
	Model                 string
	SerialNumber          string
	FirmwareVersion       string
	MACAddress            string
	IPAddress             string
	Hostname              string
	SourceType            string  // 'rtsp'|'rtsps'|'rtmp'|'hls'|'onvif'
	SourceURL             string  // template, no userinfo
	OnvifXAddr            string
	RecordingPolicyID     string  // empty string when NULL → resolver falls back to policy_default
	Enabled               bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
	PairedAt              time.Time  // zero if never paired
	LastCapabilityProbeAt time.Time  // zero if never probed
}
```

**Methods:**
- `Insert(ctx, *Camera) error` — assigns `CreatedAt`/`UpdatedAt` if zero
- `GetByID(ctx, id) (*Camera, error)`
- `GetByName(ctx, name) (*Camera, error)`
- `List(ctx, ListCamerasFilter) ([]*Camera, error)` — filter struct holds optional `GroupID`, `Enabled *bool`, pagination cursor + limit
- `Update(ctx, *Camera) error` — full-row update, sets `UpdatedAt = Now()`
- `SetEnabled(ctx, id string, enabled bool) error`
- `SetFirmwareVersion(ctx, id, version string) error` (called by capability probe)
- `SetPaired(ctx, id string, t time.Time) error`
- `SetLastCapabilityProbe(ctx, id string, t time.Time) error`
- `Delete(ctx, id) error` — schema cascades take care of credentials/capabilities/health/events/clips/snapshots; the `cameras.Service` (Phase 3) is responsible for tearing down ONVIF subscriptions BEFORE calling this

**Sentinels:** `ErrCameraExists` (UNIQUE on name), `ErrCameraNotFound`.

**Test cases:**
- Round-trip insert + GetByID with all-fields populated and with optional fields empty
- GetByName
- List filtered by GroupID and by Enabled
- Update mutates and re-read confirms
- SetFirmwareVersion / SetPaired / SetLastCapabilityProbe each isolated
- Delete returns `ErrCameraNotFound` on second invocation

Apply the DAO authoring template, commit `feat(store): add CamerasRepo`.

### Task 2.5: CameraCredentialsRepo

**Table:** `camera_credentials` (migration 0007). The vault writes ciphertext+nonce produced by `internal/cameracred` (Phase 3); the repo is opaque to encryption.

```go
type CameraCredentials struct {
	CameraID                 string
	Username                 string
	PasswordCiphertext       []byte
	PasswordNonce            []byte
	OnvifUsername            string
	OnvifPasswordCiphertext  []byte
	OnvifPasswordNonce       []byte
	RotatedAt                time.Time
	RotationDueAt            time.Time  // zero if not set
}
```

**Methods:**
- `Upsert(ctx, *CameraCredentials) error` — single statement: `INSERT … ON CONFLICT(camera_id) DO UPDATE SET …`. Sets `RotatedAt = Now()` on every call.
- `Get(ctx, cameraID) (*CameraCredentials, error)`
- `Exists(ctx, cameraID) (bool, error)` — used by Camera serializers to populate `password_set` boolean without decrypting

**Sentinels:** `ErrCameraCredentialsNotFound`.

**Test cases:**
- Upsert + Get round-trip with both RTSP and ONVIF creds
- Upsert + Get with only RTSP creds (ONVIF columns nil)
- Upsert again replaces and bumps RotatedAt
- Exists returns true after upsert, false before

Apply the DAO authoring template, commit `feat(store): add CameraCredentialsRepo`.

### Task 2.6: CameraCapabilitiesRepo

**Table:** `camera_capabilities` (migration 0008).

```go
type CameraCapabilities struct {
	CameraID               string
	ProfilesJSON           string  // raw ONVIF GetProfiles response
	SelectedProfileToken   string
	HasAudio               bool
	HasPTZ                 bool
	HasMotion              bool
	HasIO                  bool
	HasImaging             bool
	VendorCapabilitiesJSON string
	ProbedAt               time.Time
}
```

**Methods:** `Upsert`, `Get`, `Delete` (cascade carries it via FK on camera delete; explicit Delete only used for diagnostics).

**Test cases:** upsert + get round-trip; upsert replaces.

Apply the DAO authoring template, commit `feat(store): add CameraCapabilitiesRepo`.

### Task 2.7: CameraHealthRepo

**Table:** `camera_health` (migration 0009). Hot path — upserts on every state change.

```go
type CameraHealth struct {
	CameraID            string
	RTSPState           string  // 'connected'|'reconnecting'|'failed'|'idle'
	LastKeyframeAt      time.Time
	LastEventAt         time.Time
	LastSeenAt          time.Time
	ConsecutiveFailures int
	LastError           string
	UpdatedAt           time.Time
}
```

**Methods:**
- `Upsert(ctx, *CameraHealth) error` — sets `UpdatedAt = Now()`
- `Get(ctx, cameraID) (*CameraHealth, error)`
- `IncrementFailure(ctx, cameraID, errMsg string) error` — atomic counter bump + error message + UpdatedAt
- `ResetFailures(ctx, cameraID string) error` — sets `consecutive_failures=0`, clears `last_error`
- `TouchKeyframe(ctx, cameraID string, t time.Time) error` — sets `last_keyframe_at`, optionally also `last_seen_at = t`
- `TouchEvent(ctx, cameraID string, t time.Time) error`

**Test cases:** upsert + get; increment; reset; touch keyframe / event each isolated.

Apply the DAO authoring template, commit `feat(store): add CameraHealthRepo`.

### Task 2.8: RecordingPoliciesRepo

**Table:** `recording_policies` (migration 0010). Already-seeded `policy_default` is referenced by tests.

```go
type RecordingPolicy struct {
	ID                        string
	Name                      string
	Mode                      string  // 'continuous'|'motion'|'scheduled'|'off'
	RetentionDurationSeconds  int64
	Container                 string  // 'fmp4'|'mpegts'
	MinSegmentDurationSeconds int64
	MaxSegmentDurationSeconds int64
	PartDurationMS            int64
	MaxPartSizeBytes          int64
	PreEventSeconds           int
	PostEventSeconds          int
	Enabled                   bool
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}
```

**Methods:** `Insert`, `GetByID`, `GetByName`, `List`, `Update`, `Delete` (returns `ErrPolicyIsDefault` when `id='policy_default'`).

**Sentinels:** `ErrRecordingPolicyExists`, `ErrRecordingPolicyNotFound`, `ErrPolicyIsDefault`.

**Test cases:**
- `policy_default` exists after Open (verify in test using GetByID)
- Insert + Get round-trip for a custom policy
- Update mutates and re-read confirms
- Delete custom policy works
- Delete `policy_default` returns `ErrPolicyIsDefault`

Apply the DAO authoring template, commit `feat(store): add RecordingPoliciesRepo`.

### Task 2.9: RecordingSchedulesRepo

**Table:** `recording_schedules` (migration 0011).

```go
type RecordingSchedule struct {
	ID          string
	PolicyID    string
	DayOfWeek   int  // 0=Sun..6=Sat
	StartMinute int  // 0..1439
	EndMinute   int  // exclusive; if < start, wraps midnight
}
```

**Methods:**
- `ListByPolicy(ctx, policyID) ([]*RecordingSchedule, error)`
- `ReplaceAllForPolicy(ctx, policyID string, schedules []*RecordingSchedule) error` — atomic: in a tx, DELETE all rows with that policy_id, then INSERT new ones. Used by `PUT /v1/recording-policies/{id}/schedules`.
- `DeleteByPolicy(ctx, policyID) error` — used as a helper during policy deletion (cascade also fires via FK)

**Test cases:** ReplaceAllForPolicy idempotent; ListByPolicy returns inserted rows in deterministic order (sort by `day_of_week, start_minute`).

Apply the DAO authoring template, commit `feat(store): add RecordingSchedulesRepo`.

### Task 2.10: EventTypesRepo

**Table:** `event_types` (migration 0012). Operator-extensible.

```go
type EventType struct {
	ID          string
	DisplayName string
	Vendor      string  // empty for the special '__default__' is N/A; only event_retention has '__default__'
	Description string
}
```

**Methods:** `Insert` (vendor must be one of the allowed enum values), `GetByID`, `List`, `Update` (only DisplayName + Description; vendor is immutable), `Delete` (rejects deletion of the seeded well-knowns by checking `vendor != 'custom'` → returns `ErrEventTypeProtected`).

**Sentinels:** `ErrEventTypeExists`, `ErrEventTypeNotFound`, `ErrEventTypeProtected`.

**Test cases:**
- All seeded types present after Open
- Insert custom + Get + List
- Update display_name on a custom; original vendor preserved
- Delete custom works; Delete seeded ('motion') returns `ErrEventTypeProtected`

Apply the DAO authoring template, commit `feat(store): add EventTypesRepo`.

### Task 2.11: EventRetentionRepo

**Table:** `event_retention` (migration 0013).

```go
type EventRetention struct {
	TypeID              string  // includes '__default__' as a real row
	KeepDurationSeconds int64
	UpdatedAt           time.Time
}
```

**Methods:**
- `Get(ctx, typeID) (*EventRetention, error)` — caller falls back to `__default__` on `sql.ErrNoRows`
- `GetEffective(ctx, typeID string) (time.Duration, error)` — convenience wrapper that does the fallback to `__default__` internally
- `Upsert(ctx, *EventRetention) error`
- `List(ctx) ([]*EventRetention, error)`

**Test cases:** seeded `__default__` exists; GetEffective returns the type-specific duration when present, falls back to `__default__` otherwise.

Apply the DAO authoring template, commit `feat(store): add EventRetentionRepo`.

### Task 2.12: EventsRepo

**Table:** `events` (migration 0014). Hot path — heavy reads + sustained writes.

```go
type Event struct {
	ID             string
	CameraID       string
	TypeID         string
	Source         string
	OccurredAt     time.Time
	ReceivedAt     time.Time
	Severity       string  // empty string when NULL
	PayloadJSON    string  // raw JSON
	RegionJSON     string  // raw JSON
	AcknowledgedAt time.Time  // zero if not acked
	AcknowledgedBy string     // empty if not acked
	ExpiresAt      time.Time
}

type ListEventsFilter struct {
	CameraIDs  []string  // empty = any
	TypeIDs    []string  // empty = any
	Sources    []string  // empty = any
	From, To   time.Time // zero = unbounded
	MinSev     string    // empty = any
	OnlyUnack  bool
	Cursor     string    // last seen id (UUIDv7 sortable)
	Limit      int       // capped at 500
}
```

**Methods:**
- `Insert(ctx, *Event) error` — caller materializes ExpiresAt before calling; this method just writes
- `GetByID(ctx, id) (*Event, error)`
- `List(ctx, ListEventsFilter) (page []*Event, nextCursor string, err error)` — orders by `id DESC` (UUIDv7 = chronological); cursor is the last id from the previous page
- `Acknowledge(ctx, id, userID string) error` — sets `acknowledged_at = Now()`, `acknowledged_by = userID`
- `DeleteExpired(ctx, batchSize int) (int, error)` — used by retention sweeper; deletes up to batchSize rows where `expires_at < Now()`, returns count
- `CountByCameraSince(ctx, cameraID string, since time.Time) (int, error)` — used by health/notification dashboards

**Sentinels:** `ErrEventNotFound`.

**Test cases:**
- Insert + GetByID round-trip with both `severity` populated and empty
- List with various filters (camera, type, time-range, only-unack)
- Cursor pagination returns expected slices and stable order
- Acknowledge sets fields; double-ack is idempotent (no error, no side effect)
- DeleteExpired removes only past-expiry rows; returns the count

Apply the DAO authoring template, commit `feat(store): add EventsRepo`.

### Task 2.13: EventSnapshotsRepo

**Table:** `event_snapshots` (migration 0015). Composite PK `(event_id, kind)`.

```go
type EventSnapshot struct {
	EventID   string
	Kind      string  // 'full' | 'thumb'
	Width     int
	Height    int
	Path      string
	SizeBytes int64
	FetchedAt time.Time
}
```

**Methods:** `Insert`, `Get(ctx, eventID, kind)`, `ListByEvent(ctx, eventID)`, `Delete(ctx, eventID, kind)`. No Update (snapshots are immutable; replace by Delete+Insert).

**Test cases:** insert both kinds for one event; ListByEvent returns both; Delete removes one without affecting the other.

Apply the DAO authoring template, commit `feat(store): add EventSnapshotsRepo`.

### Task 2.14: ClipsRepo

**Table:** `clips` (migration 0016).

```go
type Clip struct {
	ID               string
	EventID          string  // empty if ad-hoc
	CameraID         string
	StartTime        time.Time
	EndTime          time.Time
	PreRollSeconds   int
	PostRollSeconds  int
	State            string  // 'requested'|'preparing'|'ready'|'failed'
	Format           string  // 'fmp4'
	OutputPath       string  // populated when ready
	SizeBytes        int64
	Checksum         string
	CreatedBy        string
	CreatedAt        time.Time
	ReadyAt          time.Time
	ExpiresAt        time.Time
}
```

**Methods:**
- `Insert(ctx, *Clip) error` — seeds `state='requested'`, `created_at = Now()`
- `GetByID(ctx, id) (*Clip, error)`
- `List(ctx, ListClipsFilter) ([]*Clip, error)` — filter on camera_id, event_id, state, time-range
- `SetPreparing(ctx, id) error`
- `SetReady(ctx, id, outputPath, checksum string, sizeBytes int64) error`
- `SetFailed(ctx, id, lastErr string) error`
- `Delete(ctx, id) error`
- `DeleteExpiredReady(ctx, batchSize int) (paths []string, err error)` — used by retention sweeper; returns paths so the caller can unlink files before commit

**Sentinels:** `ErrClipNotFound`.

**Test cases:** state-machine transitions (Insert → SetPreparing → SetReady); SetFailed; DeleteExpiredReady returns expired paths and removes them.

Apply the DAO authoring template, commit `feat(store): add ClipsRepo`.

### Task 2.15: ClipSegmentsRepo

**Table:** `clip_segments` (migration 0017). Composite PK.

```go
type ClipSegment struct {
	ClipID        string
	SegmentPath   string
	StartOffsetMS int64
	DurationMS    int64
}
```

**Methods:** `InsertBatch(ctx, clipID, segments []*ClipSegment) error` (one tx, atomic), `ListByClip(ctx, clipID)`, `DeleteByClip(ctx, clipID)`.

**Test cases:** batch insert + list + delete.

Apply the DAO authoring template, commit `feat(store): add ClipSegmentsRepo`.

### Task 2.16: NotificationTargetsRepo

**Table:** `notification_targets` (migration 0018).

```go
type NotificationTarget struct {
	Kind                    string  // 'webhook' | 'email'
	ID                      string
	Name                    string
	WebhookURL              string  // empty for email targets
	WebhookSecretCiphertext []byte  // nil for email targets
	WebhookSecretNonce      []byte
	EmailAddress            string  // empty for webhook targets
	Enabled                 bool
	CreatedAt               time.Time
}
```

**Methods:** `Insert`, `GetByID`, `List`, `Update` (allows Name + URL/email + enabled), `SetEnabled`, `Delete`. `RotateWebhookSecret(ctx, id string, ct, nonce []byte) error` for credential rotation.

**Test cases:** insert webhook + insert email + list + update + rotate secret.

Apply the DAO authoring template, commit `feat(store): add NotificationTargetsRepo`.

### Task 2.17: NotificationSubscriptionsRepo

**Table:** `notification_subscriptions` (migration 0019).

```go
type NotificationSubscription struct {
	ID                    string
	TargetID              string
	EventTypeID           string  // empty if applies to all types
	CameraID              string  // empty if applies to all cameras
	MinSeverity           string  // empty if applies to all severities
	QuietHoursStartMinute int  // -1 if unset
	QuietHoursEndMinute   int  // -1 if unset
}
```

**Methods:** `Insert`, `GetByID`, `ListAllJoined(ctx) ([]*NotificationSubscriptionWithTarget, error)` (the cached view used by the dispatcher), `Delete`. No update — operators delete and re-create when filters change.

**Test cases:** insert with various filter combos; ListAllJoined returns the subscription + the parent target (denormalized) skipping disabled targets.

Apply the DAO authoring template, commit `feat(store): add NotificationSubscriptionsRepo`.

### Task 2.18: NotificationOutboxRepo

**Table:** `notification_outbox` (migration 0020). Hot path — workers poll constantly.

```go
type NotificationOutboxRow struct {
	ID            string
	TargetID      string
	EventID       string
	PayloadJSON   string
	State         string  // 'pending'|'in_flight'|'delivered'|'failed'|'dead'
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	DeliveredAt   time.Time
}
```

**Methods:**
- `Insert(ctx, *NotificationOutboxRow) error` — initial state always 'pending'
- `ClaimBatch(ctx, batchSize int) ([]*NotificationOutboxRow, error)` — single statement: `UPDATE notification_outbox SET state='in_flight' WHERE id IN (SELECT id FROM notification_outbox WHERE state='pending' AND next_attempt_at <= ? ORDER BY next_attempt_at LIMIT ?) RETURNING …`. Workers consume the returned rows.
- `MarkDelivered(ctx, id string) error`
- `MarkFailed(ctx, id, lastErr string, nextAttemptAt time.Time) error` — increments attempts, sets state back to 'pending' (or 'dead' if attempts >= max)
- `MarkDead(ctx, id, lastErr string) error`
- `Reset(ctx, id) error` — used by `POST /v1/notification-outbox/{id}/retry`; sets state='pending', next_attempt_at=Now(), attempts unchanged
- `ListRecent(ctx, limit int) ([]*NotificationOutboxRow, error)` — diagnostics endpoint

**Test cases:**
- Insert + ClaimBatch atomically transitions to in_flight
- ClaimBatch respects next_attempt_at (doesn't claim future-scheduled rows)
- MarkDelivered / MarkFailed (with retry) / MarkDead state transitions
- Reset moves a dead row back to pending

Apply the DAO authoring template, commit `feat(store): add NotificationOutboxRepo`.

### Task 2.19: AuditLogRepo

**Table:** `audit_log` (migration 0021). Append-only.

```go
type AuditEntry struct {
	ID            string
	OccurredAt    time.Time
	ActorUserID   string  // empty if system
	ActorUsername string  // denormalized snapshot
	ActorIP       string
	Action        string
	TargetKind    string
	TargetID      string
	BeforeJSON    string
	AfterJSON     string
	Details       string
}

type ListAuditFilter struct {
	ActorUserID string
	Action      string
	TargetKind  string
	TargetID    string
	From, To    time.Time
	Cursor      string
	Limit       int
}
```

**Methods:**
- `Insert(ctx, *AuditEntry) error` — never throws on duplicate (UUIDv7 collisions implausible); generates ID if zero
- `GetByID(ctx, id) (*AuditEntry, error)`
- `List(ctx, ListAuditFilter) ([]*AuditEntry, string, error)` — cursor-paginated, ORDER BY id DESC
- `StreamForExport(ctx, fromTime, toTime time.Time) (<-chan *AuditEntry, error)` — channel-based streaming for the export endpoint; the caller closes via context cancel
- `PurgeOlderThan(ctx, t time.Time) (int, error)` — manual admin purge; returns delete count

**Test cases:** insert + list with filters; cursor pagination; export streaming; purge respects cutoff.

Apply the DAO authoring template, commit `feat(store): add AuditLogRepo`.

### Task 2.20: CloudOutboxRepo

**Table:** `cloud_outbox` (migration 0022). Same shape as notification_outbox but kind-tagged.

```go
type CloudOutboxRow struct {
	ID            string
	Kind          string  // 'event'|'health'|'audit'
	PayloadJSON   string
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	DeliveredAt   time.Time
}
```

**Methods:** mirror `NotificationOutboxRepo` — `Insert`, `ClaimBatch`, `MarkDelivered`, `MarkFailed`, `MarkDead`, plus `DeleteOlderThan(ctx, t time.Time) (int, error)` for the unconfigured-install sweeper.

**Test cases:** mirror notification outbox tests; `DeleteOlderThan` removes rows older than horizon and returns count.

Apply the DAO authoring template, commit `feat(store): add CloudOutboxRepo`.

### Task 2.21: SystemSettingsRepo

**Table:** `system_settings` (migration 0023).

```go
type SystemSetting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string  // empty if system-set
}
```

**Methods:**
- `Get(ctx, key) (*SystemSetting, error)`
- `GetAll(ctx) (map[string]string, error)` — returns just key→value map for warm-cache builders
- `Upsert(ctx, key, value, updatedBy string) error`
- `UpsertMany(ctx, kv map[string]string, updatedBy string) error` — atomic batch in a tx; used by `PATCH /v1/system/settings`

**Convenience typed accessors** (added next to the repo, not on it):

```go
// In internal/store/system_settings.go after the Repo:

func (r *SystemSettingsRepo) GetInt(ctx context.Context, key string, defaultVal int) (int, error) {
	s, err := r.Get(ctx, key)
	if err == sql.ErrNoRows {
		return defaultVal, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(s.Value)
	if err != nil {
		return defaultVal, nil  // tolerate corrupt values; log via caller
	}
	return n, nil
}

func (r *SystemSettingsRepo) GetDuration(ctx context.Context, key string, defaultVal time.Duration) (time.Duration, error) { ... }
func (r *SystemSettingsRepo) GetBool(ctx context.Context, key string, defaultVal bool) (bool, error) { ... }
```

**Test cases:** Get/Upsert/UpsertMany; typed accessors with present/missing/malformed values.

Apply the DAO authoring template, commit `feat(store): add SystemSettingsRepo`.

### Task 2.22: Phase 2 verification + checkpoint

- [ ] **Step 1: Run the full store package test suite**

```bash
go test ./internal/store/... -v
```
Expected: every `Test*Repo*`, `TestMigrations*`, `TestLocalUsers*`, `TestOnvifSubscriptions*` test passes.

- [ ] **Step 2: Verify the binary still builds**

```bash
go build ./...
```
Expected: no output. Phase 3 onward will start using the new repos from outside `internal/store/`; for now everything compiles in isolation.

- [ ] **Step 3: Tag the phase-2 checkpoint locally**

```bash
git tag phase-2-dao-complete
```
This is a local-only tag for navigation; do not push.

---

## Phase 3 — Domain packages

Nine new packages, one task per package. Most depend on Phase 2 repos and on each other in a small dependency DAG:

```text
cameracred (no deps)
rbac (no deps)
schedule (depends on store.RecordingPolicies + store.RecordingSchedules)
events (depends on store.Events + store.EventRetention + store.CloudOutbox)
outbox (generic; consumed by notifications + cloudbridge later)
notifications (depends on store.NotificationTargets/Subscriptions/Outbox + outbox processor + cameracred for webhook secret decrypt + events bus)
cameras (depends on store.Cameras/Credentials/Capabilities/Health + cameracred + onvif manager + path manager bridge)
retention (depends on store.{Events,Clips,RecordingPolicies,RecordingSchedules} + recordstore)
cloudbridge (depends on store.CloudOutbox + outbox processor)
```

Build order: `cameracred`, `rbac` (in parallel) → `schedule`, `outbox` → `events` → `notifications`, `cameras` → `retention`, `cloudbridge`.

### Task 3.1: internal/cameracred — credential vault

**Files:**
- Create: `internal/cameracred/vault.go`
- Create: `internal/cameracred/url.go`
- Create: `internal/cameracred/vault_test.go`
- Create: `internal/cameracred/url_test.go`

- [ ] **Step 1: Write `vault.go`**

```go
// Package cameracred implements an AES-GCM envelope around per-camera
// secrets (RTSP password, ONVIF password, webhook signing secret, SMTP
// password). The envelope key lives at <identityDir>/cred.key (mode 0600)
// and is generated on first Open. The vault is the only place plaintext
// secrets are decrypted; consumers (path manager, webhook dispatcher)
// receive materialized URLs / strings without ever touching ciphertext.
package cameracred

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const keyFileName = "cred.key"
const keySize = 32 // AES-256

var ErrShortKey = errors.New("cameracred: key file shorter than 32 bytes")

// Vault holds the AES-GCM AEAD plus its key for nonce generation.
type Vault struct {
	aead cipher.AEAD
}

// Open loads or creates the vault key under identityDir and returns a
// ready Vault. Idempotent: subsequent Opens with the same dir return a
// vault using the same key.
func Open(identityDir string) (*Vault, error) {
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		return nil, fmt.Errorf("cameracred: identity dir: %w", err)
	}
	keyPath := filepath.Join(identityDir, keyFileName)
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cameracred: aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cameracred: gcm: %w", err)
	}
	return &Vault{aead: aead}, nil
}

// Encrypt produces (ciphertext, nonce). Each call generates a fresh
// random nonce.
func (v *Vault) Encrypt(plain []byte) (ct, nonce []byte, err error) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("cameracred: nonce: %w", err)
	}
	ct = v.aead.Seal(nil, nonce, plain, nil)
	return ct, nonce, nil
}

// Decrypt reverses Encrypt. Authentication failure produces an error.
func (v *Vault) Decrypt(ct, nonce []byte) ([]byte, error) {
	plain, err := v.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("cameracred: decrypt: %w", err)
	}
	return plain, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) < keySize {
			return nil, ErrShortKey
		}
		return data[:keySize], nil
	}
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("cameracred: gen key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("cameracred: persist key: %w", err)
	}
	return key, nil
}
```

- [ ] **Step 2: Write `url.go`**

```go
package cameracred

import (
	"net/url"
	"strings"
)

// MaterializeRTSPURL injects username + password into an RTSP URL
// template (no userinfo). The result is the only point where plaintext
// password meets a string. Callers must NEVER log this value.
//
// Example:
//   tmpl = "rtsp://192.168.1.42/cam/realmonitor?channel=1&subtype=0"
//   user = "admin", pass = "p@ss/word"
//   ->     "rtsp://admin:p%40ss%2Fword@192.168.1.42/cam/realmonitor?channel=1&subtype=0"
//
// If the template already contains userinfo, MaterializeRTSPURL replaces
// it. If parsing fails, the template is returned unchanged (so callers
// can fall back to the unmaterialized URL when they have no credentials
// configured); callers should validate beforehand for production use.
func MaterializeRTSPURL(tmpl, username, password string) string {
	if username == "" {
		return tmpl
	}
	u, err := url.Parse(tmpl)
	if err != nil {
		return tmpl
	}
	u.User = url.UserPassword(username, password)
	return u.String()
}

// RedactURLUserinfo returns a copy of an RTSP URL with userinfo removed,
// suitable for logging. Used by the auth/credentials redaction helper.
func RedactURLUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// Best-effort: blanket strip "user:pass@" from rtsp(s)://
		if i := strings.Index(raw, "://"); i >= 0 {
			rest := raw[i+3:]
			if at := strings.Index(rest, "@"); at >= 0 {
				return raw[:i+3] + rest[at+1:]
			}
		}
		return raw
	}
	if u.User != nil {
		u.User = nil
	}
	return u.String()
}
```

- [ ] **Step 3: Write `vault_test.go`**

```go
package cameracred

import (
	"path/filepath"
	"testing"
)

func TestVault_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	v, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	plain := []byte("hunter2")
	ct, nonce, err := v.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := v.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Errorf("round trip: got %q want %q", got, plain)
	}
}

func TestVault_OpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	v1, err := Open(dir)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	ct, nonce, _ := v1.Encrypt([]byte("secret"))

	// Re-open with the same dir; should use the same key file.
	v2, err := Open(dir)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	got, err := v2.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt with re-opened vault: %v", err)
	}
	if string(got) != "secret" {
		t.Errorf("got %q", got)
	}
}

func TestVault_DecryptFailsOnTamper(t *testing.T) {
	dir := t.TempDir()
	v, _ := Open(dir)
	ct, nonce, _ := v.Encrypt([]byte("secret"))
	ct[0] ^= 0xff // flip a bit
	if _, err := v.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected decrypt error on tampered ciphertext, got nil")
	}
}

func TestVault_KeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("open: %v", err)
	}
	info, err := osStat(filepath.Join(dir, "cred.key"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600, got %o", perm)
	}
}

// osStat is a thin wrapper to keep the import clean.
var osStat = func(p string) (interface {
	Mode() interface{ Perm() interface{ String() string; Perm() interface{ String() string } } }
}, error) {
	// Use os.Stat directly in real test; this stub just narrows the import.
	return nil, nil
}
```

(Note: replace the `osStat` indirection with `os.Stat` directly when implementing.)

- [ ] **Step 4: Write `url_test.go`**

```go
package cameracred

import "testing"

func TestMaterializeRTSPURL(t *testing.T) {
	cases := []struct {
		name     string
		tmpl     string
		user     string
		pass     string
		expected string
	}{
		{
			name:     "simple",
			tmpl:     "rtsp://192.168.1.42/cam",
			user:     "admin",
			pass:     "p4ss",
			expected: "rtsp://admin:p4ss@192.168.1.42/cam",
		},
		{
			name:     "password with reserved chars is escaped",
			tmpl:     "rtsp://host/path",
			user:     "u",
			pass:     "p@ss/word",
			expected: "rtsp://u:p%40ss%2Fword@host/path",
		},
		{
			name:     "no user returns template unchanged",
			tmpl:     "rtsp://host/path",
			user:     "",
			expected: "rtsp://host/path",
		},
		{
			name:     "preserves query string",
			tmpl:     "rtsp://host/cam?channel=1",
			user:     "admin",
			pass:     "p",
			expected: "rtsp://admin:p@host/cam?channel=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaterializeRTSPURL(tc.tmpl, tc.user, tc.pass)
			if got != tc.expected {
				t.Errorf("got %q want %q", got, tc.expected)
			}
		})
	}
}

func TestRedactURLUserinfo(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"rtsp://admin:p4ss@host/path", "rtsp://host/path"},
		{"rtsps://u:p%40ss@host:554/cam?x=1", "rtsps://host:554/cam?x=1"},
		{"http://host/path", "http://host/path"},
		{"not a url", "not a url"},
	}
	for _, tc := range cases {
		got := RedactURLUserinfo(tc.raw)
		if got != tc.want {
			t.Errorf("redact(%q): got %q want %q", tc.raw, got, tc.want)
		}
	}
}
```

- [ ] **Step 5: Run**

```bash
go test ./internal/cameracred/ -v
```
Expected: 6 tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/cameracred/
git commit -m "feat(cameracred): AES-GCM credential vault + RTSP URL materializer"
```

### Task 3.2: internal/rbac — roles, permissions, middleware

**Files:**
- Create: `internal/rbac/perms.go`
- Create: `internal/rbac/middleware.go`
- Create: `internal/rbac/perms_test.go`
- Create: `internal/rbac/middleware_test.go`

- [ ] **Step 1: Write `perms.go`**

```go
// Package rbac defines the consumer NVR's role and permission enums and
// the role->permission map. The map is in code (not in DB) per the
// foundation spec; promoting to DB-defined custom roles is a future
// migration. The package also exposes a Gin middleware factory that
// reads role from JWT claims and returns 403 on missing permission.
package rbac

// Role is a string enum matching the seeded roles.id values in DB.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// Permission is a string enum used by the API middleware to gate
// handlers. Permissions are dot-namespaced for grouping (camera.*,
// policy.*, event.*, ...).
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

// roleGrants is the in-code role->permission map. Admin has every
// permission; viewer has the read-only subset.
var roleGrants = map[Role]map[Permission]struct{}{
	RoleAdmin: {
		PermCameraRead:   {}, PermCameraWrite:  {},
		PermPolicyRead:   {}, PermPolicyWrite:  {},
		PermEventRead:    {}, PermEventAck:     {},
		PermClipRead:     {}, PermClipExport:   {},
		PermNotifManage:  {},
		PermUserManage:   {},
		PermSystemConfig: {},
		PermAuditRead:    {},
	},
	RoleViewer: {
		PermCameraRead: {}, PermPolicyRead: {},
		PermEventRead:  {}, PermClipRead:   {},
	},
}

// HasPermission reports whether the given role grants the permission.
// Unknown roles never grant anything (fail closed).
func HasPermission(role Role, p Permission) bool {
	grants, ok := roleGrants[role]
	if !ok {
		return false
	}
	_, ok = grants[p]
	return ok
}

// AllPermissionsFor returns the permission set for the given role.
// Used by the API to ship the user's permissions to the SPA so the SPA
// can hide/show controls without a network round-trip.
func AllPermissionsFor(role Role) []Permission {
	grants, ok := roleGrants[role]
	if !ok {
		return nil
	}
	out := make([]Permission, 0, len(grants))
	for p := range grants {
		out = append(out, p)
	}
	return out
}
```

- [ ] **Step 2: Write `middleware.go`**

```go
package rbac

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// claimsKey is the gin context key the auth middleware uses to stash the
// authenticated user's claims. Defined here so the rbac middleware does
// not pull in internal/auth (and vice versa).
const ClaimsContextKey = "raikada.claims"

// Claims is the minimal shape the rbac middleware reads. The auth layer
// constructs it from the validated JWT.
type Claims struct {
	UserID   string
	Username string
	Role     Role
}

// AuditEmitter is the per-request audit hook. Implemented by the audit
// emitter wired in Phase 6; passed in by reference so this package does
// not pull in internal/store. nil means no audit emission (e.g., tests).
type AuditEmitter interface {
	PermissionDenied(ctx context.Context, claims Claims, action, ip string)
}

// RequirePerm returns a Gin middleware that aborts with 403 if the
// authenticated user does not have the named permission. Anonymous
// requests are aborted with 401.
func RequirePerm(perm Permission, audit AuditEmitter) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(ClaimsContextKey)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		claims, ok := v.(Claims)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !HasPermission(claims.Role, perm) {
			if audit != nil {
				audit.PermissionDenied(c.Request.Context(), claims, c.Request.URL.Path, c.ClientIP())
			}
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 3: Write `perms_test.go`**

```go
package rbac

import "testing"

func TestHasPermission(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleAdmin, PermCameraWrite, true},
		{RoleAdmin, PermAuditRead, true},
		{RoleViewer, PermCameraRead, true},
		{RoleViewer, PermCameraWrite, false},
		{RoleViewer, PermUserManage, false},
		{Role("nonexistent"), PermCameraRead, false}, // fail-closed
	}
	for _, tc := range cases {
		got := HasPermission(tc.role, tc.perm)
		if got != tc.want {
			t.Errorf("HasPermission(%q, %q) = %v, want %v", tc.role, tc.perm, got, tc.want)
		}
	}
}

func TestAllPermissionsFor(t *testing.T) {
	if got := AllPermissionsFor(RoleAdmin); len(got) != 12 {
		t.Errorf("admin should have 12 perms, got %d", len(got))
	}
	if got := AllPermissionsFor(RoleViewer); len(got) != 4 {
		t.Errorf("viewer should have 4 perms, got %d", len(got))
	}
	if got := AllPermissionsFor(Role("nope")); got != nil {
		t.Errorf("unknown role should yield nil, got %v", got)
	}
}
```

- [ ] **Step 4: Write `middleware_test.go`**

```go
package rbac

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeAudit struct {
	calls []string
}

func (f *fakeAudit) PermissionDenied(_ context.Context, c Claims, action, ip string) {
	f.calls = append(f.calls, c.Username+":"+action)
}

func TestRequirePerm_Allows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(ClaimsContextKey, Claims{UserID: "u1", Username: "alice", Role: RoleAdmin})
	}, RequirePerm(PermCameraWrite, nil), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestRequirePerm_Denies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &fakeAudit{}
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(ClaimsContextKey, Claims{UserID: "u1", Username: "viewer", Role: RoleViewer})
	}, RequirePerm(PermCameraWrite, a), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
	if len(a.calls) != 1 {
		t.Errorf("expected 1 audit call, got %d", len(a.calls))
	}
}

func TestRequirePerm_Anonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", RequirePerm(PermCameraWrite, nil), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
```

- [ ] **Step 5: Run + commit**

```bash
go test ./internal/rbac/ -v
git add internal/rbac/
git commit -m "feat(rbac): role/permission enums + RequirePerm Gin middleware"
```

### Task 3.3: internal/schedule — weekly time-window resolver

**Files:**
- Create: `internal/schedule/resolver.go`
- Create: `internal/schedule/resolver_test.go`

- [ ] **Step 1: Write `resolver.go`**

```go
// Package schedule evaluates "is camera X recording at time t" given
// a recording policy (mode + schedules) plus the motion controller's
// current per-camera state. Site timezone is global, sourced from
// system_settings['timezone']. Wrap-midnight windows and union of
// overlapping windows are handled here.
package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

// MotionState reports whether a given camera is currently in a
// motion-triggered window (start..end+postroll). Implemented by the
// motion controller in Phase 6.
type MotionState interface {
	IsActive(cameraID string, t time.Time) (active bool, until time.Time)
}

// Resolver answers "is recording active for this camera at this instant?"
type Resolver struct {
	cameras   *store.CamerasRepo
	policies  *store.RecordingPoliciesRepo
	schedules *store.RecordingSchedulesRepo
	settings  *store.SystemSettingsRepo
	motion    MotionState
}

func New(
	cameras *store.CamerasRepo,
	policies *store.RecordingPoliciesRepo,
	schedules *store.RecordingSchedulesRepo,
	settings *store.SystemSettingsRepo,
	motion MotionState,
) *Resolver {
	return &Resolver{cameras, policies, schedules, settings, motion}
}

// Active is the per-decision result.
type Active struct {
	On     bool
	Mode   string  // 'continuous'|'motion'|'scheduled'|'off'
	Reason string
	Until  time.Time  // optional: when the current "on" window ends
}

const (
	defaultPolicyID = "policy_default"
)

// IsActive evaluates the policy for cameraID at time t. Errors only
// surface for genuine DB failures; missing rows produce sensible defaults
// (camera missing → false; policy missing → fall back to default).
func (r *Resolver) IsActive(ctx context.Context, cameraID string, t time.Time) (Active, error) {
	cam, err := r.cameras.GetByID(ctx, cameraID)
	if err != nil {
		return Active{}, err
	}
	policyID := cam.RecordingPolicyID
	if policyID == "" {
		policyID = defaultPolicyID
	}
	pol, err := r.policies.GetByID(ctx, policyID)
	if err != nil {
		return Active{}, err
	}
	if !pol.Enabled || pol.Mode == "off" {
		return Active{On: false, Mode: pol.Mode, Reason: "policy disabled"}, nil
	}
	switch pol.Mode {
	case "continuous":
		return Active{On: true, Mode: "continuous", Reason: "continuous"}, nil
	case "motion":
		if r.motion == nil {
			return Active{On: false, Mode: "motion", Reason: "no motion controller"}, nil
		}
		on, until := r.motion.IsActive(cameraID, t)
		if on {
			return Active{On: true, Mode: "motion", Reason: "motion active", Until: until}, nil
		}
		return Active{On: false, Mode: "motion", Reason: "motion idle"}, nil
	case "scheduled":
		tz, _ := r.siteTimezone(ctx)
		schedules, err := r.schedules.ListByPolicy(ctx, pol.ID)
		if err != nil {
			return Active{}, err
		}
		on, until := evaluateWindows(schedules, t.In(tz))
		if on {
			return Active{On: true, Mode: "scheduled", Reason: "scheduled window", Until: until}, nil
		}
		return Active{On: false, Mode: "scheduled", Reason: "outside window"}, nil
	}
	return Active{}, errors.New("unknown policy mode: " + pol.Mode)
}

func (r *Resolver) siteTimezone(ctx context.Context) (*time.Location, error) {
	s, err := r.settings.Get(ctx, "timezone")
	if err != nil || s == nil || s.Value == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(s.Value)
	if err != nil {
		return time.Local, nil
	}
	return loc, nil
}

// evaluateWindows checks every schedule row for the given local time and
// returns (on, until). Windows that wrap midnight are split internally.
// `until` is the next minute boundary at which the current decision
// changes (best-effort; the caller should re-evaluate around `until`).
func evaluateWindows(schedules []*store.RecordingSchedule, t time.Time) (bool, time.Time) {
	if len(schedules) == 0 {
		return false, time.Time{}
	}
	day := int(t.Weekday()) // 0=Sun..6=Sat
	minuteOfDay := t.Hour()*60 + t.Minute()
	yesterday := (day + 6) % 7

	for _, s := range schedules {
		if s.DayOfWeek == day {
			start, end := s.StartMinute, s.EndMinute
			if end > start && minuteOfDay >= start && minuteOfDay < end {
				until := startOfDay(t).Add(time.Duration(end) * time.Minute)
				return true, until
			}
			if end < start && minuteOfDay >= start {
				// wraps into tomorrow
				until := startOfDay(t).Add(24*time.Hour + time.Duration(end)*time.Minute)
				return true, until
			}
		}
		if s.DayOfWeek == yesterday && s.EndMinute < s.StartMinute && minuteOfDay < s.EndMinute {
			// midnight-crossed window from yesterday
			until := startOfDay(t).Add(time.Duration(s.EndMinute) * time.Minute)
			return true, until
		}
	}
	return false, time.Time{}
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
```

- [ ] **Step 2: Write `resolver_test.go`**

```go
package schedule

import (
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

func TestEvaluateWindows_SimpleWeekday(t *testing.T) {
	schedules := []*store.RecordingSchedule{
		{DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 17 * 60}, // Mon 09:00-17:00
	}
	loc := time.UTC
	cases := []struct {
		when string
		want bool
	}{
		{"2026-05-04T09:00:00Z", true},  // Mon 09:00 → true
		{"2026-05-04T08:59:00Z", false}, // Mon 08:59 → false
		{"2026-05-04T17:00:00Z", false}, // Mon 17:00 → false (exclusive)
		{"2026-05-05T10:00:00Z", false}, // Tue 10:00 → false (no schedule)
	}
	for _, tc := range cases {
		ts, _ := time.ParseInLocation(time.RFC3339, tc.when, loc)
		on, _ := evaluateWindows(schedules, ts)
		if on != tc.want {
			t.Errorf("%s: got %v want %v", tc.when, on, tc.want)
		}
	}
}

func TestEvaluateWindows_WrapMidnight(t *testing.T) {
	schedules := []*store.RecordingSchedule{
		{DayOfWeek: 5, StartMinute: 22 * 60, EndMinute: 2 * 60}, // Fri 22:00 → Sat 02:00
	}
	loc := time.UTC
	cases := []struct {
		when string
		want bool
	}{
		{"2026-05-08T23:00:00Z", true},  // Fri 23:00 → true (start side)
		{"2026-05-09T01:30:00Z", true},  // Sat 01:30 → true (wrap)
		{"2026-05-09T02:00:00Z", false}, // Sat 02:00 → false (exclusive)
		{"2026-05-09T03:00:00Z", false},
	}
	for _, tc := range cases {
		ts, _ := time.ParseInLocation(time.RFC3339, tc.when, loc)
		on, _ := evaluateWindows(schedules, ts)
		if on != tc.want {
			t.Errorf("%s: got %v want %v", tc.when, on, tc.want)
		}
	}
}

func TestEvaluateWindows_UnionOverlapping(t *testing.T) {
	schedules := []*store.RecordingSchedule{
		{DayOfWeek: 3, StartMinute: 9 * 60, EndMinute: 12 * 60},
		{DayOfWeek: 3, StartMinute: 14 * 60, EndMinute: 16 * 60},
	}
	loc := time.UTC
	for _, when := range []string{"2026-05-06T10:00:00Z", "2026-05-06T15:00:00Z"} {
		ts, _ := time.ParseInLocation(time.RFC3339, when, loc)
		if on, _ := evaluateWindows(schedules, ts); !on {
			t.Errorf("%s: expected true", when)
		}
	}
	ts, _ := time.ParseInLocation(time.RFC3339, "2026-05-06T13:00:00Z", loc)
	if on, _ := evaluateWindows(schedules, ts); on {
		t.Errorf("13:00 (gap): expected false")
	}
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/schedule/ -v
git add internal/schedule/
git commit -m "feat(schedule): weekly time-window resolver with wrap-midnight + union"
```

### Task 3.4: internal/outbox — generic outbox processor

**Files:**
- Create: `internal/outbox/processor.go`
- Create: `internal/outbox/backoff.go`
- Create: `internal/outbox/processor_test.go`
- Create: `internal/outbox/backoff_test.go`

- [ ] **Step 1: Write `backoff.go`**

```go
// Package outbox is a generic "claim → dispatch → mark" processor used
// by both internal/notifications (webhook + SMTP delivery) and
// internal/cloudbridge (events/health/audit pushed to a future cloud
// product). The package is intentionally agnostic to row shape; callers
// supply Claim, Dispatch, MarkDelivered, MarkFailed, MarkDead funcs.
package outbox

import (
	"math/rand"
	"time"
)

// DefaultBackoffSteps is the canonical exponential backoff schedule
// (jittered ±25% per attempt). Index 0 is used after attempt 1, index 1
// after attempt 2, etc. Total cap ≈ 4h22m across 8 attempts.
var DefaultBackoffSteps = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	1 * time.Hour,
	2 * time.Hour,
}

// NextAttemptDelay returns the duration to wait before retry given the
// number of attempts already made. Adds ±25% jitter. attempts is 1-based
// (1 == first delay).
func NextAttemptDelay(attempts int, steps []time.Duration, rng *rand.Rand) time.Duration {
	if len(steps) == 0 {
		steps = DefaultBackoffSteps
	}
	idx := attempts - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(steps) {
		idx = len(steps) - 1
	}
	base := steps[idx]
	jitter := time.Duration(float64(base) * 0.25 * (rng.Float64()*2 - 1))
	return base + jitter
}
```

- [ ] **Step 2: Write `processor.go`**

```go
package outbox

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// Job is one claimed row. Opaque to the processor; fields are interpreted
// by the caller's Dispatch func.
type Job struct {
	ID          string
	Attempts    int
	PayloadJSON string
	Extra       any  // caller-defined: target row, kind, etc.
}

// Result is what Dispatch returns.
type Result int

const (
	ResultDelivered Result = iota
	ResultRetry             // 5xx, timeout, transient
	ResultDead              // 4xx, permanent failure
)

// Funcs is the per-processor wiring.
type Funcs struct {
	Claim         func(ctx context.Context, batchSize int) ([]*Job, error)
	Dispatch      func(ctx context.Context, j *Job) (Result, string, error)  // returns (result, errMsg, sentinel-err)
	MarkDelivered func(ctx context.Context, id string) error
	MarkFailed    func(ctx context.Context, id, lastErr string, nextAttemptAt time.Time) error
	MarkDead      func(ctx context.Context, id, lastErr string) error
}

// Config tunes the processor. Zero values use sensible defaults.
type Config struct {
	Workers       int  // default 4
	BatchSize     int  // default 16
	PollInterval  time.Duration  // default 1s
	MaxAttempts   int  // default 8
	BackoffSteps  []time.Duration
}

// Processor runs a worker pool that polls Claim, dispatches each Job,
// and updates state via MarkDelivered/MarkFailed/MarkDead.
type Processor struct {
	cfg    Config
	funcs  Funcs
	logger logger.Writer
	rng    *rand.Rand
	rngMu  sync.Mutex
}

func NewProcessor(funcs Funcs, cfg Config, log logger.Writer) *Processor {
	if cfg.Workers == 0 {
		cfg.Workers = 4
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 16
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.BackoffSteps == nil {
		cfg.BackoffSteps = DefaultBackoffSteps
	}
	return &Processor{
		cfg:    cfg,
		funcs:  funcs,
		logger: log,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run blocks until ctx is cancelled. Spins up Workers goroutines, each
// long-polling Claim and dispatching results through the Funcs hooks.
func (p *Processor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.worker(ctx)
		}()
	}
	wg.Wait()
}

func (p *Processor) worker(ctx context.Context) {
	tick := time.NewTicker(p.cfg.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			jobs, err := p.funcs.Claim(ctx, p.cfg.BatchSize)
			if err != nil {
				if p.logger != nil {
					p.logger.Log(logger.Warn, "[outbox] claim: %v", err)
				}
				continue
			}
			for _, j := range jobs {
				p.dispatchOne(ctx, j)
			}
		}
	}
}

func (p *Processor) dispatchOne(ctx context.Context, j *Job) {
	res, errMsg, err := p.funcs.Dispatch(ctx, j)
	if err != nil && p.logger != nil {
		p.logger.Log(logger.Debug, "[outbox] dispatch error %s: %v", j.ID, err)
	}
	switch res {
	case ResultDelivered:
		if err := p.funcs.MarkDelivered(ctx, j.ID); err != nil && p.logger != nil {
			p.logger.Log(logger.Warn, "[outbox] mark delivered %s: %v", j.ID, err)
		}
	case ResultDead:
		if err := p.funcs.MarkDead(ctx, j.ID, errMsg); err != nil && p.logger != nil {
			p.logger.Log(logger.Warn, "[outbox] mark dead %s: %v", j.ID, err)
		}
	case ResultRetry:
		if j.Attempts+1 >= p.cfg.MaxAttempts {
			if err := p.funcs.MarkDead(ctx, j.ID, "max attempts: "+errMsg); err != nil && p.logger != nil {
				p.logger.Log(logger.Warn, "[outbox] mark dead (max attempts) %s: %v", j.ID, err)
			}
			return
		}
		p.rngMu.Lock()
		delay := NextAttemptDelay(j.Attempts+1, p.cfg.BackoffSteps, p.rng)
		p.rngMu.Unlock()
		next := time.Now().Add(delay)
		if err := p.funcs.MarkFailed(ctx, j.ID, errMsg, next); err != nil && p.logger != nil {
			p.logger.Log(logger.Warn, "[outbox] mark failed %s: %v", j.ID, err)
		}
	}
}
```

- [ ] **Step 3: Write tests**

```go
// internal/outbox/backoff_test.go
package outbox

import (
	"math/rand"
	"testing"
	"time"
)

func TestNextAttemptDelay_WithinBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for attempts := 1; attempts <= 8; attempts++ {
		base := DefaultBackoffSteps[attempts-1]
		got := NextAttemptDelay(attempts, nil, rng)
		min, max := time.Duration(float64(base)*0.75), time.Duration(float64(base)*1.25)
		if got < min || got > max {
			t.Errorf("attempt %d: %v not in [%v,%v]", attempts, got, min, max)
		}
	}
}

func TestNextAttemptDelay_CapsAtLastStep(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	got := NextAttemptDelay(20, nil, rng)
	last := DefaultBackoffSteps[len(DefaultBackoffSteps)-1]
	if got > time.Duration(float64(last)*1.25) {
		t.Errorf("expected cap, got %v", got)
	}
}
```

```go
// internal/outbox/processor_test.go
package outbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProcessor_DispatchesAndMarks(t *testing.T) {
	var (
		mu        sync.Mutex
		dispatched []string
		delivered []string
		failed    []string
		dead      []string
	)
	queued := []*Job{
		{ID: "a", PayloadJSON: "{}", Attempts: 0, Extra: ResultDelivered},
		{ID: "b", PayloadJSON: "{}", Attempts: 0, Extra: ResultRetry},
		{ID: "c", PayloadJSON: "{}", Attempts: 7, Extra: ResultRetry},  // hits MaxAttempts
		{ID: "d", PayloadJSON: "{}", Attempts: 0, Extra: ResultDead},
	}
	served := false
	funcs := Funcs{
		Claim: func(_ context.Context, _ int) ([]*Job, error) {
			if served {
				return nil, nil
			}
			served = true
			return queued, nil
		},
		Dispatch: func(_ context.Context, j *Job) (Result, string, error) {
			mu.Lock()
			dispatched = append(dispatched, j.ID)
			mu.Unlock()
			return j.Extra.(Result), "synthetic", nil
		},
		MarkDelivered: func(_ context.Context, id string) error {
			mu.Lock(); delivered = append(delivered, id); mu.Unlock(); return nil
		},
		MarkFailed: func(_ context.Context, id, _ string, _ time.Time) error {
			mu.Lock(); failed = append(failed, id); mu.Unlock(); return nil
		},
		MarkDead: func(_ context.Context, id, _ string) error {
			mu.Lock(); dead = append(dead, id); mu.Unlock(); return nil
		},
	}
	cfg := Config{Workers: 1, PollInterval: 5 * time.Millisecond, MaxAttempts: 8}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	NewProcessor(funcs, cfg, nil).Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 1 || delivered[0] != "a" {
		t.Errorf("delivered: got %v", delivered)
	}
	if len(failed) != 1 || failed[0] != "b" {
		t.Errorf("failed: got %v", failed)
	}
	if len(dead) != 2 || !contains(dead, "c") || !contains(dead, "d") {
		t.Errorf("dead: got %v", dead)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/outbox/ -v
git add internal/outbox/
git commit -m "feat(outbox): generic claim/dispatch/mark processor with exponential backoff"
```

### Task 3.5: internal/events — canonical event service + bus

**Files:**
- Create: `internal/events/types.go`
- Create: `internal/events/service.go`
- Create: `internal/events/bus.go`
- Create: `internal/events/service_test.go`
- Create: `internal/events/bus_test.go`

- [ ] **Step 1: Write `types.go`**

```go
// Package events owns the canonical CameraEvent shape, the in-process
// bus that fans new events out to subscribers (notification dispatcher,
// snapshot fetcher, clip linker, SSE stream, cloud_outbox enqueuer), and
// the service that persists, materializes expires_at, and exposes
// list/ack/delete-expired operations to the API and retention sweepers.
package events

import (
	"encoding/json"
	"time"
)

type Event struct {
	ID             string
	CameraID       string
	TypeID         string
	Source         string
	OccurredAt     time.Time
	ReceivedAt     time.Time
	Severity       string
	PayloadJSON    json.RawMessage
	RegionJSON     json.RawMessage
	AcknowledgedAt time.Time
	AcknowledgedBy string
	ExpiresAt      time.Time
}

type ListFilter struct {
	CameraIDs []string
	TypeIDs   []string
	Sources   []string
	From, To  time.Time
	MinSev    string
	OnlyUnack bool
	Cursor    string
	Limit     int
}
```

- [ ] **Step 2: Write `bus.go`**

```go
package events

import "sync"

// Bus is an in-process fan-out for newly-inserted events. Subscribers
// receive every event via their channel; a slow subscriber backpressures
// only itself (channel buffer = 256, beyond which events are dropped for
// that subscriber and a counter is incremented).
type Bus struct {
	mu     sync.Mutex
	subs   []*subscriber
	nextID int
}

type subscriber struct {
	id    int
	ch    chan Event
	drops int64
}

func NewBus() *Bus { return &Bus{} }

// Subscribe returns a receive channel and an unsubscribe func. Buffer
// is 256; lossless for any sane subscriber, lossy for stuck ones.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscriber{id: b.nextID, ch: make(chan Event, 256)}
	b.nextID++
	b.subs = append(b.subs, s)
	id := s.id
	return s.ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, x := range b.subs {
			if x.id == id {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(x.ch)
				return
			}
		}
	}
}

// Publish fans ev to every subscriber. Non-blocking: a full subscriber
// channel drops the event for that subscriber and increments drops.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			s.drops++
		}
	}
}
```

- [ ] **Step 3: Write `service.go`**

```go
package events

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/store"
)

// CloudOutboxEnqueuer mirrors a single method from the cloud_outbox
// repo so this package does not need to import all of internal/store
// just for one call.
type CloudOutboxEnqueuer interface {
	Insert(ctx context.Context, row *store.CloudOutboxRow) error
}

type Service struct {
	events    *store.EventsRepo
	retention *store.EventRetentionRepo
	cloud     CloudOutboxEnqueuer
	bus       *Bus
}

func NewService(
	events *store.EventsRepo,
	retention *store.EventRetentionRepo,
	cloud CloudOutboxEnqueuer,
	bus *Bus,
) *Service {
	return &Service{events, retention, cloud, bus}
}

// Insert validates, materializes ExpiresAt, persists, enqueues the cloud
// outbox row, and publishes to the bus.
func (s *Service) Insert(ctx context.Context, ev *Event) error {
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	if ev.ReceivedAt.IsZero() {
		ev.ReceivedAt = time.Now().UTC()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = ev.ReceivedAt
	}
	keep, err := s.retention.GetEffective(ctx, ev.TypeID)
	if err != nil {
		return err
	}
	ev.ExpiresAt = ev.ReceivedAt.Add(keep)

	row := &store.Event{
		ID: ev.ID, CameraID: ev.CameraID, TypeID: ev.TypeID,
		Source: ev.Source, OccurredAt: ev.OccurredAt, ReceivedAt: ev.ReceivedAt,
		Severity: ev.Severity,
		PayloadJSON: string(ev.PayloadJSON), RegionJSON: string(ev.RegionJSON),
		ExpiresAt: ev.ExpiresAt,
	}
	if err := s.events.Insert(ctx, row); err != nil {
		return err
	}
	if s.cloud != nil {
		payload, _ := json.Marshal(ev)
		_ = s.cloud.Insert(ctx, &store.CloudOutboxRow{
			ID: uuid.New().String(), Kind: "event",
			PayloadJSON: string(payload), State: "pending",
			NextAttemptAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
		})
		// Cloud-outbox failure is logged but non-fatal; events table is
		// authoritative. Caller's logger is on Service if needed.
	}
	s.bus.Publish(*ev)
	return nil
}

func (s *Service) Get(ctx context.Context, id string) (*Event, error) {
	row, err := s.events.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return rowToEvent(row), nil
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]*Event, string, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	storeFilter := store.ListEventsFilter{
		CameraIDs: f.CameraIDs, TypeIDs: f.TypeIDs, Sources: f.Sources,
		From: f.From, To: f.To, MinSev: f.MinSev, OnlyUnack: f.OnlyUnack,
		Cursor: f.Cursor, Limit: f.Limit,
	}
	rows, next, err := s.events.List(ctx, storeFilter)
	if err != nil {
		return nil, "", err
	}
	out := make([]*Event, len(rows))
	for i, r := range rows {
		out[i] = rowToEvent(r)
	}
	return out, next, nil
}

func (s *Service) Acknowledge(ctx context.Context, id, userID string) error {
	if id == "" || userID == "" {
		return errors.New("acknowledge: id and userID required")
	}
	return s.events.Acknowledge(ctx, id, userID)
}

func (s *Service) Subscribe() (<-chan Event, func()) {
	return s.bus.Subscribe()
}

func rowToEvent(r *store.Event) *Event {
	ev := &Event{
		ID: r.ID, CameraID: r.CameraID, TypeID: r.TypeID, Source: r.Source,
		OccurredAt: r.OccurredAt, ReceivedAt: r.ReceivedAt, Severity: r.Severity,
		PayloadJSON: json.RawMessage(r.PayloadJSON),
		RegionJSON:  json.RawMessage(r.RegionJSON),
		AcknowledgedAt: r.AcknowledgedAt, AcknowledgedBy: r.AcknowledgedBy,
		ExpiresAt: r.ExpiresAt,
	}
	return ev
}
```

- [ ] **Step 4: Write tests** (use the in-memory store helpers from Phase 1; add `mustOpenStore` equivalent or call the existing one via test build tag)

```go
// internal/events/service_test.go
package events

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Seed a camera so events.camera_id FK is satisfiable.
	if err := s.Cameras.Insert(context.Background(), &store.Camera{
		ID: "cam-1", Name: "cam-1", SourceType: "rtsp",
		SourceURL: "rtsp://x/y", Enabled: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	return s
}

func TestService_InsertMaterializesExpiresAt(t *testing.T) {
	s := openStore(t)
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)

	now := time.Now().UTC()
	ev := &Event{
		CameraID: "cam-1", TypeID: "motion", Source: "onvif_pullpoint",
		OccurredAt: now, ReceivedAt: now,
	}
	if err := svc.Insert(context.Background(), ev); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ev.ExpiresAt.IsZero() {
		t.Fatal("expected ExpiresAt to be materialized")
	}
	// motion has 90d retention per the seeded event_retention.
	want := now.Add(90 * 24 * time.Hour)
	if got, max := ev.ExpiresAt, want.Add(time.Second); got.Before(want.Add(-time.Second)) || got.After(max) {
		t.Errorf("ExpiresAt %v not within 1s of expected %v", got, want)
	}
}

func TestService_BusReceivesPublished(t *testing.T) {
	s := openStore(t)
	bus := NewBus()
	svc := NewService(s.Events, s.EventRetention, nil, bus)

	ch, unsub := svc.Subscribe()
	defer unsub()

	go func() {
		_ = svc.Insert(context.Background(), &Event{
			CameraID: "cam-1", TypeID: "motion", Source: "onvif_pullpoint",
		})
	}()
	select {
	case ev := <-ch:
		if ev.CameraID != "cam-1" {
			t.Errorf("got camera %q", ev.CameraID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for bus publish")
	}
}
```

```go
// internal/events/bus_test.go
package events

import (
	"testing"
	"time"
)

func TestBus_FanOut(t *testing.T) {
	b := NewBus()
	c1, u1 := b.Subscribe()
	defer u1()
	c2, u2 := b.Subscribe()
	defer u2()

	b.Publish(Event{ID: "x"})

	for _, c := range []<-chan Event{c1, c2} {
		select {
		case ev := <-c:
			if ev.ID != "x" {
				t.Errorf("got %q", ev.ID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timeout")
		}
	}
}

func TestBus_UnsubscribeDoesNotReceive(t *testing.T) {
	b := NewBus()
	c, u := b.Subscribe()
	u()
	select {
	case _, ok := <-c:
		if ok {
			t.Fatal("expected closed channel after unsub")
		}
	case <-time.After(50 * time.Millisecond):
	}
}
```

- [ ] **Step 5: Run + commit**

```bash
go test ./internal/events/ -v
git add internal/events/
git commit -m "feat(events): canonical Service + in-process Bus + retention materialization"
```

### Task 3.6: internal/notifications — webhook + SMTP delivery

**Files:**
- Create: `internal/notifications/payload.go`
- Create: `internal/notifications/webhook.go`
- Create: `internal/notifications/smtp.go`
- Create: `internal/notifications/dispatcher.go`
- Create: `internal/notifications/payload_test.go`
- Create: `internal/notifications/webhook_test.go`
- Create: `internal/notifications/dispatcher_test.go`

- [ ] **Step 1: Write `payload.go`**

```go
// Package notifications builds canonical webhook/email payloads from
// canonical events, signs webhooks with HMAC-SHA-256, and renders the
// HTML email template. The dispatcher runs an outbox worker pool over
// notification_outbox rows.
package notifications

import (
	"encoding/json"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

const PayloadSchema = "raikada.event.v1"

type Payload struct {
	Schema       string                 `json:"schema"`
	DeliveryID   string                 `json:"delivery_id"`
	Site         SitePayload            `json:"site"`
	Event        EventPayload           `json:"event"`
	SnapshotURL  string                 `json:"snapshot_url"`
	ThumbnailURL string                 `json:"thumbnail_url"`
	ClipURL      string                 `json:"clip_url,omitempty"`
}

type SitePayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type EventPayload struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`
	TypeDisplayName string                 `json:"type_display_name"`
	Camera          CameraPayload          `json:"camera"`
	Source          string                 `json:"source"`
	Severity        string                 `json:"severity,omitempty"`
	OccurredAt      time.Time              `json:"occurred_at"`
	ReceivedAt      time.Time              `json:"received_at"`
	Region          json.RawMessage        `json:"region,omitempty"`
	Payload         json.RawMessage        `json:"payload,omitempty"`
}

type CameraPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

// Build renders a Payload from an event + the site/camera/type lookups
// the caller supplies. URLs are filled in by the dispatcher (it knows
// the recorder's external URL); this function leaves them empty.
func Build(deliveryID string, site SitePayload, ev *events.Event, cam *store.Camera, evType *store.EventType) *Payload {
	return &Payload{
		Schema:     PayloadSchema,
		DeliveryID: deliveryID,
		Site:       site,
		Event: EventPayload{
			ID:              ev.ID,
			Type:            ev.TypeID,
			TypeDisplayName: evType.DisplayName,
			Camera: CameraPayload{
				ID: cam.ID, Name: cam.Name, DisplayName: cam.DisplayName,
			},
			Source: ev.Source, Severity: ev.Severity,
			OccurredAt: ev.OccurredAt, ReceivedAt: ev.ReceivedAt,
			Region: ev.RegionJSON, Payload: ev.PayloadJSON,
		},
	}
}
```

- [ ] **Step 2: Write `webhook.go`**

```go
package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookSender posts a Payload to a URL with X-Raikada-Signature.
type WebhookSender struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// Send returns (http_status, body_snippet, error). status==0 means a
// transport error happened (timeout, dns, etc.); the dispatcher treats
// status<200||status>=300 as failure.
func (w *WebhookSender) Send(ctx context.Context, url string, secret []byte, p *Payload) (status int, body string, err error) {
	body256 := func() string { b := []byte(body); if len(b) > 256 { return string(b[:256]) }; return body }
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, "", fmt.Errorf("marshal: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))

	reqCtx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Raikada-Signature", sig)
	req.Header.Set("X-Raikada-Delivery", p.DeliveryID)
	req.Header.Set("X-Raikada-Event", p.Event.Type)

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	body = string(buf[:n])
	_ = body256
	return resp.StatusCode, body, nil
}
```

- [ ] **Step 3: Write `smtp.go`**

```go
package notifications

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"
)

// SMTPSettings is the rendered subset of system_settings the SMTP path
// needs. Loaded fresh per dispatch (cheap; cached if needed).
type SMTPSettings struct {
	Host        string
	Port        int
	Username    string
	Password    string  // already decrypted by caller via Vault
	FromAddress string
	UseTLS      bool
}

const emailTemplate = `<!DOCTYPE html>
<html><body style="font-family: sans-serif">
<h2>{{.Site.Name}} — {{.Event.TypeDisplayName}}</h2>
<p><strong>Camera:</strong> {{.Event.Camera.DisplayName}}</p>
<p><strong>Source:</strong> {{.Event.Source}}</p>
<p><strong>When:</strong> {{.Event.OccurredAt}}</p>
{{if .ThumbnailURL}}<p><img src="{{.ThumbnailURL}}" alt="thumbnail" style="max-width:480px"/></p>{{end}}
<p><a href="{{.SnapshotURL}}">View full snapshot</a></p>
</body></html>`

func RenderEmail(p *Payload) (subject, htmlBody string, err error) {
	tmpl, err := template.New("email").Parse(emailTemplate)
	if err != nil {
		return "", "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return "", "", err
	}
	subject = fmt.Sprintf("[%s] %s — %s", p.Site.Name, p.Event.TypeDisplayName, p.Event.Camera.DisplayName)
	return subject, buf.String(), nil
}

func SendEmail(ctx context.Context, s SMTPSettings, recipient string, subject, htmlBody string) error {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	headers := []string{
		"From: " + s.FromAddress,
		"To: " + recipient,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	}
	msg := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + htmlBody)
	if s.UseTLS {
		return sendTLS(addr, auth, s.FromAddress, recipient, msg, s.Host)
	}
	return smtp.SendMail(addr, auth, s.FromAddress, []string{recipient}, msg)
}

func sendTLS(addr string, auth smtp.Auth, from, to string, msg []byte, host string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
```

- [ ] **Step 4: Write `dispatcher.go`** (the outbox-aware orchestrator)

```go
package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/outbox"
	"github.com/bluenviron/mediamtx/internal/store"
)

// SubscriptionView is a denormalized join row for matching.
type SubscriptionView struct {
	ID                    string
	TargetID              string
	EventTypeID           string
	CameraID              string
	MinSeverity           string
	QuietHoursStartMinute int
	QuietHoursEndMinute   int
}

// Dispatcher consumes the events bus, matches subscriptions, enqueues
// outbox rows, and runs the outbox processor that delivers them.
type Dispatcher struct {
	store     *store.Store
	vault     *cameracred.Vault
	events    *events.Service
	bus       <-chan events.Event
	unsub     func()
	site      SitePayload
	publicURL string
	signURL   func(eventID, kind string) string
	httpClient *http.Client
	mu        sync.RWMutex
	cache     []SubscriptionView
	cacheStaleAt time.Time
	logger    logger.Writer
}

func NewDispatcher(
	st *store.Store,
	vault *cameracred.Vault,
	evs *events.Service,
	site SitePayload,
	signURL func(eventID, kind string) string,
	log logger.Writer,
) *Dispatcher {
	bus, unsub := evs.Subscribe()
	return &Dispatcher{
		store: st, vault: vault, events: evs,
		bus: bus, unsub: unsub,
		site: site, signURL: signURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger: log,
	}
}

// Run blocks until ctx is cancelled. Spins up: (a) the bus consumer
// goroutine that enqueues outbox rows on matching subscriptions; (b)
// the outbox processor that delivers them.
func (d *Dispatcher) Run(ctx context.Context) {
	go d.runBusConsumer(ctx)
	d.runOutbox(ctx)
	d.unsub()
}

func (d *Dispatcher) runBusConsumer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-d.bus:
			if !ok {
				return
			}
			d.handleEvent(ctx, ev)
		}
	}
}

func (d *Dispatcher) handleEvent(ctx context.Context, ev events.Event) {
	subs, err := d.subscriptions(ctx)
	if err != nil {
		d.logger.Log(logger.Warn, "[notifications] load subscriptions: %v", err)
		return
	}
	cam, _ := d.store.Cameras.GetByID(ctx, ev.CameraID)
	evType, _ := d.store.EventTypes.GetByID(ctx, ev.TypeID)
	if cam == nil || evType == nil {
		return
	}
	now := time.Now().UTC()
	for _, sub := range subs {
		if !matches(sub, ev, now) {
			continue
		}
		p := Build(uuid.New().String(), d.site, &ev, cam, evType)
		p.SnapshotURL = d.signURL(ev.ID, "full")
		p.ThumbnailURL = d.signURL(ev.ID, "thumb")
		raw, _ := json.Marshal(p)
		row := &store.NotificationOutboxRow{
			ID: p.DeliveryID, TargetID: sub.TargetID, EventID: ev.ID,
			PayloadJSON: string(raw), State: "pending",
			NextAttemptAt: now, CreatedAt: now,
		}
		if err := d.store.NotificationOutbox.Insert(ctx, row); err != nil {
			d.logger.Log(logger.Warn, "[notifications] enqueue: %v", err)
		}
	}
}

func (d *Dispatcher) subscriptions(ctx context.Context) ([]SubscriptionView, error) {
	d.mu.RLock()
	if time.Now().Before(d.cacheStaleAt) && d.cache != nil {
		out := d.cache
		d.mu.RUnlock()
		return out, nil
	}
	d.mu.RUnlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().Before(d.cacheStaleAt) && d.cache != nil {
		return d.cache, nil
	}
	rows, err := d.store.NotificationSubscriptions.ListAllJoined(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SubscriptionView, len(rows))
	for i, r := range rows {
		out[i] = SubscriptionView{
			ID: r.ID, TargetID: r.TargetID,
			EventTypeID: r.EventTypeID, CameraID: r.CameraID, MinSeverity: r.MinSeverity,
			QuietHoursStartMinute: r.QuietHoursStartMinute,
			QuietHoursEndMinute:   r.QuietHoursEndMinute,
		}
	}
	d.cache = out
	d.cacheStaleAt = time.Now().Add(30 * time.Second)
	return out, nil
}

// InvalidateCache is called by API mutations on notification_targets/subscriptions.
func (d *Dispatcher) InvalidateCache() {
	d.mu.Lock()
	d.cacheStaleAt = time.Time{}
	d.mu.Unlock()
}

var severityRank = map[string]int{"": 0, "info": 1, "warning": 2, "critical": 3}

func matches(sub SubscriptionView, ev events.Event, now time.Time) bool {
	if sub.EventTypeID != "" && sub.EventTypeID != ev.TypeID {
		return false
	}
	if sub.CameraID != "" && sub.CameraID != ev.CameraID {
		return false
	}
	if sub.MinSeverity != "" && severityRank[ev.Severity] < severityRank[sub.MinSeverity] {
		return false
	}
	if sub.QuietHoursStartMinute >= 0 && sub.QuietHoursEndMinute >= 0 {
		minute := now.Hour()*60 + now.Minute()
		if inQuietHours(minute, sub.QuietHoursStartMinute, sub.QuietHoursEndMinute) {
			return false
		}
	}
	return true
}

func inQuietHours(minute, start, end int) bool {
	if end > start {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}

func (d *Dispatcher) runOutbox(ctx context.Context) {
	wsender := &WebhookSender{HTTP: d.httpClient, Timeout: 5 * time.Second}
	cfg := outbox.Config{
		Workers: d.workersFromSettings(ctx),
		MaxAttempts: d.maxAttemptsFromSettings(ctx),
	}
	funcs := outbox.Funcs{
		Claim: func(ctx context.Context, n int) ([]*outbox.Job, error) {
			rows, err := d.store.NotificationOutbox.ClaimBatch(ctx, n)
			if err != nil {
				return nil, err
			}
			out := make([]*outbox.Job, len(rows))
			for i, r := range rows {
				out[i] = &outbox.Job{ID: r.ID, Attempts: r.Attempts, PayloadJSON: r.PayloadJSON, Extra: r.TargetID}
			}
			return out, nil
		},
		Dispatch: func(ctx context.Context, j *outbox.Job) (outbox.Result, string, error) {
			targetID := j.Extra.(string)
			tgt, err := d.store.NotificationTargets.GetByID(ctx, targetID)
			if err != nil || tgt == nil || !tgt.Enabled {
				return outbox.ResultDead, "target missing or disabled", nil
			}
			var p Payload
			if err := json.Unmarshal([]byte(j.PayloadJSON), &p); err != nil {
				return outbox.ResultDead, "payload unmarshal: " + err.Error(), nil
			}
			switch tgt.Kind {
			case "webhook":
				secret, derr := d.vault.Decrypt(tgt.WebhookSecretCiphertext, tgt.WebhookSecretNonce)
				if derr != nil {
					return outbox.ResultDead, "decrypt secret: " + derr.Error(), nil
				}
				status, body, err := wsender.Send(ctx, tgt.WebhookURL, secret, &p)
				if err != nil {
					return outbox.ResultRetry, fmt.Sprintf("transport: %v", err), nil
				}
				if status >= 200 && status < 300 {
					return outbox.ResultDelivered, "", nil
				}
				if status >= 400 && status < 500 {
					return outbox.ResultDead, fmt.Sprintf("HTTP %d: %s", status, body), nil
				}
				return outbox.ResultRetry, fmt.Sprintf("HTTP %d: %s", status, body), nil
			case "email":
				ss, err := d.smtpSettings(ctx)
				if err != nil {
					return outbox.ResultRetry, "smtp settings: " + err.Error(), nil
				}
				subject, html, err := RenderEmail(&p)
				if err != nil {
					return outbox.ResultDead, "render: " + err.Error(), nil
				}
				if err := SendEmail(ctx, ss, tgt.EmailAddress, subject, html); err != nil {
					return outbox.ResultRetry, "smtp send: " + err.Error(), nil
				}
				return outbox.ResultDelivered, "", nil
			default:
				return outbox.ResultDead, "unknown kind: " + tgt.Kind, nil
			}
		},
		MarkDelivered: d.store.NotificationOutbox.MarkDelivered,
		MarkFailed:    d.store.NotificationOutbox.MarkFailed,
		MarkDead:      d.store.NotificationOutbox.MarkDead,
	}
	outbox.NewProcessor(funcs, cfg, d.logger).Run(ctx)
}

func (d *Dispatcher) workersFromSettings(ctx context.Context) int {
	n, _ := d.store.SystemSettings.GetInt(ctx, "outbox_workers", 4)
	return n
}
func (d *Dispatcher) maxAttemptsFromSettings(ctx context.Context) int {
	n, _ := d.store.SystemSettings.GetInt(ctx, "outbox_max_attempts", 8)
	return n
}

func (d *Dispatcher) smtpSettings(ctx context.Context) (SMTPSettings, error) {
	get := func(k string) string {
		s, _ := d.store.SystemSettings.Get(ctx, k)
		if s == nil {
			return ""
		}
		return s.Value
	}
	port, _ := strconv.Atoi(get("smtp_port"))
	if port == 0 {
		port = 587
	}
	pwCT, _ := hexToBytes(get("smtp_password_ciphertext"))
	pwNonce, _ := hexToBytes(get("smtp_password_nonce"))
	pw := ""
	if len(pwCT) > 0 && len(pwNonce) > 0 {
		decoded, err := d.vault.Decrypt(pwCT, pwNonce)
		if err != nil {
			return SMTPSettings{}, err
		}
		pw = string(decoded)
	}
	useTLS := get("smtp_use_tls") == "true"
	host := get("smtp_host")
	if host == "" {
		return SMTPSettings{}, errors.New("smtp_host not configured")
	}
	return SMTPSettings{
		Host: host, Port: port,
		Username: get("smtp_username"), Password: pw,
		FromAddress: get("smtp_from_address"), UseTLS: useTLS,
	}, nil
}

// hexToBytes is a tiny helper; system_settings stores binary as hex
// (textual k/v table). Real impl uses encoding/hex.
func hexToBytes(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	// import "encoding/hex"; return hex.DecodeString(s)
	panic("implement with encoding/hex")
}
```

(Replace the `hexToBytes` panic stub with `encoding/hex.DecodeString` when implementing.)

- [ ] **Step 5: Write tests** (focus on signature + matching)

```go
// internal/notifications/payload_test.go
package notifications

import (
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

func TestBuild(t *testing.T) {
	ev := &events.Event{
		ID: "ev-1", CameraID: "cam-1", TypeID: "motion", Source: "onvif_pullpoint",
		OccurredAt: time.Now(), ReceivedAt: time.Now(), Severity: "info",
	}
	cam := &store.Camera{ID: "cam-1", Name: "front_door", DisplayName: "Front Door"}
	evType := &store.EventType{ID: "motion", DisplayName: "Motion"}
	site := SitePayload{ID: "site-1", Name: "My House"}
	p := Build("d-1", site, ev, cam, evType)
	if p.Schema != PayloadSchema || p.DeliveryID != "d-1" || p.Event.Camera.ID != "cam-1" {
		t.Errorf("payload: %+v", p)
	}
}
```

```go
// internal/notifications/webhook_test.go
package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookSender_SignsAnd2xx(t *testing.T) {
	secret := []byte("topsecret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		got := r.Header.Get("X-Raikada-Signature")
		if got != want {
			t.Errorf("sig mismatch: got %s want %s", got, want)
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	ws := &WebhookSender{HTTP: srv.Client(), Timeout: time.Second}
	status, _, err := ws.Send(context.Background(), srv.URL, secret, &Payload{Schema: PayloadSchema, DeliveryID: "d-1"})
	if err != nil || status != 204 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}
```

- [ ] **Step 6: Run + commit**

```bash
go test ./internal/notifications/ -v
git add internal/notifications/
git commit -m "feat(notifications): webhook + SMTP delivery + outbox dispatcher"
```
