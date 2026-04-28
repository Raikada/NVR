// Duration helpers for the canonical /v1/ wire shape.
//
// Go's time.Duration JSON-marshals to int64 nanoseconds. The recorder's
// canonical RecordingPolicy fields (retention_duration, part_duration,
// pre_event_buffer, etc.) all ride this format. The UI surfaces them
// in human-friendly units (days for retention, seconds for buffers /
// part durations); these helpers do the round-trip without a Date /
// Duration library.

const NS_PER_SECOND = 1_000_000_000;
const NS_PER_MINUTE = 60 * NS_PER_SECOND;
const NS_PER_HOUR = 60 * NS_PER_MINUTE;
const NS_PER_DAY = 24 * NS_PER_HOUR;

export function nsToDays(ns: number): number {
  if (!Number.isFinite(ns) || ns <= 0) return 0;
  return ns / NS_PER_DAY;
}

export function daysToNs(days: number): number {
  return Math.round(days * NS_PER_DAY);
}

export function nsToSeconds(ns: number): number {
  if (!Number.isFinite(ns) || ns <= 0) return 0;
  return ns / NS_PER_SECOND;
}

export function secondsToNs(seconds: number): number {
  return Math.round(seconds * NS_PER_SECOND);
}

export function nsToMinutes(ns: number): number {
  if (!Number.isFinite(ns) || ns <= 0) return 0;
  return ns / NS_PER_MINUTE;
}

export function minutesToNs(minutes: number): number {
  return Math.round(minutes * NS_PER_MINUTE);
}

// Render a duration in the most appropriate unit for a compact label.
// "14d" / "10m" / "5s" — used in policy summary cards and the policies
// list table.
export function formatDuration(ns: number): string {
  if (!Number.isFinite(ns) || ns <= 0) return '—';
  if (ns >= NS_PER_DAY) {
    const d = ns / NS_PER_DAY;
    return d % 1 === 0 ? `${d}d` : `${d.toFixed(1)}d`;
  }
  if (ns >= NS_PER_HOUR) {
    const h = ns / NS_PER_HOUR;
    return h % 1 === 0 ? `${h}h` : `${h.toFixed(1)}h`;
  }
  if (ns >= NS_PER_MINUTE) {
    const m = ns / NS_PER_MINUTE;
    return m % 1 === 0 ? `${m}m` : `${m.toFixed(1)}m`;
  }
  const s = ns / NS_PER_SECOND;
  return s % 1 === 0 ? `${s}s` : `${s.toFixed(1)}s`;
}

// Render bytes as KB / MB / GB. Used in the Advanced disclosure to
// label the max_part_size slider.
export function formatPartSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '—';
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(0)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}
