// Console chrome — TopBar (status pills, host stats, clock, theme,
// user) and FooterStrip (service health, storage, shortcut legend).
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Icon } from '../components/Icon';
import { bareBtn } from '../components/ui';
import { fetchHealth, fetchStorageVolumes, getSystemInfo, type HealthStatus, type StorageVolume } from '../lib/api';
import { fmtTime } from '../lib/format';
import { useConsole } from '../lib/console';
import type { Theme } from './theme';

const mono10: CSSProperties = {
  fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)',
};

function Pill({ children, c }: { children: ReactNode; c: string }) {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5, padding: '2px 6px', borderRadius: 2,
      fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', textTransform: 'uppercase',
      background: `color-mix(in srgb, ${c} 7%, transparent)`,
      border: `1px solid color-mix(in srgb, ${c} 27%, transparent)`,
      color: c,
    }}>
      <span style={{ width: 4, height: 4, borderRadius: '50%', background: c, boxShadow: `0 0 4px ${c}` }} />
      {children}
    </span>
  );
}

// Shared poll of /v1/health + /v1/storage-volumes for the chrome.
export function useChromeStats() {
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [volumes, setVolumes] = useState<StorageVolume[]>([]);
  useEffect(() => {
    const load = () => {
      void fetchHealth().then(setHealth).catch(() => {});
      void fetchStorageVolumes().then((r) => setVolumes(r.items ?? [])).catch(() => {});
    };
    load();
    const t = window.setInterval(load, 15_000);
    return () => window.clearInterval(t);
  }, []);
  return { health, volumes };
}

export function ConsoleTopBar({ username, theme, setTheme, health, onOpenHealth, onOpenUsers, onLogout }: {
  username: string;
  theme: Theme;
  setTheme: (t: Theme) => void;
  health: HealthStatus | null;
  onOpenHealth: () => void;
  onOpenUsers: () => void;
  onLogout: () => void;
}) {
  const data = useConsole();
  const [version, setVersion] = useState('');
  const [hostname, setHostname] = useState('NVR');
  useEffect(() => {
    void getSystemInfo()
      .then((i) => {
        setVersion(i.version);
        setHostname(`NVR-${i.recorder_id.slice(0, 4).toUpperCase()}`);
      })
      .catch(() => {});
  }, []);

  const online = data.cameras.filter((c) => c.status === 'online').length;
  const offline = data.cameras.filter((c) => c.status === 'offline').length;
  const degraded = data.cameras.filter((c) => c.status === 'degraded').length;
  const off = -new Date().getTimezoneOffset() / 60;
  const tz = `UTC${off >= 0 ? '+' : ''}${off}`;
  const initials = username.slice(0, 2).toUpperCase();

  return (
    <header style={{
      height: 36, padding: '0 12px 0 18px', borderBottom: '1px solid var(--border)',
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      background: 'var(--bg-secondary)', flexShrink: 0,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
        <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', letterSpacing: '.5px' }}>RAIKADA</span>
        <span style={mono10}>{hostname}{version ? ` · ${version}` : ''}</span>
        <Pill c="var(--success)">{online} ONLINE</Pill>
        {degraded > 0 && <Pill c="var(--warning)">{degraded} DEGRADED</Pill>}
        {offline > 0 && <Pill c="var(--danger)">{offline} OFFLINE</Pill>}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
        {health && (
          <button onClick={onOpenHealth} style={{ ...bareBtn, display: 'flex', gap: 14 }}>
            <span style={mono10}>CPU {health.cpu_pct.toFixed(0)}%</span>
            <span style={mono10}>NET {(health.bandwidth.rx_bps / 1e6).toFixed(1)} Mbps</span>
          </button>
        )}
        <span style={{ width: 1, height: 14, background: 'var(--border)' }} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums', letterSpacing: '.5px' }}>
          {fmtTime(data.now)}
        </span>
        <span style={mono10}>{tz}</span>
        <span style={{ width: 1, height: 14, background: 'var(--border)' }} />
        <button
          onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}
          title={theme === 'light' ? 'Switch to dark mode' : 'Switch to light mode'}
          style={{ ...bareBtn, display: 'flex', color: 'var(--text-secondary)' }}
        >
          <Icon name={theme === 'light' ? 'moon' : 'sun'} size={13} />
        </button>
        <button onClick={onOpenUsers} style={{ ...bareBtn, display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{
            width: 22, height: 22, borderRadius: '50%',
            background: 'linear-gradient(135deg, var(--accent), color-mix(in srgb, var(--accent) 55%, #000))',
            display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
            fontFamily: 'var(--font-mono)', fontSize: 9, fontWeight: 600, color: 'var(--on-accent)',
          }}>{initials}</span>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 11, color: 'var(--text-primary)' }}>{username}</span>
        </button>
        <button onClick={onLogout} title="Sign out" style={{ ...bareBtn, display: 'flex', color: 'var(--text-secondary)' }}>
          <Icon name="x" size={13} />
        </button>
      </div>
    </header>
  );
}

export function FooterStrip({ health, volumes, onOpenHealth, onOpenStorage }: {
  health: HealthStatus | null;
  volumes: StorageVolume[];
  onOpenHealth: () => void;
  onOpenStorage: () => void;
}) {
  const data = useConsole();
  const dotColor = health
    ? health.overall === 'healthy' ? 'var(--success)' : health.overall === 'degraded' ? 'var(--warning)' : 'var(--danger)'
    : 'var(--text-muted)';
  const cap = volumes.reduce((a, v) => a + v.capacity_bytes, 0);
  const used = volumes.reduce((a, v) => a + v.used_bytes, 0);
  const pct = cap > 0 ? Math.round((used / cap) * 100) : 0;
  const writeBps = volumes.reduce((a, v) => a + (v.write_bytes_per_second ?? 0), 0);
  const daysLeft = writeBps > 0 ? Math.floor((cap - used) / (writeBps * 86400)) : null;
  const last24h = data.events.filter((e) => Date.now() - new Date(e.occurred_at).getTime() < 86_400_000).length;

  return (
    <footer style={{
      height: 24, padding: '0 14px', borderTop: '1px solid var(--border)',
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      background: 'var(--bg-secondary)', flexShrink: 0,
      fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: '.5px',
    }}>
      <div style={{ display: 'flex', gap: 18 }}>
        <button onClick={onOpenHealth} style={{ ...bareBtn, color: 'inherit', font: 'inherit', letterSpacing: 'inherit', display: 'flex', gap: 18 }}>
          <span><span style={{ color: dotColor }}>● </span>recorder</span>
          <span><span style={{ color: dotColor }}>● </span>media</span>
          <span><span style={{ color: dotColor }}>● </span>events</span>
        </button>
        {cap > 0 && (
          <button onClick={onOpenStorage} style={{ ...bareBtn, color: 'inherit', font: 'inherit', letterSpacing: 'inherit' }}>
            STORAGE {pct}% · {(used / 1e12).toFixed(2)} / {(cap / 1e12).toFixed(1)} TB
            {daysLeft != null ? ` · ${daysLeft} DAYS LEFT` : ''}
          </button>
        )}
      </div>
      <div style={{ display: 'flex', gap: 18 }}>
        <span>/ SEARCH · S ALERTS · C CAMERAS · 1 2 3 NAV · ESC BACK</span>
        <span style={{ color: 'var(--accent)' }}>{last24h} EVENTS · 24H</span>
      </div>
    </footer>
  );
}
