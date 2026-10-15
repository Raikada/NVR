// Shared event-type presentation: class buckets, tones, icons.
import type { IconName } from '../components/Icon';
import type { EventRecord } from '../lib/api';

// Seeded event types: person vehicle package animal motion line_cross
// audio_alarm doorbell tamper io_in camera_online camera_offline bookmark.
export type EventClass =
  | 'PERSON' | 'VEHICLE' | 'PACKAGE' | 'ANIMAL' | 'MOTION'
  | 'DOORBELL' | 'BOOKMARK' | 'SYSTEM';

export function classOf(typeId: string): EventClass {
  if (typeId === 'person') return 'PERSON';
  if (typeId === 'vehicle') return 'VEHICLE';
  if (typeId === 'package') return 'PACKAGE';
  if (typeId === 'animal') return 'ANIMAL';
  if (typeId === 'doorbell') return 'DOORBELL';
  if (typeId === 'bookmark') return 'BOOKMARK';
  if (typeId === 'camera_online' || typeId === 'camera_offline') return 'SYSTEM';
  return 'MOTION'; // motion, line_cross, audio_alarm, tamper, io_in, …
}

export const CLASS_COLOR: Record<EventClass, string> = {
  PERSON: 'var(--accent)',
  VEHICLE: '#3B82F6',
  PACKAGE: 'var(--success)',
  ANIMAL: '#14B8A6',
  MOTION: '#737373',
  DOORBELL: 'var(--warning)',
  BOOKMARK: '#A855F7',
  SYSTEM: 'var(--danger)',
};

export const CLASS_ICON: Record<EventClass, IconName> = {
  PERSON: 'user',
  VEHICLE: 'car',
  PACKAGE: 'package',
  ANIMAL: 'activity',
  MOTION: 'activity',
  DOORBELL: 'bell',
  BOOKMARK: 'circle-dot',
  SYSTEM: 'wifi-off',
};

export function toneOf(e: EventRecord): string {
  if (e.type_id === 'camera_offline') return 'var(--danger)';
  if (e.type_id === 'camera_online') return 'var(--success)';
  if (e.type_id === 'tamper' || e.type_id === 'audio_alarm') return 'var(--warning)';
  return CLASS_COLOR[classOf(e.type_id)];
}

export function labelOf(e: EventRecord): string {
  return e.type_id.replace(/_/g, ' ').toUpperCase();
}

// payload note (bookmarks carry {"note": …}).
export function noteOf(e: EventRecord): string | null {
  if (e.payload && typeof e.payload === 'object') {
    const n = (e.payload as Record<string, unknown>)['note'];
    if (typeof n === 'string' && n) return n;
  }
  return null;
}
