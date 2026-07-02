# SP2 Camera Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Discover cameras on the LAN, adopt them with one credentialed probe, record their capabilities, and track per-camera health with `camera.online`/`camera.offline` events.

**Architecture:** Three new leaf packages (`internal/discovery`, `internal/camerahealth`, plus `MediaClient`/`ProbeCapabilities` additions inside `internal/onvif`) wired by `internal/core`, surfaced by `internal/api`. Everything funnels through existing seams: `cameras.Service` for camera writes, `events.Service.Insert` for health events, existing store repos (`CameraCapabilities`, `CameraHealth`).

**Tech Stack:** Go, SOAP/WS-Security (existing hand-rolled envelope machinery in `internal/onvif`), SQLite via existing repos, React SPA under `web/src`.

**Spec:** `docs/superpowers/specs/2026-07-02-sp2-camera-lifecycle-design.md`

---

### Task 1: `onvif.MediaClient` — GetCapabilities / GetProfiles / GetStreamUri / GetSnapshotUri

**Files:**
- Create: `internal/onvif/media.go`
- Create: `internal/onvif/media_test.go` (fixtures: captured Amcrest SOAP responses inline as consts)
- Reference: `internal/onvif/device_info.go` (reuse `soapEnvelope`/UsernameToken helpers — extract shared helper if currently private to device_info)

**Interfaces:**

```go
// MediaProfile is one ONVIF media profile (token + what it carries).
type MediaProfile struct {
    Token       string `json:"token"`
    Name        string `json:"name"`
    VideoCodec  string `json:"video_codec"`  // "H264"|"H265"|...
    Width       int    `json:"width"`
    Height      int    `json:"height"`
    HasAudio    bool   `json:"has_audio"`
}

// Capabilities is the parsed tds:GetCapabilities subset SP2 needs.
type Capabilities struct {
    MediaXAddr  string
    EventsXAddr string
    HasPTZ      bool
    HasImaging  bool
    HasIO       bool
}

type MediaClient struct { // mirrors DeviceClient fields: XAddr, Username, Password, HTTPClient, Now, NewNonce
}

func (c *DeviceClient) GetCapabilities(ctx context.Context) (*Capabilities, error)
func (c *MediaClient) GetProfiles(ctx context.Context) ([]MediaProfile, error)
func (c *MediaClient) GetStreamURI(ctx context.Context, profileToken string) (string, error)
func (c *MediaClient) GetSnapshotURI(ctx context.Context, profileToken string) (string, error)
```

- [ ] Step 1: failing tests — `TestGetCapabilitiesParsesAmcrest`, `TestGetProfilesParsesAmcrest`, `TestGetStreamURIStripsUserinfo`, `TestGetSnapshotURI` against `httptest.Server` returning fixture SOAP; assert parsed fields.
- [ ] Step 2: run, verify FAIL (undefined types).
- [ ] Step 3: implement envelope builders + parsers (namespace-aware, tolerate prefix variation like discovery.go does).
- [ ] Step 4: run, verify PASS. `go test ./internal/onvif/`.
- [ ] Step 5: commit `feat(onvif): MediaClient — capabilities, profiles, stream/snapshot URIs`.

### Task 2: `onvif.ProbeCapabilities` composition + report

**Files:**
- Create: `internal/onvif/probe.go`, `internal/onvif/probe_test.go`

```go
// CapabilityReport aggregates one full probe. Persisted by the API
// layer into store.CameraCapabilities.
type CapabilityReport struct {
    Device        DeviceInformation
    Profiles      []MediaProfile
    SelectedToken string // highest-res H264/H265 profile, audio preferred on tie
    StreamURI     string // no userinfo
    SnapshotURI   string
    HasAudio, HasPTZ, HasMotion, HasIO, HasImaging bool
}

func ProbeCapabilities(ctx context.Context, xaddr, username, password string) (*CapabilityReport, error)
```

- HasMotion = events XAddr present (PullPoint capable). Strip any userinfo from returned URIs. Profile selection: sort by Width*Height desc, prefer HasAudio on equal area, first wins.
- [ ] Step 1: failing tests — selection table test (`TestSelectProfilePrefersResolutionThenAudio`), end-to-end probe against fixture server (`TestProbeCapabilitiesAmcrest`), auth-failure surfaces typed error (`TestProbeCapabilitiesBadCredentials` → `ErrUnauthorized`).
- [ ] Step 2: FAIL → implement → PASS.
- [ ] Step 3: commit `feat(onvif): ProbeCapabilities composite probe`.

### Task 3: `internal/discovery` cache service

**Files:**
- Create: `internal/discovery/discovery.go`, `internal/discovery/discovery_test.go`

