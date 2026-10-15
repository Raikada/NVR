// System Health — real container stats (/v1/health), per-stream table
// (camera stats deltas), network info, recording volume, recent system
// activity (audit + lifecycle events).
import { useEffect, useRef, useState } from 'react';
import { Icon } from '../components/Icon';
import { Btn, Panel, StatusBadge, pageTitle, screenHdr } from '../components/ui';
import {
  exportRecorderRecoveryBundle, fetchHealth, fetchNetworkInfo, fetchStorageVolumes,
  getCameraStats, getSystemInfo, listAudit,
  type AuditLogEntry, type HealthStatus, type NetworkInfo, type StorageVolume,
} from '../lib/api';
import { fmtTime } from '../lib/format';
import { useConsole } from '../lib/console';

interface StreamRow {
  id: string;
  name: string;
  status: 'online' | 'degraded' | 'offline';
  bitrate: string;
  framesErr: number;
  readers: number;
}

export function SystemHealth() {
  const data = useConsole();
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [history, setHistory] = useState<{ cpu: number[]; ram: number[]; net: number[] }>({ cpu: [], ram: [], net: [] });
  const [net, setNet] = useState<NetworkInfo | null>(null);
  const [volumes, setVolumes] = useState<StorageVolume[]>([]);
  const [version, setVersion] = useState('');
  const [audit, setAudit] = useState<AuditLogEntry[]>([]);
  const [streams, setStreams] = useState<StreamRow[]>([]);
  const prevBytes = useRef<Record<string, { bytes: number; ts: number }>>({});
  const [bundleBusy, setBundleBusy] = useState(false);

  useEffect(() => {
    const load = () => {
      void fetchHealth().then((h) => {
        setHealth(h);
        setHistory((hs) => ({
          cpu: [...hs.cpu.slice(-39), h.cpu_pct],
          ram: [...hs.ram.slice(-39), h.mem_pct],
          net: [...hs.net.slice(-39), h.bandwidth.rx_bps / 1e6],
        }));
      }).catch(() => {});
    };
    load();
    const t = window.setInterval(load, 5000);
    return () => window.clearInterval(t);
  }, []);

  useEffect(() => {
    void fetchNetworkInfo().then(setNet).catch(() => {});
    void fetchStorageVolumes().then((r) => setVolumes(r.items ?? [])).catch(() => {});
    void getSystemInfo().then((i) => setVersion(i.version)).catch(() => {});
    void listAudit({ perPage: 12 }).then(setAudit).catch(() => {});
  }, []);

  // Per-camera stream stats: poll every 5s, derive bitrate from deltas.
  useEffect(() => {
    let dead = false;
    const poll = () => {
      void Promise.all(
        data.cameras.map(async (c) => {
          try {
            const s = await getCameraStats(c.id);
            const now = Date.now();
            const prev = prevBytes.current[c.id];
            let bitrate = '—';
            if (prev && now > prev.ts && s.bytes_received >= prev.bytes) {
              const bps = ((s.bytes_received - prev.bytes) * 8) / ((now - prev.ts) / 1000);
              bitrate = `${(bps / 1e6).toFixed(1)} Mbps`;
            }
            prevBytes.current[c.id] = { bytes: s.bytes_received, ts: now };
            return { id: c.id, name: c.name, status: c.status, bitrate: c.status === 'offline' ? '—' : bitrate, framesErr: s.frames_in_error, readers: s.readers } as StreamRow;
          } catch {
            return { id: c.id, name: c.name, status: c.status, bitrate: '—', framesErr: 0, readers: 0 } as StreamRow;
          }
        }),
      ).then((rows) => { if (!dead) setStreams(rows); });
    };
    poll();
    const t = window.setInterval(poll, 5000);
    return () => { dead = true; window.clearInterval(t); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.cameras.map((c) => c.id).join(',')]);

  const days = health ? Math.floor(health.uptime / 1e9 / 86400) : 0;
  const hours = health ? Math.floor(((health.uptime / 1e9) % 86400) / 3600) : 0;
  const overall = health?.overall ?? 'healthy';
  const cap = volumes.reduce((a, v) => a + v.capacity_bytes, 0);
  const used = volumes.reduce((a, v) => a + v.used_bytes, 0);

  const downloadBundle = () => {
    setBundleBusy(true);
    void exportRecorderRecoveryBundle({ purpose: 'recovery', include_audit: true })
      .then((b) => {
        const blob = new Blob([JSON.stringify(b, null, 2)], { type: 'application/json' });
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = `raikada-diagnostics-${Date.now()}.json`;
        a.click();
        URL.revokeObjectURL(a.href);
      })
      .catch(() => {})
      .finally(() => setBundleBusy(false));
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)', overflowY: 'auto' }}>
      <header style={screenHdr}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={pageTitle}>System Health</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}>raikada-nvr{version ? ` · ${version}` : ''} · docker</span>
          <StatusBadge kind={overall === 'healthy' ? 'online' : overall === 'degraded' ? 'degraded' : 'offline'} label={overall.toUpperCase()} />
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <Btn kind="ghost" onClick={downloadBundle} disabled={bundleBusy}>
            <Icon name="download" size={12} />{bundleBusy ? 'Building…' : 'Diagnostics bundle'}
          </Btn>
        </div>
      </header>

      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 20 }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12 }}>
          <KPICard label="CPU · CONTAINER" value={health ? `${health.cpu_pct.toFixed(0)}%` : '—'} series={history.cpu} max={100} thresh={75} />
          <KPICard label="MEMORY" value={health ? `${health.mem_pct.toFixed(0)}%` : '—'} series={history.ram} max={100} thresh={80} />
          <KPICard label="INGEST" value={health ? `${(health.bandwidth.rx_bps / 1e6).toFixed(1)} Mbps` : '—'} series={history.net} max={50} thresh={40} sub={`${data.cameras.filter((c) => c.status !== 'offline').length} STREAMS`} />
          <KPICard label="UPTIME" value={health ? `${days}D ${String(hours).padStart(2, '0')}H` : '—'} sub="CONTAINER" />
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 12 }}>
          <Panel title="STREAMS">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 100px 100px 90px', padding: '8px 14px', borderBottom: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>
              <span>CAMERA</span><span>BITRATE</span><span>FRAME ERRS</span><span>VIEWERS</span>
            </div>
            {streams.map((st) => {
              const dot = st.status === 'online' ? 'var(--success)' : st.status === 'degraded' ? 'var(--warning)' : 'var(--danger)';
              return (
                <div key={st.id} style={{ display: 'grid', gridTemplateColumns: '1fr 100px 100px 90px', padding: '9px 14px', borderBottom: '1px solid var(--border)', alignItems: 'center', fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)' }}>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 8, fontFamily: 'var(--font-sans)', fontSize: 12 }}>
                    <span style={{ width: 6, height: 6, borderRadius: '50%', background: dot, boxShadow: `0 0 6px ${dot}` }} />{st.name}
                  </span>
                  <span>{st.bitrate}</span>
                  <span style={{ color: st.framesErr > 0 ? 'var(--warning)' : 'var(--text-primary)' }}>{st.framesErr}</span>
                  <span>{st.readers}</span>
                </div>
              );
            })}
            {streams.length === 0 && <div style={{ padding: 20, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No cameras yet.</div>}
          </Panel>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Panel title="NETWORK">
              <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 8 }}>
                {net?.interfaces?.filter((i) => i.is_up && !i.is_loopback).slice(0, 3).map((i) => (
                  <KV2 key={i.name} k={i.name.toUpperCase()} v={(i.addresses?.[0] ?? '—').split('/')[0]} />
                ))}
                {health && <KV2 k="INGEST" v={`${(health.bandwidth.rx_bps / 1e6).toFixed(1)} Mbps`} />}
                {health && <KV2 k="EGRESS" v={`${(health.bandwidth.tx_bps / 1e6).toFixed(1)} Mbps`} />}
                {!net && !health && <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No network info.</span>}
              </div>
            </Panel>
            <Panel title="RECORDING VOLUME">
              <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>
                {volumes.map((v) => (
                  <Bar key={v.id} label={`${v.mount_path} · ${v.kind}`} value={v.capacity_bytes > 0 ? Math.round((v.used_bytes / v.capacity_bytes) * 100) : 0} max={100} unit="%" />
                ))}
                {cap > 0 && (
                  <div style={{ display: 'flex', justifyContent: 'space-between', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                    <span>{(used / 1e12).toFixed(2)} TB USED</span><span>{((cap - used) / 1e12).toFixed(2)} TB FREE</span>
                  </div>
                )}
                <div style={{ padding: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, display: 'flex', alignItems: 'center', gap: 10 }}>
                  <Icon name="external-link" size={13} color="var(--text-secondary)" />
                  <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
                    Drive health, RAID and temperatures are managed by your NAS.
                  </span>
                </div>
              </div>
            </Panel>
          </div>
        </div>

        <Panel title="RECENT ACTIVITY">
          <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
            {audit.map((a) => (
              <div key={a.id} style={{ padding: '6px 14px', borderBottom: '1px solid var(--border)', display: 'flex', gap: 10 }}>
                <span style={{ color: 'var(--text-muted)' }}>{fmtTime(new Date(a.occurred_at)).slice(0, 5)}</span>
                <span style={{ width: 60, color: a.outcome === 'success' ? 'var(--text-secondary)' : 'var(--warning)', textTransform: 'uppercase', fontSize: 9, letterSpacing: 1, alignSelf: 'center' }}>{a.outcome}</span>
                <span style={{ color: 'var(--accent)' }}>{a.action}</span>
                <span style={{ flex: 1, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.resource_kind}{a.resource_id ? ` · ${a.resource_id.slice(0, 24)}` : ''}</span>
              </div>
            ))}
            {audit.length === 0 && <div style={{ padding: 20, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No recent activity.</div>}
          </div>
        </Panel>
      </div>
    </div>
  );
}

function KPICard({ label, value, sub, series, max = 100, thresh = 80 }: {
  label: string; value: string; sub?: string; series?: number[]; max?: number; thresh?: number;
}) {
  return (
    <div style={{ background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, padding: 14, display: 'flex', flexDirection: 'column', gap: 8, minHeight: 100 }}>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{label}</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 28, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums', lineHeight: 1 }}>{value}</span>
      {sub && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{sub}</span>}
      {series && series.length > 1 && <Sparkline points={series} max={max} thresh={thresh} />}
    </div>
  );
}

function Sparkline({ points, max = 100, thresh = 80 }: { points: number[]; max?: number; thresh?: number }) {
  const w = 100, h = 28;
  const step = w / Math.max(1, points.length - 1);
  const path = points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${(i * step).toFixed(1)} ${(h - (Math.min(p, max) / max) * h).toFixed(1)}`).join(' ');
  return (
    <svg width="100%" height={h} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ marginTop: 4 }}>
      <line x1="0" x2={w} y1={h - (thresh / max) * h} y2={h - (thresh / max) * h} stroke="rgba(234,179,8,0.3)" strokeDasharray="2 3" />
      <path d={path} fill="none" stroke="var(--accent)" strokeWidth="1.5" />
    </svg>
  );
}

function KV2({ k, v }: { k: string; v: string }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10 }}>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{k}</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)' }}>{v}</span>
    </div>
  );
}

function Bar({ label, value, max, unit }: { label: string; value: number; max: number; unit: string }) {
  const pct = Math.min(100, (value / max) * 100);
  const tone = pct > 85 ? 'var(--danger)' : pct > 70 ? 'var(--warning)' : 'var(--accent)';
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{label}</span>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' }}>{value}{unit}</span>
      </div>
      <div style={{ height: 6, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, overflow: 'hidden' }}>
        <div style={{ width: `${pct}%`, height: '100%', background: tone }} />
      </div>
    </div>
  );
}
