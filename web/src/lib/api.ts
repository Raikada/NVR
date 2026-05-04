// Typed wrapper over the recorder's /v1/ HTTP API.
//
// Hand-rolled types for the fields each route actually consumes —
// not a full openapi-typescript codegen, because most of the
// canonical surface (clips, recordings, segments, recording-policies,
// recorder-config block, stream-track discriminators) isn't surfaced
// by this UI yet, and a generated 5000-line types module would dwarf
// what we need. Add fields here as additional routes wire up.
//
// All fetches return parsed JSON or throw an ApiError with the HTTP
// status + parsed `{status,error}` body when the server returned a
// non-2xx response.

export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const BASE = '/v1';

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  };
  const res = await fetch(BASE + path, init);
  const text = await res.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }
  if (!res.ok) {
    const detail =
      typeof parsed === 'object' && parsed && 'error' in parsed && typeof parsed.error === 'string'
        ? parsed.error
        : `${method} ${path} failed: ${res.status}`;
    throw new ApiError(res.status, parsed, detail);
  }
  return parsed as T;
}

export const api = {
  get<T>(path: string): Promise<T> { return request<T>('GET', path); },
  post<T>(path: string, body?: unknown): Promise<T> { return request<T>('POST', path, body); },
  patch<T>(path: string, body?: unknown): Promise<T> { return request<T>('PATCH', path, body); },
  delete<T>(path: string): Promise<T> { return request<T>('DELETE', path); },
};

/* ---------- /v1/health ---------- */

export type HealthVolumeState = 'healthy' | 'degraded' | 'full' | 'read_only';

export interface HealthVolume {
  volume_id: string;
  used_pct: number;
  status: HealthVolumeState;
}

export interface HealthNetwork {
  management_server_reachable: boolean;
  cloud_reachable: boolean;
  last_sync_at?: string;
}

export interface HealthBandwidth {
  rx_bps: number;
  tx_bps: number;
}

export interface HealthStatus {
  id: string;
  recording_server_id: string;
  tenant_id: string;
  reported_at: string;
  // Go's time.Duration marshals to an int64 of nanoseconds; the SPA
  // formats it via formatUptime() in lib/format.ts.
  uptime: number;
  cpu_pct: number;
  mem_pct: number;
  cameras_total: number;
  cameras_recording: number;
  cameras_offline: number;
  storage?: HealthVolume[];
  network: HealthNetwork;
  bandwidth: HealthBandwidth;
  overall: 'healthy' | 'degraded' | 'unhealthy';
}

export const fetchHealth = () => api.get<HealthStatus>('/health');

/* ---------- /v1/cameras ---------- */

export type CameraSourceType =
  | 'rtsp'
  | 'rtsps'
  | 'rtmp'
  | 'rtmps'
  | 'hls'
  | 'webrtc'
  | 'srt'
  | 'rpicamera'
  | 'redirect'
  | 'mpegts_udp'
  | 'rtp'
  | 'publisher';

export interface CameraRuntime {
  online: boolean;
  available: boolean;
  last_online_at?: string;
}

export interface Camera {
  id: string;
  tenant_id: string;
  site_id: string;
  recording_server_id: string;
  name: string;
  source_type: CameraSourceType;
  source_url: string;
  credentials_ref?: string;
  recording_policy_id?: string;
  runtime?: CameraRuntime;
  created_at: string;
  updated_at: string;
}

export interface ListEnvelope<T> {
  items: T[];
  item_count: number;
  page_count: number;
}

export interface CameraList extends ListEnvelope<Camera> {
  notice?: string;
}

export const fetchCameras = (page = 0, perPage = 50) =>
  api.get<CameraList>(`/cameras?page=${page}&items_per_page=${perPage}`);

export const deleteCamera = (id: string) => api.delete<unknown>(`/cameras/${id}`);

export interface CameraCreateBody {
  name: string;
  source_type: CameraSourceType;
  source_url: string;
}

export const createCamera = (body: CameraCreateBody) => api.post<Camera>('/cameras', body);

export interface CameraPatchBody {
  name?: string;
  source_type?: CameraSourceType;
  source_url?: string;
  recording_policy_id?: string;
}

export const patchCamera = (id: string, patch: CameraPatchBody) =>
  api.patch<Camera>(`/cameras/${id}`, patch);