```go
// Prober abstracts onvif discovery for tests.
type Prober interface{ Probe(ctx context.Context) ([]onvif.DiscoveredDevice, error) }

// CameraLister abstracts the store for matching.
type CameraLister interface{ List(ctx context.Context, f store.ListCamerasFilter) ([]*store.Camera, error) }

type Entry struct {
    onvif.DiscoveredDevice
    FirstSeenAt, LastSeenAt time.Time
    MatchedCameraID string `json:"matched_camera_id,omitempty"`
}

type Service struct{ /* mu, cache map[endpointRef]Entry, prober, cameras, logger, clock */ }

func New(p Prober, c CameraLister, log logger.Writer) *Service
func (s *Service) Run(ctx context.Context)                    // 60s ticker + initial probe
func (s *Service) ProbeNow(ctx context.Context) ([]Entry, error)
func (s *Service) Snapshot() []Entry                          // sorted by LastSeenAt desc
func (s *Service) Lookup(endpointRef string) (Entry, bool)
```

- Aging: entries with `LastSeenAt` older than 10min dropped at each probe round. Matching: host of camera `source_url`/`onvif_xaddr` == host of entry XAddr. Injected clock (`func() time.Time`) — no bare `time.Now()` in logic, matches repo test style.
- [ ] Step 1: failing tests — dedup by endpoint ref across rounds preserves FirstSeenAt; aging drops stale; matching flags managed cameras; ProbeNow error keeps stale cache.
- [ ] Step 2: FAIL → implement → PASS.
- [ ] Step 3: commit `feat(discovery): WS-Discovery cache service with camera matching`.

### Task 4: `internal/camerahealth` collector

**Files:**
- Create: `internal/camerahealth/collector.go`, `internal/camerahealth/collector_test.go`

```go
// PathSnapshot is what the collector needs per camera path per poll.
type PathSnapshot struct{ Name string; Online bool; LastFrameAt time.Time }

type PathLister interface{ ListPaths(ctx context.Context) ([]PathSnapshot, error) }
type EventSink interface{ Insert(ctx context.Context, ev *events.Event) error }

type Collector struct{ /* store *store.Store, cameras CameraLister, paths PathLister, sink EventSink, clock, interval(5s), debounce(30s), failThreshold(5) */ }

func New(...) *Collector
func (c *Collector) Run(ctx context.Context)
func (c *Collector) Touch(cameraID string, lastEventAt time.Time)  // SP3 seam: stamps last_event_at on next flush
func (c *Collector) pollOnce(ctx context.Context)                  // exported for tests as PollOnce
```

State machine per spec: `connected` (online), `reconnecting` (path present, not online, fails<5), `failed` (fails≥5), `idle` (disabled/no path). Health rows upserted on state change or 60s heartbeat. Events: `connected→(reconnecting|failed)` sustained ≥ debounce → insert `camera.offline` (severity warning, source `health`, payload `{"from":"connected","state":"failed","last_error":...}`); any→`connected` after an offline was emitted → `camera.online` (info). Poll list timeout 2s → skip round (not a failure increment).

- [ ] Step 1: failing table-driven tests — transitions (online→offline after threshold+debounce, flap within debounce emits nothing, recovery emits online exactly once), heartbeat upsert cadence, Touch stamps last_event_at, poll timeout is neutral. Use in-memory store (store.Open on t.TempDir) + fake PathLister/clock.
- [ ] Step 2: FAIL → implement → PASS.
- [ ] Step 3: commit `feat(camerahealth): poll-based collector with online/offline events`.

### Task 5: API surface

**Files:**
- Create: `internal/api/api_v1_discovery.go`, `internal/api/api_v1_discovery_test.go`
- Modify: `internal/api/api_v1_camera_extensions.go` (probe 501 → real; add capabilities GET)
- Modify: `internal/api/api.go` (route registration + service handles: `Discovery *discovery.Service`, probe func injection)
- Modify: `internal/rbac/perms.go` only if `camera.probe` grants missing from roles (verify: PermCameraProbe exists; ensure RoleAdmin includes it)

Routes (perms per spec): GET `/v1/discovery/cameras`, POST `/v1/discovery/probe`, POST `/v1/discovery/adopt`, POST `/v1/cameras/:id/probe`, GET `/v1/cameras/:id/capabilities`.

Adopt handler order (compensating writes): lookup entry (404) → `onvif.ProbeCapabilities` (401→400 `camera rejected credentials`; net err→502) → create camera via existing conf-path create internals (extract `createCameraLocked(cam) (defs.Camera, error)` from onV1CamerasPost so adopt reuses name/policy/conf logic + store sync) → `CamerasService.SetCredentials` → `Store.CameraCapabilities.Upsert` → on any failure after create: delete camera (service + conf) and return error. Audit: `camera.adopted` via `emitMutationAudit` + the existing config.applied event. Response: 201 with the created camera + capability summary.

