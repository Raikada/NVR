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