/* ---------- /v1/recording-policies ---------- */
//
// RecordingPolicy is the canonical entity defined in
// platform/docs/domain-model.md §RecordingPolicy. Wire shape is
// snake_case per ADR 0009; Go's time.Duration JSON-marshals to
// int64 nanoseconds, so every *_duration / *_buffer field on the
// wire is a number-of-nanoseconds, not a string. Use the helpers
// in lib/duration.ts to convert between nanoseconds and the
// human-friendly units the UI surfaces (days, seconds).
//
// The seeded "Default" policy ships at boot with the deterministic
// UUID DEFAULT_RECORDING_POLICY_ID — clients can rely on its
// presence and the recorder rejects deleting it through the regular
// "policy is referenced by N cameras" path-conflict check.

export const DEFAULT_RECORDING_POLICY_ID = '00000000-0000-0000-0000-000000000001';

export type RecordingPolicyMode =
  | 'continuous'
  | 'motion'
  | 'schedule'
  | 'event_triggered'
  | 'off';

export type RecordingPolicyContainer = 'fmp4' | 'mpegts';

export interface ScheduleWindow {
  days: string[];
  start: string; // HH:MM
  end: string;   // HH:MM
}

export interface RecordingPolicySchedule {
  timezone: string;
  windows: ScheduleWindow[];
}

export interface RecordingPolicy {
  id: string;
  tenant_id: string;
  name: string;
  mode: RecordingPolicyMode;
  schedule?: RecordingPolicySchedule;
  retention_duration: number;       // nanoseconds
  min_segment_duration: number;     // nanoseconds
  max_segment_duration: number;     // nanoseconds
  container: RecordingPolicyContainer | '';
  pre_event_buffer?: number;        // nanoseconds
  post_event_buffer?: number;       // nanoseconds
  enabled: boolean;
  part_duration: number;            // nanoseconds
  max_part_size: number;            // bytes
  record_path_template?: string;
  created_at: string;
  updated_at: string;
}

export interface RecordingPolicyList extends ListEnvelope<RecordingPolicy> {}

// Body shape for POST / PATCH. All fields are optional on PATCH;
// on POST `name` and `mode` are the minimum viable. Server stamps
// id / tenant_id / created_at / updated_at.
export type RecordingPolicyWriteBody = Partial<Omit<RecordingPolicy, 'id' | 'tenant_id' | 'created_at' | 'updated_at'>>;

export const fetchRecordingPolicies = () =>
  api.get<RecordingPolicyList>('/recording-policies?items_per_page=200');

export const fetchRecordingPolicy = (id: string) =>
  api.get<RecordingPolicy>(`/recording-policies/${id}`);

export const createRecordingPolicy = (body: RecordingPolicyWriteBody) =>
  api.post<RecordingPolicy>('/recording-policies', body);

export const updateRecordingPolicy = (id: string, patch: RecordingPolicyWriteBody) =>
  api.patch<RecordingPolicy>(`/recording-policies/${id}`, patch);

export const deleteRecordingPolicy = (id: string) =>
  api.delete<{ status: string }>(`/recording-policies/${id}`);

/* ---------- /v1/events ---------- */

export type EventSeverity = 'info' | 'warning' | 'error';

export interface Event {
  id: string;
  recording_server_id: string;
  tenant_id: string;
  site_id: string;
  occurred_at: string;
  kind: string;
  severity: EventSeverity;
  subject_kind: string;
  subject_id: string;
  message: string;
  attributes?: Record<string, string>;
  correlation_id?: string;
}

export interface EventList extends ListEnvelope<Event> {
  notice?: string;
}

export const fetchEvents = (params: {
  page?: number;
  perPage?: number;
  cameraId?: string;
  severity?: string;
  kind?: string[];
} = {}) => {
  const qs = new URLSearchParams();
  qs.set('page', String(params.page ?? 0));
  qs.set('items_per_page', String(params.perPage ?? 100));
  if (params.cameraId) qs.set('camera_id', params.cameraId);
  if (params.severity) qs.set('severity', params.severity);
  for (const k of params.kind ?? []) qs.append('kind', k);
  return api.get<EventList>(`/events?${qs}`);
};

/* ---------- /v1/audit ---------- */

