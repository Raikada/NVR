// Shared time/number formatting for the operator console.

export function pad(n: number, w = 2): string {
  return String(n).padStart(w, '0');
}

export function fmtTime(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export function fmtDate(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function fmtAgo(secs: number): string {
  if (secs < 0) secs = 0;
  if (secs < 60) return `${Math.floor(secs)}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

// fmtDelta renders a minutes-ago offset as a compact "1D 4H" / "2H 05M"
// / "3M 20S" label for the playback transport.
export function fmtDelta(m: number): string {
  if (m >= 1440) return `${Math.floor(m / 1440)}D ${Math.floor((m % 1440) / 60)}H`;
  if (m >= 60) return `${Math.floor(m / 60)}H ${pad(Math.floor(m % 60))}M`;
  return `${Math.floor(m)}M ${pad(Math.floor((m % 1) * 60))}S`;
}

export function fmtBytes(n: number): string {
  if (n >= 1e12) return `${(n / 1e12).toFixed(2)} TB`;
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)} GB`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} MB`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)} KB`;
  return `${n} B`;
}

export function fmtBitrate(bps: number): string {
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} Mbps`;
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(0)} kbps`;
  return `${bps.toFixed(0)} bps`;
}