- [ ] Step 1: failing handler tests on the `startCameraAPI` harness + fake prober/probe func: adopt happy path writes camera+creds+capabilities; adopt with failing capability upsert unwinds camera row; probe endpoint refreshes row; capabilities GET returns stored JSON; discovery list/probe endpoints serve the fake service.
- [ ] Step 2: FAIL → implement → PASS (`go test ./internal/api/ -run "Discovery|Adopt|Capabilit|CameraProbe"`).
- [ ] Step 3: commit `feat(api): discovery list/probe/adopt + capability probe endpoints`.

### Task 6: core wiring

**Files:**
- Modify: `internal/core/core.go` (fields `discoverySvc`, `healthCollector`; start after camerasService/pathManager in createResources; stop in closeResources full-shutdown branch)
- Modify: `internal/core/foundation_wiring.go` (adapters: `pathListerAdapter` wrapping `pathManager.APIPathsList` → `[]camerahealth.PathSnapshot` (Online + OnlineTime; LastFrameAt from ReadyTime), `discoveryProberAdapter` wrapping `onvif.Discoverer`)
- Modify: API construction site to hand `Discovery` + probe func + health `Touch` through (find where `&API{...}` is built in core).
- Test: `internal/core/foundation_wiring_test.go` — adapter mapping unit tests (APIPath → PathSnapshot field mapping).

- [ ] Step 1: failing adapter-mapping test → implement adapters + wiring → PASS.
- [ ] Step 2: `go generate ./internal/core/... && go build ./...` clean; boot smoke: recorder starts, log shows discovery probe round + health collector first upserts.
- [ ] Step 3: commit `feat(core): wire discovery service + camera health collector`.

### Task 7: SPA

**Files:**
- Modify: `web/src/routes/Cameras.tsx` (Discovered section + Adopt modal + health badge per card)
- Modify: `web/src/api.ts` or equivalent client module (types + `getDiscoveredCameras`, `probeDiscovery`, `adoptCamera`, `getCameraCapabilities`, `getCameraHealth` already present?)
- Modify: camera drawer component (health tab live fields, capability chips)
- Rebuild: `make web` → commit `internal/web/dist` diff (foundation constraint: every SPA change is followed by `make web` and the dist diff rides the same commit).

- [ ] Step 1: add client methods + types.
- [ ] Step 2: Discovered section: poll `GET /v1/discovery/cameras` every 30s while page open; card per unmatched entry (model/vendor/IP), Adopt → modal (name, username, password) → POST adopt → toast + refresh cameras. Matched entries render as muted "managed".
- [ ] Step 3: health badge: green `connected` / amber `reconnecting` / red `failed` / grey `idle|unknown` from `GET /v1/cameras/:id/health` (batch: reuse cameras list polling).
- [ ] Step 4: `make web`; manual check via recorder; commit `feat(web): discovery + adopt UI, camera health badges, capability chips`.

### Task 8: live acceptance + runbook + tag

- [ ] Boot scratch instance (fresh identity), complete setup via API.
- [ ] `GET /v1/discovery/cameras` lists all 3 LAN cameras with vendor/model from scopes.
- [ ] Adopt `192.168.1.110` end-to-end with real creds: probed StreamURI ends `/cam/realmonitor?...`, capabilities row has profiles + has_audio, stream goes live, recording starts.
- [ ] Health: `rtsp_state:"connected"` within 15s (F4 criterion reactivated). Simulate failure (rotate to wrong password) → within debounce+threshold `camera.offline` event exists + webhook target receives it; restore → `camera.online`.
- [ ] Update `docs/superpowers/runbooks/real-camera-smoke.md`: Step 2 becomes discover→adopt path (manual add stays as fallback); F4 note replaced with live criterion.
- [ ] Full test sweep `go test -p 1 ./internal/...` (accept the 19 pre-existing core flakes only), `go vet`.
- [ ] Commit docs; tag `v1.1.0-camera-lifecycle`; push branch + tag.

## Self-review notes

- Spec coverage: discovery cache/API ✓ (T3/T5), adopt ✓ (T5), MediaClient+probe ✓ (T1/T2), health collector + events ✓ (T4), SPA ✓ (T7), re-probe + capabilities endpoints ✓ (T5), live acceptance ✓ (T8).
- Type checks: `Entry.MatchedCameraID` string (not pointer) — JSON omitempty; `PathSnapshot.LastFrameAt` sourced from `APIPath.ReadyTime` (deprecated but populated) — verify at T6, fall back to `OnlineTime`.
- The `camera.online`/`camera.offline` event type ids must exist in seeded `event_types`; verify seed list at T4 start and add a migration if absent.
