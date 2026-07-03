# Real-Camera Smoke Test (Run On Wake)

This runbook validates the consumer NVR foundation against your **own LAN cameras**. It exists because the overnight implementation could not test against real hardware (the agents had no access to your network or camera credentials). Run this when you're awake and at the recorder host.

---

## Prerequisites

1. **Recorder built** — from the `consumer-foundation` branch in `../NVR-consumer-foundation`:
   ```bash
   cd /Users/ethanflower/raikada-consumer/NVR-consumer-foundation
   go generate ./internal/core/... ./internal/servers/hls/...   # populates VERSION + hls.min.js
   go build -o /tmp/raikada ./
   ```
2. **Recorder running** at `https://localhost:9997` (or your configured listen addr) — note `confpath` is a positional argument:
   ```bash
   /tmp/raikada /tmp/raikada-test.yml
   ```
   (Generate a minimal `raikada-test.yml` — see "Minimal config" at the bottom of this runbook.)
3. **At least one ONVIF-capable camera** reachable on the LAN with known IP + credentials.
4. A web browser; accept the self-signed cert warning on first visit.

---

## Step 1 — Setup wizard

1. Open `https://localhost:9997` (or `https://<recorder-hostname>.local:9997`). The browser will warn about the self-signed cert; click through.
2. The first-run wizard should appear. If not, check `GET /v1/system/setup-status` returns `setup_required: true`.
3. Walk the wizard:
   - Enter the **initial admin password** printed at first start (also at `<identityDir>/initial-admin-password.txt`).
   - Set a new admin password.
   - Site name (e.g. "Home"), timezone (auto-detected — confirm), language.
   - **Skip** TLS upload + SMTP — we'll come back to those if needed.
4. Wizard completes; dashboard loads.

**Pass criteria:** dashboard renders. `local_users.must_change_password` is now 0 for the admin user (verify: `sqlite3 <identityDir>/recorder.db "SELECT must_change_password FROM local_users"` → returns 0).

---

## Step 2 — Add a camera

SP2 shipped discover → review → adopt: the Cameras page lists cameras
found on the LAN (WS-Discovery); click **Adopt**, enter a name +
credentials, and the recorder probes ONVIF capabilities, picks the
vendor's blessed RTSP URL, stores credentials in the vault, and starts
recording. Use that path first; the manual form below stays as the
fallback for non-ONVIF sources.

1. Navigate to **Cameras** → **Add Camera**.
2. Enter:
   - **Name:** short slug, lowercase + underscores (e.g. `front_door`)
   - **Display Name:** human-readable (e.g. "Front Door")
   - **Source URL** (no userinfo): RTSP URL exactly as your camera vendor documents.
     - **Amcrest:** `rtsp://<camera-ip>:554/cam/realmonitor?channel=1&subtype=0`
     - **Hikvision:** `rtsp://<camera-ip>:554/Streaming/Channels/101`
     - **Reolink:** `rtsp://<camera-ip>:554/h264Preview_01_main`
   - **ONVIF XAddr** (optional but recommended): `http://<camera-ip>/onvif/device_service` (some cameras use port 8000 or 80; check your vendor docs)
   - **Recording Policy:** leave as `Default Policy` for now
3. Save the camera.
4. Open the camera detail → **Credentials** tab → enter username + password. Save.
5. Within ~10s, the camera card should show **Live** with a working video preview.

**Pass criteria:**
- `PUT /v1/cameras/<id>/credentials` returns `password_set: true` and **no plaintext password** appears in any response, in the persisted `mediamtx.yml`, or in the audit log.
- The path manager is serving the live stream at `rtsp://localhost:8554/<name>` (you can verify with `ffplay -rtsp_transport tcp rtsp://localhost:8554/front_door`).
- The recorder log shows `[path <name>] stream is available and online` and no `401 (Unauthorized)` loop.

`GET /v1/cameras/<id>/health` returns `rtsp_state: "connected"` within
~15s of the stream coming up (SP2 health collector; finding F4 closed).

---

## Step 3 — Verify recording

1. Wait 60–120 seconds.
2. Navigate to **Recordings** → select the camera.
3. Confirm at least one segment appears with non-zero duration.

**Pass criteria:** `ls <recordings_root>/<camera_name>/` shows `.mp4` files. `GET /v1/recording-segments?camera_id=<id>` returns at least one row.

---

## Step 4 — Events pipeline (SP3)

Vendor event channels are live: Amcrest cameras stream events over the
CGI attach channel, everything else with an ONVIF XAddr uses PullPoint
(per-camera override via the camera's `event_channel` field / drawer
selector). Walk in front of a camera (or press the doorbell):

**Pass criteria:** a `motion` (and `person`/`vehicle` if the camera's
SmartMotion is enabled) event appears in `GET /v1/events` within ~5s,
carrying `snapshot_url`/`thumbnail_url` (SP4 signed URLs) that serve
real JPEGs; the doorbell press arrives as `doorbell`;
`camera_health.last_event_at` advances. Unplugging a camera produces
`camera_offline` (warning) after ~30s; recovery produces
`camera_online`.

