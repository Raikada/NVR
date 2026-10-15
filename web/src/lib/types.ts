// Shared foundation entity types for the operator console.

/* ---------- Foundation entities ---------- */

export type Role = 'admin' | 'owner' | 'operator' | 'viewer' | 'audit';

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
