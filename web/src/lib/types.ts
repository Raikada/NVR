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
}

/* ---------- Management server pairing ---------- */

export interface ManagementServer {
  host: string;
  ip: string;
  mac: string;
  cameras: number;
  ver: string;
  trust?: 'SIGNED' | 'SELF';
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
  paired: boolean;
  managementServer: ManagementServer | null;
  cameras: UICamera[];
}

/* ---------- Routes ---------- */

export const ROUTES = [
  'overview',
  'cameras',
  'pairing',
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
