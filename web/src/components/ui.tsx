// Operator-console UI kit — primitives ported from the Claude Design
// prototype (Tactical HUD system), wired to design tokens. These are
// the building blocks every console screen shares.
import { useRef, type CSSProperties, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react';
import { Icon } from './Icon';

/* ---------- Buttons ---------- */

export type BtnKind = 'primary' | 'secondary' | 'danger' | 'tactical' | 'ghost';

const BTN_KINDS: Record<BtnKind, CSSProperties> = {
  primary: { background: 'var(--accent)', color: 'var(--on-accent)' },
  secondary: { background: 'var(--bg-tertiary)', color: 'var(--text-primary)', borderColor: 'var(--border)' },
  danger: { background: 'var(--danger-13)', color: 'var(--danger)', borderColor: 'var(--danger-27)' },
  tactical: {
    background: 'var(--bg-tertiary)', color: 'var(--accent)', borderColor: 'var(--accent-27)',
    fontFamily: 'var(--font-mono)', textTransform: 'uppercase', letterSpacing: 1, fontSize: 11, fontWeight: 500,
  },
  ghost: { background: 'transparent', color: 'var(--text-secondary)', borderColor: 'var(--border)' },
};

export function Btn({
  kind = 'primary', children, onClick, style, title, disabled,
}: {
  kind?: BtnKind; children?: ReactNode; onClick?: (e: ReactMouseEvent) => void;
  style?: CSSProperties; title?: string; disabled?: boolean;
}) {
  return (
    <button
      title={title}
      disabled={disabled}
      onClick={onClick}
      style={{
        fontFamily: 'var(--font-sans)', fontWeight: 600, fontSize: 12,
        height: 32, padding: '0 14px', borderRadius: 4, border: '1px solid transparent',
        cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 6,
        transition: 'background 150ms var(--ease-out)',
        ...BTN_KINDS[kind],
        ...(disabled ? { opacity: 0.4, cursor: 'default' } : null),
        ...style,
      }}
    >
      {children}
    </button>
  );
}

/* ---------- Status badge ---------- */

const BADGE_TONES = {
  live: { c: 'var(--success)', l: 'LIVE' },
  rec: { c: 'var(--danger)', l: 'REC' },
  motion: { c: 'var(--accent)', l: 'MOTION' },
  online: { c: 'var(--success)', l: 'ONLINE' },
  offline: { c: '#737373', l: 'OFFLINE' },
  degraded: { c: 'var(--warning)', l: 'DEGRADED' },
} as const;

export type BadgeKind = keyof typeof BADGE_TONES;

export function StatusBadge({ kind = 'live', label }: { kind?: BadgeKind; label?: string }) {
  const t = BADGE_TONES[kind];
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 6,
      padding: '3px 7px', borderRadius: 3,
      fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '.5px', textTransform: 'uppercase',
      background: `color-mix(in srgb, ${t.c} 7%, transparent)`,
      border: `1px solid color-mix(in srgb, ${t.c} 27%, transparent)`,
      color: t.c,
    }}>
      <span style={{ width: 5, height: 5, borderRadius: '50%', background: t.c, boxShadow: `0 0 6px ${t.c}` }} />
      {label || t.l}
    </span>
  );
}

/* ---------- Toggle ---------- */

export function Toggle({ on, onChange, label }: { on: boolean; onChange?: (v: boolean) => void; label?: boolean }) {
  return (
    <button onClick={() => onChange && onChange(!on)} style={{
      background: 'transparent', border: 'none', cursor: 'pointer',
      display: 'inline-flex', flexDirection: 'column', alignItems: 'center', gap: 4, padding: 0,
    }}>
      <div style={{
        width: 42, height: 22, borderRadius: 999, background: 'var(--bg-tertiary)',
        border: on ? '2px solid var(--accent)' : '2px solid var(--border)',
        boxShadow: on ? 'var(--glow-accent-md)' : 'none',
        position: 'relative', transition: 'all 150ms var(--ease-out)',
      }}>
        <div style={{
          position: 'absolute', top: '50%', transform: 'translateY(-50%)',
          width: 12, height: 12, borderRadius: '50%',
          background: on ? 'var(--accent)' : 'var(--text-muted)',
          left: on ? 24 : 4,
          transition: 'left 150ms var(--ease-out)',
        }} />
      </div>
      {label && (
        <span style={{
          fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1,
          color: on ? 'var(--accent)' : 'var(--text-muted)', textTransform: 'uppercase',
        }}>{on ? 'ON' : 'OFF'}</span>
      )}
    </button>
  );
}

/* ---------- Segmented ---------- */