export interface AuditLogEntry {
  id: string;
  emitter_kind: string;
  emitter_id: string;
  tenant_id: string;
  site_id?: string;
  occurred_at: string;
  actor_kind: string;
  actor_id: string;
  action: string;
  outcome: 'success' | 'failure' | 'denied';
  resource_kind: string;
  resource_id: string;
  attributes?: Record<string, string>;
  source_ip?: string;
  client_fingerprint?: string;
  correlation_id?: string;
  prev_hash: string;
  hash: string;
  seq: number;
}

export interface AuditList extends ListEnvelope<AuditLogEntry> {
  notice?: string;
}

export const fetchAudit = (page = 0, perPage = 100) =>
  api.get<AuditList>(`/audit?page=${page}&items_per_page=${perPage}`);

/* ---------- /v1/storage-volumes ---------- */

export type StorageVolumeStatus = 'online' | 'degraded' | 'full' | 'read_only' | 'offline';

export interface StorageVolumeSMART {
  device?: string;
  model_family?: string;
  model_name?: string;
  serial_number?: string;
  power_on_hours?: number;
  health_passed?: boolean;
  temperature_c?: number;
}

export interface StorageVolume {
  id: string;
  recording_server_id: string;
  mount_path: string;
  kind: string;
  capacity_bytes: number;
  used_bytes: number;
  reserved_bytes: number;
  status: StorageVolumeStatus;
  last_checked_at: string;
  priority: number;
  write_bytes_per_second?: number;
  smart?: StorageVolumeSMART;
}

export interface StorageVolumeList extends ListEnvelope<StorageVolume> {}

export const fetchStorageVolumes = () =>
  api.get<StorageVolumeList>('/storage-volumes?items_per_page=100');

/* ---------- /v1/recorder/config ---------- */
//
// The recorder's GlobalConf shape is large (~50 fields) and uses
// camelCase (MediaMTX-lineage) per the documented escape-hatch
// carve-out. We type only the fields the UI surfaces today and
// pass the rest through as `Record<string, unknown>` to preserve
// fidelity on PATCH round-trips.

export interface RecorderConfig {
  tenantId?: string;
  // Fields the UI reads — extend as more settings come online.
  api?: boolean;
  rtsp?: boolean;
  rtmp?: boolean;
  hls?: boolean;
  webrtc?: boolean;
  srt?: boolean;
  // Catch-all for unspecified fields.
  [key: string]: unknown;
}

export const fetchRecorderConfig = () => api.get<RecorderConfig>('/recorder/config');
export const patchRecorderConfig = (body: Partial<RecorderConfig>) =>
  api.patch<{ status: string }>('/recorder/config', body);

/* ---------- /v1/recorder/identity ---------- */

export interface RecorderIdentity {
  id: string;
  tenant_id: string;
  hostname: string;
  location: string;
  timezone: string;
  firmware_version: string;
  paired: boolean;
  public_key_fingerprint: string;
  pinned_root_fingerprints: string[];
}

export const fetchIdentity = () => api.get<RecorderIdentity>('/recorder/identity');
export const patchIdentity = (body: { location: string }) =>
  api.patch<{ status: string }>('/recorder/identity', body);

/* ---------- /v1/recorder/pair ---------- */

// Mirrors internal/pairing.State — the recorder-side pairing-flow
// state machine. Drives the WizPair / Pairing-route UI loop.
export type PairState =
  | 'idle'
  | 'in_progress'
  | 'approved'
  | 'rejected'
  | 'token_expired'
  | 'token_consumed_elsewhere'
  | 'failed'
  | 'already_paired';

export interface PairStatus {
  state: PairState;
  started_at?: string;
  updated_at: string;
  ms_url?: string;
  pairing_request_id?: string;
  detail?: string;
}

export interface PairStartRequest {
  ms_url: string;
  token: string;
  root_fingerprint?: string; // sha256 hex (with optional `sha256:` prefix); from QR
}

export interface PairStartResponse {
  state: PairState;
  pairing_request_id?: string;
  detail?: string;
}

export const startPairing = (body: PairStartRequest) =>
  api.post<PairStartResponse>('/recorder/pair', body);

export const fetchPairStatus = () => api.get<PairStatus>('/recorder/pair/status');

export const resetPairing = () => api.post<{ status: string }>('/recorder/pair/reset');

