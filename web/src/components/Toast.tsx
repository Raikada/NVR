import { Icon } from './Icon';
import type { IconName } from './Icon';
import type { Toast as ToastT } from '../lib/types';
import { hex2rgb } from '../lib/colors';

interface ToastProps {
  toasts: ToastT[];
  remove: (id: string) => void;
}

const TONE_COLOR: Record<string, string> = {
  success: '#22C55E',
  danger: '#EF4444',
  warning: '#EAB308',
  info: '#F97316',
};

export function ToastStack({ toasts, remove }: ToastProps) {
  return (
    <div
      style={{
        position: 'fixed',
        bottom: 16,
        right: 16,
        display: 'flex',
        flexDirection: 'column',
        gap: 8,
        zIndex: 9000,
      }}
    >
      {toasts.map((t) => {
        const toneColor = TONE_COLOR[t.kind] || '#F97316';
        return (
          <div
            key={t.id}
            style={{
              minWidth: 280,
              maxWidth: 380,
              background: 'var(--bg-secondary)',
              border: `1px solid rgba(${hex2rgb(toneColor)},0.27)`,
              borderLeft: `2px solid ${toneColor}`,
              borderRadius: 4,
              padding: '10px 12px',
              display: 'flex',
              gap: 10,
              alignItems: 'flex-start',
            }}
          >
            <Icon
              name={(t.icon as IconName) || 'info'}
              style={{ width: 16, height: 16, color: toneColor, flexShrink: 0, marginTop: 2 }}
            />
            <div style={{ flex: 1 }}>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 1,
                  color: toneColor,
                  textTransform: 'uppercase',
                }}
              >
                {t.title}
              </div>
              {t.body && (
                <div
                  style={{
                    fontFamily: 'var(--font-sans)',
                    fontSize: 12,
                    color: 'var(--text-secondary)',
                    marginTop: 2,
                    lineHeight: 1.4,
                  }}
                >
                  {t.body}
                </div>
              )}
            </div>
            <button
              onClick={() => remove(t.id)}
              style={{
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                color: 'var(--text-muted)',
                padding: 0,
              }}
            >
              <Icon name="x" style={{ width: 14, height: 14 }} />
            </button>
          </div>
        );
      })}
    </div>
  );
}