export type SegOption = string | { value: string; label: string };

export function Segmented({ options, value, onChange }: {
  options: SegOption[]; value: string; onChange?: (v: string) => void;
}) {
  return (
    <div style={{
      display: 'inline-flex', border: '1px solid var(--border)', borderRadius: 4,
      overflow: 'hidden', background: 'var(--bg-primary)',
    }}>
      {options.map((opt, i) => {
        const v = typeof opt === 'string' ? opt : opt.value;
        const lbl = typeof opt === 'string' ? opt : opt.label;
        const active = v === value;
        return (
          <button key={v} onClick={() => onChange && onChange(v)} style={{
            fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 1,
            padding: '6px 12px', background: active ? 'var(--accent-13)' : 'transparent',
            border: 'none', color: active ? 'var(--accent)' : 'var(--text-muted)',
            cursor: 'pointer',
            borderRight: i < options.length - 1 ? '1px solid var(--border)' : 'none',
            textTransform: 'uppercase', whiteSpace: 'nowrap',
          }}>{lbl}</button>
        );
      })}
    </div>
  );
}

/* ---------- Slider ---------- */

export function Slider({ label, value, onChange, min = 0, max = 100, suffix = '%', ticks = 11 }: {
  label?: string; value: number; onChange?: (v: number) => void;
  min?: number; max?: number; suffix?: string; ticks?: number;
}) {
  const pct = ((value - min) / (max - min)) * 100;
  const trackRef = useRef<HTMLDivElement>(null);
  const onDown = (e: ReactMouseEvent) => {
    const move = (ev: globalThis.MouseEvent | ReactMouseEvent) => {
      const el = trackRef.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      const p = Math.max(0, Math.min(1, (ev.clientX - r.left) / r.width));
      onChange && onChange(Math.round(min + p * (max - min)));
    };
    const up = () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up); };
    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', up);
    move(e);
  };
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)', textTransform: 'uppercase' }}>{label}</span>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--accent)' }}>{value}{suffix}</span>
      </div>
      <div ref={trackRef} onMouseDown={onDown} style={{
        position: 'relative', height: 5, background: 'var(--bg-tertiary)',
        border: '1px solid var(--border)', borderRadius: 3, cursor: 'pointer',
      }}>
        <div style={{
          position: 'absolute', inset: 0, width: `${pct}%`,
          background: 'linear-gradient(90deg, var(--accent), var(--accent-40))', borderRadius: 3,
        }} />
        <div style={{
          position: 'absolute', left: `${pct}%`, top: '50%', transform: 'translate(-50%,-50%)',
          width: 16, height: 16, borderRadius: '50%', background: 'var(--bg-tertiary)',
          border: '2px solid var(--accent)', boxShadow: 'var(--glow-accent-sm)',
        }} />
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between' }}>
        {Array.from({ length: ticks }).map((_, i) => (
          <div key={i} style={{ width: 1, height: 4, background: 'var(--border)' }} />
        ))}
      </div>
    </div>
  );
}

/* ---------- Section header (mono caps) ---------- */

export function SectionHeader({ children, right }: { children: ReactNode; right?: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
      <span style={{
        fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 2, fontWeight: 500,
        color: 'var(--text-secondary)', textTransform: 'uppercase',
      }}>{children}</span>
      {right}
    </div>
  );
}

/* ---------- Panel card ---------- */

export function Panel({ title, children, right }: { title: ReactNode; children?: ReactNode; right?: ReactNode }) {
  return (
    <div style={{ background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, overflow: 'hidden' }}>
      <div style={{ padding: '10px 14px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', minHeight: 40 }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 2, fontWeight: 500, color: 'var(--text-secondary)', textTransform: 'uppercase' }}>{title}</span>
        {right}
      </div>
      <div>{children}</div>
    </div>
  );
}

/* ---------- Corner bracket (video tile framing) ---------- */

export function Bracket({ pos }: { pos: 'tl' | 'tr' | 'bl' | 'br' }) {
  const sz = 16, t = 2;
  const map: Record<string, CSSProperties> = {
    tl: { top: 6, left: 6, borderWidth: `${t}px 0 0 ${t}px` },
    tr: { top: 6, right: 6, borderWidth: `${t}px ${t}px 0 0` },
    bl: { bottom: 6, left: 6, borderWidth: `0 0 ${t}px ${t}px` },
    br: { bottom: 6, right: 6, borderWidth: `0 ${t}px ${t}px 0` },
  };
  return (
    <span style={{
      position: 'absolute', width: sz, height: sz, opacity: 0.4,
      borderColor: 'var(--accent)', borderStyle: 'solid', pointerEvents: 'none',
      ...map[pos],
    }} />
  );
}

