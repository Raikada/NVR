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
2. **Recorder running** at `https://localhost:9997` (or your configured listen addr):
   ```bash
   /tmp/raikada --confpath /tmp/raikada-test.yml
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

## Step 2 — Add a camera (manual path)

Discovery + camera-side pairing live in **sub-project 2** (not yet built); foundation only supports manual camera addition.

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
- `GET /v1/cameras/<id>` returns the camera with `password_set: true` and **no plaintext password** in the response.
- `GET /v1/cameras/<id>/health` returns `rtsp_state: "connected"`.
- The path manager is serving the live stream at `rtsp://localhost:8554/<name>` (you can verify with `ffplay -rtsp_transport tcp rtsp://localhost:8554/front_door`).

---

## Step 3 — Verify recording

1. Wait 60–120 seconds.
2. Navigate to **Recordings** → select the camera.
3. Confirm at least one segment appears with non-zero duration.

**Pass criteria:** `ls <recordings_root>/<camera_name>/` shows `.mp4` files. `GET /v1/recording-segments?camera_id=<id>` returns at least one row.

---

## Step 4 — Trigger an event manually

Sub-project 3 (vendor event channels) and sub-project 4 (snapshot fetch) are not yet built. For foundation, you can trigger an event manually via the API to validate the events pipeline + notification dispatch:

```bash
curl -k -X POST "https://localhost:9997/v1/events" \
  -H "Authorization: Bearer <your_jwt>" \
  -H "Content-Type: application/json" \
  -d '{
    "camera_id": "<camera_id>",
    "type_id": "motion",
    "source": "manual",
    "severity": "info",
    "payload_json": "{}"
  }'
```

**Pass criteria:** the event appears in `/v1/events` immediately; `expires_at` is materialized to ~90 days from now (motion's seeded retention).

---

## Step 5 — Webhook delivery

1. Get a free webhook test URL from `https://webhook.site` (note your unique URL).
2. Navigate to **Notifications** → **Targets** → **Add Webhook**.
3. URL = your webhook.site URL. Generate a secret. Save.
4. Add a **Subscription** with: target × `event_type=motion` × `camera=<your camera>`.
5. Re-run the manual event-trigger from Step 4 (or wave at the camera if motion detection is wired up to the camera-side and ONVIF events are active — only relevant once sub-project 2 lands).
6. Within ~10s, the webhook.site URL should receive a POST with:
   - `Content-Type: application/json`
   - `X-Raikada-Signature: <hex hmac>`
   - `X-Raikada-Delivery: <uuid>`
   - `X-Raikada-Event: motion`
   - JSON body matching `raikada.event.v1` schema

**Pass criteria:** webhook.site shows the POST, signature header is present, body validates against the schema.

---

## Step 6 — Audit log spot-check

Every mutation you made above should be in the audit log:

```bash
curl -k "https://localhost:9997/v1/audit?limit=50" \
  -H "Authorization: Bearer <your_jwt>"
```

Expect rows for: `auth.login.success`, `auth.password_changed`, `system.bootstrap_completed`, `camera.created`, `camera.credentials_rotated`, `notification_target.created`, `notification_subscription.created`, `event.acknowledged` (if you ack'd one).

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
# Bootstrap-only fields. Cameras / policies / users / events live in DB now.
identityDir: /tmp/raikada-identity
recordingsDir: /tmp/raikada-recordings
databasePath: /tmp/raikada-identity/recorder.db
logLevel: info
api: yes
apiAddress: :9997
apiEncryption: yes  # HTTPS with self-signed default

# RTSP server stays plaintext on LAN by default; flip to rtspsAddress for TLS.
rtspAddress: :8554

# Optional TLS overrides; defaults to <identityDir>/tls.crt + tls.key.
# tls:
#   cert_path: /etc/raikada/cert.pem
#   key_path:  /etc/raikada/key.pem

# Optional mDNS advertisement (default on).
mdns: true
```

(Schema field names may differ slightly once Phase 4 lands — check `internal/conf/conf.go` on the `consumer-foundation` branch for the canonical list.)
