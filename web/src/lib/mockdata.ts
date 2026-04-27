// Mock data sources used while the SPA runs against the design's
// faux state. Replaced piece-by-piece with real /v1/ calls in a
// later swing — see the data-layer follow-up note in App.tsx.

import type { UICamera } from './types';

export const INITIAL_CAMERAS: UICamera[] = [
  { id: 'CAM-01', name: 'Front Door East', ip: '10.0.1.41', status: 'online', resolution: '1920×1080', fps: 30, codec: 'H.264' },
  { id: 'CAM-02', name: 'Driveway',         ip: '10.0.1.42', status: 'online', resolution: '1920×1080', fps: 30, codec: 'H.264' },
  { id: 'CAM-03', name: 'Backyard',         ip: '10.0.1.43', status: 'online', resolution: '1920×1080', fps: 25, codec: 'H.264' },
  { id: 'CAM-04', name: 'Side Gate',        ip: '10.0.1.44', status: 'offline', resolution: '1920×1080', fps: 30, codec: 'H.264' },
  { id: 'CAM-05', name: 'Garage Interior',  ip: '10.0.1.45', status: 'degraded', resolution: '1920×1080', fps: 30, codec: 'H.264' },
  { id: 'CAM-06', name: 'Front Walk',       ip: '10.0.1.46', status: 'online', resolution: '2688×1520', fps: 20, codec: 'H.265' },
];

export interface MockEvent {
  id: string;
  time: string;
  level: 'info' | 'warn' | 'error' | 'debug';
  msg: string;
}

export function seedEvents(): MockEvent[] {
  return [
    { id: 'e1', time: '14:32:07', level: 'info', msg: 'CAM-02 motion detected — zone "driveway"' },
    { id: 'e2', time: '14:31:44', level: 'info', msg: 'Recording segment rotated — CAM-01' },
    { id: 'e3', time: '14:30:12', level: 'warn', msg: 'CAM-05 packet loss 2.4% — investigating' },
    { id: 'e4', time: '14:28:09', level: 'info', msg: 'Rules sync completed — 7 rules applied' },
    { id: 'e5', time: '14:25:33', level: 'info', msg: 'CAM-02 motion ended after 14s' },
    { id: 'e6', time: '14:22:01', level: 'debug', msg: 'RTSP keepalive CAM-03' },
  ];
}

/* ---------- Logs (live-tail mock) ---------- */

export type LogLevel = 'error' | 'warn' | 'info' | 'debug';
export type LogSource = 'SYSTEM' | 'CAMERA' | 'RECORDER' | 'NETWORK';

export interface MockLog {
  id: string;
  time: string;
  level: LogLevel;
  source: LogSource;
  msg: string;
  fields: Record<string, string | number>;
}

let logCounter = 1000;

function genLog(ts: number): MockLog {
  const templates: { l: LogLevel; s: LogSource; m: string; f: Record<string, string | number> }[] = [
    { l: 'info', s: 'CAMERA', m: 'CAM-0{n} RTSP handshake complete', f: { transport: 'tcp', rtt_ms: 14, resolution: '1920x1080' } },
    { l: 'info', s: 'RECORDER', m: 'Segment rotated — CAM-0{n} — 4.2 MB', f: { segment: 'seg-{r}.mp4', duration_s: 60, codec: 'h264' } },
    { l: 'warn', s: 'CAMERA', m: 'CAM-0{n} packet loss 2.4%', f: { rtp_loss_pct: 2.4, consecutive_drops: 7 } },
    { l: 'info', s: 'SYSTEM', m: 'Rules sync from ms-prod-01.local', f: { rules_applied: 7, took_ms: 142 } },
    { l: 'debug', s: 'NETWORK', m: 'ARP refresh for 10.0.1.0/24', f: { responded: 18 } },
    { l: 'error', s: 'CAMERA', m: 'CAM-0{n} authentication failed', f: { http_status: 401, scheme: 'digest' } },
    { l: 'info', s: 'RECORDER', m: 'AI detection — person — CAM-0{n}', f: { model: 'yolov8n', confidence: 0.87, bbox: '[412,301,618,792]' } },
    { l: 'info', s: 'SYSTEM', m: 'NTP step +3 ms', f: { server: 'pool.ntp.org' } },
  ];
  const t = templates[Math.floor(Math.random() * templates.length)];
  const n = Math.floor(Math.random() * 6) + 1;
  const d = new Date(ts);
  const fields: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(t.f)) {
    fields[k] = typeof v === 'string'
      ? v.replace('{r}', Math.floor(Math.random() * 9999).toString().padStart(4, '0'))
      : v;
  }
  return {
    id: 'log' + ++logCounter,
    time: d.toLocaleTimeString('en-GB', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0'),
    level: t.l,
    source: t.s,
    msg: t.m.replace('{n}', String(n)),
    fields,
  };
}

export function seedLogs(n: number): MockLog[] {
  const arr: MockLog[] = [];
  const now = Date.now();
  for (let i = n; i > 0; i--) arr.push(genLog(now - i * 3500));
  return arr;
}

export function nextLog(): MockLog {
  return genLog(Date.now());
}

let eventCounter = 100;

export function nextEvent(): MockEvent {
  const msgs: { l: MockEvent['level']; m: string }[] = [
    { l: 'info', m: 'CAM-0{n} motion detected — zone "entry"' },
    { l: 'info', m: 'Recording segment rotated — CAM-0{n}' },
    { l: 'debug', m: 'RTSP keepalive CAM-0{n}' },
    { l: 'info', m: 'AI detection — person — CAM-0{n}' },
    { l: 'warn', m: 'CAM-0{n} reconnect after 2s dropout' },
    { l: 'info', m: 'Storage segment closed — 4.2 MB' },
  ];
  const c = msgs[Math.floor(Math.random() * msgs.length)];
  const n = Math.floor(Math.random() * 6) + 1;
  const now = new Date();
  const time = now.toLocaleTimeString('en-GB', { hour12: false });
  return { id: 'ev' + ++eventCounter, time, level: c.l, msg: c.m.replace('{n}', String(n)) };
}
