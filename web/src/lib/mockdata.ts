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