---

## Step 5 — Webhook delivery

1. Stand up a webhook sink. A local listener keeps camera/site metadata
   off third-party services (preferred); `https://webhook.site` also works
   if you accept that trade-off.
2. Navigate to **Notifications** → **Targets** → **Add Webhook** (or
   `POST /v1/notification-targets` with `kind=webhook`). URL = your sink.
   Generate a secret. Save.
3. Add subscriptions (target × motion/doorbell) and trigger a real
   event per Step 4 — subscription-driven dispatch is live since SP3.
   `POST /v1/notification-targets/<id>/test` remains for a quick
   synthetic delivery check.
4. Within ~10s the sink should receive a POST with:
   - `Content-Type: application/json`
   - `X-Raikada-Signature: <hex hmac-sha256 of the body with your secret>`
   - `X-Raikada-Delivery: <uuid>`
   - `X-Raikada-Event: __test__`
   - JSON body matching `raikada.event.v1` schema

**Pass criteria:** the sink shows the POST, the signature verifies against
the secret, body validates against the schema. Note the dispatch timeout is
short (~1s); a cold/slow sink can time out the first attempt (finding F9).

---

## Step 6 — Audit log spot-check

Every mutation you made above should be in the audit log:

```bash
curl -k "https://localhost:9997/v1/audit?limit=50" \
  -H "Authorization: Bearer <your_jwt>"
```

Expect rows for: `auth.login.success`, `auth.session_started`, `auth.password_changed`, `camera.credentials_rotated`, `notification_target.created`, `notification_target.tested`, and one `config.applied` per camera create/patch/delete (the conf-path camera CRUD emits `config.applied` with a `verb` attribute rather than `camera.created`/`camera.deleted` — smoke finding F6; there is no `system.bootstrap_completed` row).

**Pass criteria:** none of the rows leak passwords, secrets, or RTSP userinfo. The `before_json`/`after_json` may contain `redacted: ["password"]` arrays — that's the redaction marker working.

---

## Cleanup

```bash
curl -k -X DELETE "https://localhost:9997/v1/cameras/<id>" \
  -H "Authorization: Bearer <your_jwt>"
```

Cascade deletes events + clips + snapshots + credentials + capabilities + health. ONVIF subscriptions are torn down application-side.

---

## Reporting back

If any step fails, capture:

1. The **recorder log** from the failing window (`journalctl -u raikada -n 500` or whatever you're using).
2. The relevant **`audit_log`** rows: `sqlite3 <identityDir>/recorder.db "SELECT * FROM audit_log WHERE occurred_at > datetime('now', '-15 minutes') ORDER BY occurred_at DESC"`.
3. The **browser console + network tab** for the failing API call.
4. The **camera response** if it's a camera-side issue — `curl -v http://<camera-ip>/onvif/device_service`.

Open a GitHub issue with these attached, or paste them into the next Claude conversation.

---

## Minimal config (`/tmp/raikada-test.yml`)

```yaml
# Bootstrap-only fields. Cameras / policies / users / events live in the
# SQLite DB at <identityDir>/recorder.db (there is no separate
# databasePath field).
identityDir: /tmp/raikada-identity
logLevel: info
api: yes
apiAddress: :9997
apiEncryption: yes  # HTTPS with self-signed default

# RTSP server stays plaintext on LAN by default; flip to rtspsAddress for TLS.
rtspAddress: :8554

# Recordings root: there is no recordingsDir field; set the per-path
# default record pattern instead (cameras inherit it via their policy).
pathDefaults:
  recordPath: /tmp/raikada-recordings/%path/%Y-%m-%d_%H-%M-%S-%f

# Optional mDNS advertisement (default on).
mdns: true
```

Heads-up: the recorder rewrites this file in place with the full persisted
config (including camera paths) after API mutations — that's expected.
TLS defaults to `<identityDir>/tls.crt` + `tls.key`; replace via
`PUT /v1/system/tls`. Canonical field list: `internal/conf/conf.go`.

---

## Step 7 — Snapshots + clips (SP4)

1. After a Step 4 event: `ls <snapshot_root>/<camera>/<date>/` shows
   `<event_id>-full.jpg` + `-thumb.jpg`; the event's `snapshot_url`
   fetches without auth (HMAC-signed, 15 min TTL).
2. `POST /v1/events/<id>/clip` → 201 with a signed `download_url`; the
   MP4 plays and spans occurred_at ±5s (per-request pre/post-roll
   override supported). A second POST returns the same clip (200).
3. Events page in the SPA lists the event with thumbnail, ack, and
   export-clip actions.

**Pass criteria:** JPEG pair on disk + rows in `event_snapshots`; webhook
payload `snapshot_url` fetches; clip downloads and plays; expired events
take their snapshot files with them (retention file pass).
