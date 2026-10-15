# Running the consumer NVR on a NAS

How to get the recorder running on a Docker-capable NAS (Synology,
QNAP, Unraid, TrueNAS SCALE, or any Linux box with Docker) and use the
web UI to adopt your cameras, watch live video, and browse recordings.

## What you need

- A NAS with Docker (Synology: **Container Manager**; QNAP: Container
  Station; Unraid: built-in; TrueNAS SCALE: Apps → custom app or ssh).
- ssh access to the NAS (first-time build + reading the initial admin
  password are easiest over ssh).
- Cameras on the same LAN/VLAN as the NAS (discovery is multicast; it
  does not cross subnets).

## 1 — Get the image onto the NAS

**Option A — build on the NAS (simplest, no registry):**

```bash
ssh <nas>
git clone <your-repo-url> raikada && cd raikada    # branch: consumer-foundation
cd docker && docker compose up -d --build
```

First build takes a while (it compiles ffmpeg 8.0 shared libs from
source — go-astiav pins that exact major, no distro ships it). Later
builds reuse the cached ffmpeg stage.

**Option B — build elsewhere, ship the image (no toolchain on the NAS):**

```bash
# On a dev machine with Docker (pick the NAS's CPU arch):
docker buildx build --platform linux/amd64 \
  -f docker/consumer.Dockerfile -t raikada-nvr:latest --load .
docker save raikada-nvr:latest | ssh <nas> docker load
# then copy docker/docker-compose.yml (drop the build: block) and
# docker/consumer-config.yml to the NAS and `docker compose up -d`.
```

Most x86 NAS boxes are `linux/amd64`; ARM models (many QNAPs, Pis) are
`linux/arm64`. `uname -m` on the NAS tells you (`x86_64` → amd64,
`aarch64` → arm64).

## Synology quickstart (DSM 7.2+, x86 models)

Verified against the real target (DS at 192.168.1.220, x86_64):

1. **Package Center → Container Manager → Install** (Synology's Docker;
   gives you `docker` + `docker compose`, used with `sudo`).
2. **Control Panel → Terminal & SNMP → Enable SSH** if not already on.
3. DSM has no git — copy the repo from your workstation instead:
   ```bash
   rsync -a --exclude node_modules --exclude .git \
     <repo-checkout>/ <user>@<nas-ip>:~/raikada/
   ```
4. File Station: create `docker/raikada/data` on volume1 and a shared
   folder for footage (e.g. `surveillance`), then edit
   `~/raikada/docker/docker-compose.yml` to bind-mount them (see §2).
5. ```bash
   cd ~/raikada/docker && sudo docker compose up -d --build
   sudo docker logs raikada-nvr 2>&1 | grep -A6 "BOOTSTRAP ADMIN"
   ```
6. Browse to `https://<nas-ip>:9997`, sign in, adopt cameras.

First build compiles ffmpeg from source — 10–30 min on NAS-class CPUs;
subsequent rebuilds reuse the cached stage.

## 2 — Storage layout (edit docker-compose.yml before `up`)

Two volumes, different lifecycles:

| Mount | Contents | Guidance |
|---|---|---|
| `/data` | identity keys, SQLite DB, TLS certs | tiny; protected storage; **back it up** — losing it means re-adopting cameras and re-creating users |
| `/recordings` | video segments, snapshots, clip exports | big; point at your storage pool |

On Synology, replace the named volumes with bind mounts, e.g.:

```yaml
    volumes:
      - /volume1/docker/raikada/data:/data
      - /volume1/surveillance:/recordings
```

Keep `network_mode: host` — camera discovery (WS-Discovery multicast)
and mDNS need it, and it avoids mapping the RTSP/WebRTC UDP port
spread. Check nothing else on the NAS already uses ports 9997, 8554,
8888, 8889, 1935, 8890 (Synology Surveillance Station uses none of
these by default).

## 3 — First boot

```bash
docker logs raikada-nvr 2>&1 | grep -A6 "BOOTSTRAP ADMIN"
```

Copy the one-time password, then browse to **`https://<nas-ip>:9997`**
(accept the self-signed cert warning), sign in as `admin`, and set your
real password when prompted. Replace the TLS cert later under
Settings → TLS if you want the warning gone.

## 4 — Adopt your cameras

Cameras page → **"Discovered on your network"** lists everything
answering ONVIF WS-Discovery. Per camera: **Adopt** → name it + enter
its RTSP credentials. The recorder probes the camera, picks the
vendor's own RTSP URL, stores credentials encrypted, and starts
recording immediately under the default policy (continuous, 14-day
retention — change under Policies).

Non-ONVIF sources: **Add Camera** and paste an RTSP URL manually.

## 5 — Daily use

- **Live view** — Cameras page; every camera is also restreamed at
  `rtsp://<nas-ip>:8554/<camera_name>` and HLS
  `http://<nas-ip>:8888/<camera_name>` for VLC/players.
- **Recordings** — Recordings page: per-camera timeline, playback,
  per-segment download.
- **Events** — motion/person/vehicle/doorbell from the cameras' own
  detection, each with a snapshot thumbnail; **Export clip** cuts a
  trimmed MP4 around the event.
- **Notifications** — add a webhook or SMTP target + subscriptions to
  get pushed on events.
- **Users** — add `viewer` accounts for household members (read/watch,
  no admin).

## 6 — Upgrades

```bash
cd raikada && git pull
cd docker && docker compose up -d --build
```

`/data` and `/recordings` live on volumes, so state (cameras, users,
events, footage) survives rebuilds. Watch
`docker logs raikada-nvr` for the schema migrations applying.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Discovered list empty | bridged networking (use `network_mode: host`) or cameras on another VLAN |
| Camera adopt → "rejected credentials" | wrong RTSP user/pass; Amcrest doorbells lock ONVIF after repeated failures — power-cycle the doorbell, or Add Camera manually (RTSP keeps working during ONVIF lockout) |
| Camera card not Live | `docker logs raikada-nvr \| grep <camera_name>` — look for 401 (bad creds) vs timeout (wrong IP/VLAN) |
| Port conflict on 9997/8554/8888 | another NAS service owns it; change the port in consumer-config.yml |
| UI unreachable | NAS firewall — allow 9997/tcp (and 8888/8554 for direct players) |
