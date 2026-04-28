import { Icon } from './Icon';
import type { IconName } from './Icon';
import type { Route } from '../lib/types';

interface IconRailProps {
  route: Route;
  go: (next: Route) => void;
}

const ITEMS: { id: Route; icon: IconName; label: string }[] = [
  { id: 'overview', icon: 'layout-dashboard', label: 'Overview' },
  { id: 'cameras', icon: 'cctv', label: 'Cameras' },
  { id: 'policies', icon: 'database', label: 'Policies' },
  { id: 'pairing', icon: 'link', label: 'Pairing' },
  { id: 'storage', icon: 'hard-drive', label: 'Storage' },
  { id: 'network', icon: 'network', label: 'Network' },
  { id: 'logs', icon: 'terminal', label: 'Logs' },
  { id: 'diagnostics', icon: 'activity', label: 'Diagnostics' },
];

interface RailBtnProps {
  icon: IconName;
  label: string;
  active: boolean;
  onClick: () => void;
  badge?: number;
}

function RailBtn({ icon, label, active, onClick, badge }: RailBtnProps) {
  return (
    <button
      title={label}
      onClick={onClick}
      style={{
        width: 42,
        height: 42,
        position: 'relative',
        background: active ? 'rgba(249,115,22,0.13)' : 'transparent',
        border: active ? '1px solid rgba(249,115,22,0.27)' : '1px solid transparent',
        borderRadius: 4,
        cursor: 'pointer',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        color: active ? '#F97316' : 'var(--text-secondary)',
        transition: 'all 150ms var(--ease-out)',
      }}
    >
      {active && (
        <div
          style={{
            position: 'absolute',
            left: -10,
            top: 8,
            bottom: 8,
            width: 3,
            background: '#F97316',
            boxShadow: '0 0 6px rgba(249,115,22,0.6)',
          }}
        />
      )}
      <Icon name={icon} style={{ width: 18, height: 18, strokeWidth: 2 }} />
      {badge && badge > 0 && (
        <span
          style={{
            position: 'absolute',
            top: 4,
            right: 4,
            minWidth: 14,
            height: 14,
            padding: '0 3px',
            background: '#EF4444',
            color: '#0A0A0A',
            fontFamily: 'var(--font-mono)',
            fontSize: 9,
            fontWeight: 600,
            borderRadius: 7,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          {badge}
        </span>
      )}
    </button>
  );
}

export function IconRail({ route, go }: IconRailProps) {
  return (
    <aside
      style={{
        width: 60,
        background: 'var(--bg-secondary)',
        borderRight: '1px solid var(--border)',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        padding: '12px 0',
        flexShrink: 0,
      }}
    >
      <nav
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 4,
          flex: 1,
          width: '100%',
          alignItems: 'center',
        }}
      >
        {ITEMS.map((it) => (
          <RailBtn
            key={it.id}
            icon={it.icon}
            label={it.label}
            active={route === it.id}
            onClick={() => go(it.id)}
          />
        ))}
      </nav>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, paddingBottom: 4 }}>
        <RailBtn
          icon="settings"
          label="Settings"
          active={route === 'settings'}
          onClick={() => go('settings')}
        />
      </div>
    </aside>
  );
}
