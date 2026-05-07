// Shared types for the recorder configuration SPA.
//
// These mirror the design's mocked state shapes. They are NOT the
// canonical /v1/ wire shapes — the data layer that wires real /v1/
// calls (later commit) will translate from canonical Camera /
// HealthStatus / Stream / etc. into these UI types as needed.

import type { StatusBadgeKind } from '../components/primitives';

/* ---------- Camera (UI-side model) ---------- */

export type CameraStatus = 'online' | 'offline' | 'degraded';

export interface UICamera {
  id: string;
  name: string;
  ip: string;
  status: CameraStatus;
  resolution: string;
  fps: number;
  codec: string;
  /* Optional fields populated by Discover or the config drawer. */
  vendor?: string;
  model?: string;
  mac?: string;
  port?: string;
  path?: string;
  user?: string;
  /* Transient credential — only populated during the manual-add
     wizard's submit flow; the recorder redacts userinfo on read-back
     so it never round-trips here. */
  pass?: string;
  onvif?: boolean;
  transport?: string;
  /* Canonical RecordingPolicy linkage. Populated from
     /v1/cameras → Camera.recording_policy_id; surfaced in the
     drawer's Recording tab as the selected policy. */
  recording_policy_id?: string;
}

/* ---------- App-level state ---------- */

export interface AppState {
  hostname: string;
  serial: string;
  firmware: string;
  ip: string;
  gateway: string;
  cameraCount: number;
  recordingCount: number;
  cameras: UICamera[];
}

/* ---------- Routes ---------- */

export const ROUTES = [
  'overview',
  'cameras',
  'policies',
  'storage',
  'network',
  'logs',
  'diagnostics',
  'settings',
] as const;

export type Route = (typeof ROUTES)[number];

export function isRoute(s: string): s is Route {
  return (ROUTES as readonly string[]).includes(s);
}

/* ---------- Toasts ---------- */

export type ToastKind = 'success' | 'danger' | 'warning' | 'info';

export interface ToastInput {
  kind: ToastKind;
  title: string;
  body?: string;
  icon?: string;
}

export interface Toast extends ToastInput {
  id: string;
}

/* Helper to map design's status-badge "kind" string to a typed value. */
export type BadgeKind = StatusBadgeKind;

/* ---------- Foundation entities ---------- */

export type Role = 'admin' | 'viewer';

export interface User {
  id: string;
  username: string;
  display_name?: string;
  role: Role;
  email?: string;
  language?: string;
  is_active: boolean;
  must_change_password: boolean;
  last_login_at?: string;
  created_at: string;
  updated_at: string;
}

export interface CameraGroup {
  id: string;
  name: string;
  display_order: number;
}

export interface RecordingSchedule {
  id?: string;
  policy_id?: string;
  day_of_week: number; // 0=Sun..6=Sat
  start: string; // 'HH:MM'
  end: string;
}

export interface EventType {
  id: string;
  display_name: string;
  vendor?: string;
  description?: string;
}

export type NotificationTargetKind = 'webhook' | 'email';

export interface NotificationTarget {
  id: string;
  kind: NotificationTargetKind;
  name: string;
  webhook_url?: string;
  email_address?: string;
  enabled: boolean;
  created_at: string;
}

export type NotificationSeverity = 'info' | 'warning' | 'critical';

export interface NotificationSubscription {
  id: string;
  target_id: string;
  event_type_id?: string;
  camera_id?: string;
  min_severity?: NotificationSeverity;
  quiet_hours_start_minute?: number;
  quiet_hours_end_minute?: number;
}

export interface MeResponse {
  user: User;
  permissions: string[];
}