export function Brackets() {
  return (<><Bracket pos="tl" /><Bracket pos="tr" /><Bracket pos="bl" /><Bracket pos="br" /></>);
}

/* ---------- Forms ---------- */

export function FormRow({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '120px 1fr', alignItems: 'center', gap: 12 }}>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{label}</span>
      <div>{children}</div>
    </div>
  );
}

export function TxtInput({ value, onChange, placeholder, type = 'text', disabled, autoFocus, onKeyDown }: {
  value?: string; onChange?: (v: string) => void; placeholder?: string; type?: string; disabled?: boolean;
  autoFocus?: boolean; onKeyDown?: (e: React.KeyboardEvent<HTMLInputElement>) => void;
}) {
  return (
    <input
      value={value ?? ''}
      type={type}
      disabled={disabled}
      autoFocus={autoFocus}
      onKeyDown={onKeyDown}
      onChange={(e) => onChange && onChange(e.target.value)}
      placeholder={placeholder}
      style={{
        width: '100%', height: 32, padding: '0 10px',
        background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4,
        fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)',
        outline: 'none', opacity: disabled ? 0.5 : 1,
      }}
    />
  );
}

export function KV({ pairs }: { pairs: Array<[string, ReactNode]> }) {
  return (
    <div style={{ marginTop: 10, display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 12, rowGap: 6 }}>
      {pairs.map(([k, v]) => (
        <span key={k} style={{ display: 'contents' }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{k}</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums', overflow: 'hidden', textOverflow: 'ellipsis' }}>{v}</span>
        </span>
      ))}
    </div>
  );
}

/* ---------- Modal scaffold ---------- */

export function Modal({ title, width = 520, onClose, children, footer }: {
  title: ReactNode; width?: number; onClose: () => void; children: ReactNode; footer?: ReactNode;
}) {
  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'var(--scrim)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width, maxWidth: '94vw', maxHeight: '88vh', background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>{title}</span>
          <button onClick={onClose} style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}>
            <Icon name="x" size={16} />
          </button>
        </div>
        <div style={{ padding: 16, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 14 }}>{children}</div>
        {footer && <div style={{ padding: 14, borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between', gap: 8 }}>{footer}</div>}
      </div>
    </div>
  );
}

/* ---------- Role badge ---------- */

export function RoleBadge({ role }: { role: string }) {
  const r = role.toUpperCase();
  const tone = ({
    OWNER: ['var(--accent)', 'var(--accent-13)', 'var(--accent-27)'],
    ADMIN: ['#3B82F6', 'rgba(59,130,246,0.13)', 'rgba(59,130,246,0.27)'],
    OPERATOR: ['var(--success)', 'var(--success-7)', 'var(--success-27)'],
    VIEWER: ['var(--text-secondary)', 'var(--bg-tertiary)', 'var(--border)'],
    AUDIT: ['#A855F7', 'rgba(168,85,247,0.13)', 'rgba(168,85,247,0.27)'],
  } as Record<string, string[]>)[r] || ['var(--text-secondary)', 'var(--bg-tertiary)', 'var(--border)'];
  return (
    <span style={{
      padding: '2px 8px', background: tone[1], border: `1px solid ${tone[2]}`,
      color: tone[0], fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, width: 'fit-content',
    }}>{r}</span>
  );
}

/* ---------- Shared style constants ---------- */

export const bareBtn: CSSProperties = { background: 'none', border: 'none', cursor: 'pointer', padding: 0 };

export const iconBtn: CSSProperties = {
  width: 30, height: 30, background: 'var(--bg-tertiary)', border: '1px solid var(--border)',
  borderRadius: 4, color: 'var(--text-secondary)', cursor: 'pointer',
  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
};

export const chip: CSSProperties = {
  padding: '3px 7px', background: 'var(--accent-13)', border: '1px solid var(--accent-27)',
  color: 'var(--accent)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '.5px',
  borderRadius: 3, textTransform: 'uppercase',
};

export const pageTitle: CSSProperties = {
  fontFamily: 'var(--font-sans)', fontSize: 16, fontWeight: 600, color: 'var(--text-primary)',
};

export const monoLabel: CSSProperties = {
  fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1,
  color: 'var(--text-muted)', textTransform: 'uppercase',
};

export const screenHdr: CSSProperties = {
  height: 52, padding: '0 20px', borderBottom: '1px solid var(--border)',
  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
  background: 'var(--bg-primary)', flexShrink: 0,
};

export const linkBtn: CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--accent)',
  textTransform: 'uppercase', letterSpacing: '.5px', padding: 0,
};
