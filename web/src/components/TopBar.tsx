import { StatusBadge } from './primitives';
import type { AppState } from '../lib/types';

interface TopBarProps {
  server: AppState & { recordingCount: number; cameraCount: number };
  now: Date;
  /** Pre-pairing auth slice 2026-05-06: when set, renders a Sign Out
   *  button that clears the recorder-local JWT and routes back to
   *  the Login screen. */
  username?: string | null;
  onLogout?: () => void;
}

export function TopBar({ server, now, username, onLogout }: TopBarProps) {
  const time = now.toLocaleTimeString('en-GB', { hour12: false });
  return (
    <header
      style={{
        height: 48,
        flexShrink: 0,
        background: 'var(--bg-secondary)',
        borderBottom: '1px solid var(--border)',
        display: 'flex',
        alignItems: 'center',
        padding: '0 16px 0 12px',
        gap: 16,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <img src="assets/logo.png" alt="Raikada" style={{ width: 22, height: 22 }} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              letterSpacing: 2,
              color: '#F97316',
              textTransform: 'uppercase',
            }}
          >
            RAIKADA
          </span>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            RECORDING SERVER
          </span>
        </div>
      </div>
      <div style={{ width: 1, height: 24, background: 'var(--border)' }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            HOSTNAME
          </span>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
              color: 'var(--text-primary)',
              letterSpacing: 0.5,
            }}
          >
            {server.hostname}
          </span>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            SERIAL
          </span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)' }}>
            {server.serial}
          </span>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            FW
          </span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)' }}>
            {server.firmware}
          </span>
        </div>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <StatusBadge kind="recording" label={`REC ${server.recordingCount}/${server.cameraCount}`} />
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-end',
            gap: 2,
            paddingLeft: 10,
            borderLeft: '1px solid var(--border)',
          }}
        >
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
            }}
          >
            UTC
          </span>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
              color: 'var(--text-primary)',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {time}
          </span>
        </div>
        {username && onLogout && (
          <button
            type="button"
            onClick={onLogout}
            title={`Signed in as ${username} — click to sign out`}
            style={{
              marginLeft: 8,
              paddingLeft: 10,
              paddingRight: 8,
              borderLeft: '1px solid var(--border)',
              background: 'transparent',
              border: 'none',
              color: 'var(--text-secondary)',
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              letterSpacing: 1,
              textTransform: 'uppercase',
              cursor: 'pointer',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-end',
              gap: 2,
            }}
          >
            <span style={{ color: 'var(--text-muted)' }}>{username}</span>
            <span>SIGN OUT</span>
          </button>
        )}
      </div>
    </header>
  );
}