// unpairRecorder wipes the recorder's locally-stored DeviceIdentity
// (cert + chain + pinned roots) and returns it to unpaired state.
// The recorder's UUIDv7 + ECDSA keypair survive — per ADR 0002 D3
// those are stable for the life of the install. The MS still has a
// pairing record + RecordingServer entry until an MS operator
// cleans it up; the recorder ↔ MS WebSocket-driven unpair flow
// (pairing-flows.md §2.5) lands in a later slice.
export const unpairRecorder = () => api.post<{ status: string }>('/recorder/unpair');

/* ---------- /v1/recorder/discovered-management ---------- */

export interface DiscoveredManagement {
  ms_id?: string;
  hostname: string;
  addresses: string[];
  version?: string;
  tenant_id?: string;
  port: number;
  url: string;
  first_seen_at: string;
  last_seen_at: string;
}

export const fetchDiscoveredManagement = () =>
  api.get<{ items: DiscoveredManagement[] }>('/recorder/discovered-management');

/* ---------- /v1/recorder system actions ---------- */

export const rebootRecorder = () => api.post<{ status: string }>('/recorder/reboot');
// config-backup is a GET that returns a downloadable file. We hit
// it via a window.location-style redirect from the UI rather than
// fetch + JSON, so the browser's native download flow takes over.
export const configBackupURL = '/v1/recorder/config-backup';
export const restoreConfig = (body: unknown) =>
  api.post<{ status: string }>('/recorder/config-restore', body);

/* ---------- /v1/recorder/network-info ---------- */

export interface NetworkInterface {
  name: string;
  hardware_addr: string;
  mtu: number;
  is_up: boolean;
  is_loopback: boolean;
  addresses?: string[];
}

export interface BandwidthStats {
  now_rx_bps: number;
  now_tx_bps: number;
  peak_bps: number;
  avg_bps: number;
  window_sec: number;
}

export interface NetworkInfo {
  os: string;
  platform: string;
  hostname: string;
  interfaces: NetworkInterface[];
  dns?: string[];
  bandwidth: BandwidthStats;
}

export const fetchNetworkInfo = () => api.get<NetworkInfo>('/recorder/network-info');

/* ---------- /v1/cameras/probe ---------- */

export interface CameraProbeRequest {
  source_url: string;
}
export interface CameraProbeResponse {
  reachable: boolean;
  host: string;
  port: number;
  latency_ms: number;
  reason?: string;
}
export const probeCameraSource = (body: CameraProbeRequest) =>
  api.post<CameraProbeResponse>('/cameras/probe', body);

/* ---------- /v1/diagnostics/* ---------- */

export interface PingResponse {
  target: string;
  samples: { seq: number; ok: boolean; latency_ms: number; reason?: string }[];
  avg_ms: number;
  loss_pct: number;
}
export interface NtpResponse {
  server: string;
  ok: boolean;
  offset_ms: number;
  reason?: string;
}
export interface RTSPProbeResult {
  path: string;
  url: string;
  reachable: boolean;
  latency_ms: number;
  reason?: string;
}
export interface RTSPProbeResponse {
  results: RTSPProbeResult[];
  ok_count: number;
  total: number;
}
export const diagPing = (target: string, count = 4) =>
  api.post<PingResponse>('/diagnostics/ping', { target, count });
export const diagNTP = (server = 'pool.ntp.org') =>
  api.post<NtpResponse>('/diagnostics/ntp', { server });
export const diagRTSPProbe = () =>
  api.post<RTSPProbeResponse>('/diagnostics/rtsp-probe');

/* ---------- /v1/streams ---------- */
//
// We surface only the stream fields the Cameras list consumes for
// the resolution/fps/codec columns. Real Stream entities are
// richer (per ADR 0009 §D5 Streams) but we don't need it here.

export interface StreamTrack {
  kind: string;
  codec: string;
  resolution?: string; // e.g. "1920x1080" — present for video tracks
  fps?: number;        // present for video tracks
}

export interface Stream {
  id: string;
  camera_id?: string;
  protocol: string;
  state: string;
  tracks?: StreamTrack[];
}

export interface StreamList extends ListEnvelope<Stream> {}

export const fetchStreams = () =>
  api.get<StreamList>('/streams?items_per_page=200');
