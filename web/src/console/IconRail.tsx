// IconRail — 60px nav rail. Video ops (top), management (mid),
// alerts/health/settings (bottom). Ported from the design prototype.
import { Icon, type IconName } from '../components/Icon';
import type { ConsoleRoute } from './routes';

const TOP: Array<{ id: ConsoleRoute; icon: IconName; label: string }> = [
  { id: 'live', icon: 'grid-2x2', label: 'Live View' },
  { id: 'playback', icon: 'play-circle', label: 'Playback' },
  { id: 'search', icon: 'search', label: 'Event Search' },
];
const MID: Array<{ id: ConsoleRoute; icon: IconName; label: string }> = [
  { id: 'devices', icon: 'cctv', label: 'Devices' },
  { id: 'storage', icon: 'hard-drive', label: 'Storage & Retention' },
  { id: 'users', icon: 'users', label: 'Users' },
];

export function IconRail({ active, onNavigate, onAlerts, alertsCount }: {
  active: ConsoleRoute;
  onNavigate: (r: ConsoleRoute) => void;
  onAlerts: () => void;
  alertsCount: number;
}) {
  return (
    <aside style={{
      width: 60, background: 'var(--bg-secondary)', borderRight: '1px solid var(--border)',
      display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '14px 0',
      flexShrink: 0,
    }}>
      <div title="Raikada NVR" style={{ width: 36, height: 36, display: 'flex', alignItems: 'center', justifyContent: 'center', marginBottom: 18 }}>
        <img src="assets/logo.png" alt="Raikada" style={{ width: 44, height: 44 }} />
      </div>

      <nav style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, width: '100%', alignItems: 'center' }}>
        {TOP.map((it) => (
          <RailButton key={it.id} icon={it.icon} label={it.label} active={active === it.id} onClick={() => onNavigate(it.id)} />
        ))}
        <div style={{ width: 28, height: 1, background: 'var(--border)', margin: '12px 0' }} />
        {MID.map((it) => (
          <RailButton key={it.id} icon={it.icon} label={it.label} active={active === it.id} onClick={() => onNavigate(it.id)} />
        ))}
      </nav>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, paddingBottom: 6 }}>
        <RailButton icon="bell" label="Alerts" badge={alertsCount} active={active === 'alerts'} onClick={onAlerts} />
        <RailButton icon="activity" label="System Health" active={active === 'health'} onClick={() => onNavigate('health')} />
        <RailButton icon="settings" label="Settings" active={active === 'settings'} onClick={() => onNavigate('settings')} />
      </div>
    </aside>
  );
}

function RailButton({ icon, label, active, onClick, badge }: {
  icon: IconName; label: string; active?: boolean; onClick: () => void; badge?: number;
}) {
  return (
    <button title={label} onClick={onClick} style={{
      width: 42, height: 42, position: 'relative',
      background: active ? 'var(--accent-13)' : 'transparent',
      border: active ? '1px solid var(--accent-27)' : '1px solid transparent',
      borderRadius: 4, cursor: 'pointer',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      color: active ? 'var(--accent)' : 'var(--text-secondary)',
      transition: 'all 150ms var(--ease-out)',
    }}>
      {active && (
        <div style={{
          position: 'absolute', left: -10, top: 8, bottom: 8, width: 3, background: 'var(--accent)',
          boxShadow: 'var(--glow-accent-sm)',
        }} />
      )}
      <Icon name={icon} size={18} strokeWidth={2} />
      {badge != null && badge > 0 && (
        <span style={{
          position: 'absolute', top: 4, right: 4,
          minWidth: 14, height: 14, padding: '0 3px',
          background: 'var(--danger)', color: 'var(--on-accent)',
          fontFamily: 'var(--font-mono)', fontSize: 9, fontWeight: 600,
          borderRadius: 7, display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>{badge > 99 ? '99+' : badge}</span>
      )}
    </button>
  );
}
