// Raikada shared primitives.
//
// Ported from the design's primitives.jsx. Inline-style approach kept
// verbatim — matches the design's visual output exactly and the
// CSS custom properties in tokens.css carry the actual theming.
// Switching to styled-components / Tailwind would have meant
// re-deriving every value; the inline-style approach lets the
// CSS-variable system handle theming without an indirection layer.

import { useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';
import { Icon } from './Icon';
import type { IconName } from './Icon';
import { hex2rgb } from '../lib/colors';

/* ---------- Button ---------- */

export type BtnKind = 'primary' | 'secondary' | 'danger' | 'tactical' | 'ghost' | 'ghostQuiet';
export type BtnSize = 'sm' | 'md' | 'lg';

interface BtnProps {
  kind?: BtnKind;
  children?: ReactNode;
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;
  style?: CSSProperties;
  disabled?: boolean;
  type?: 'button' | 'submit';
  size?: BtnSize;
  icon?: IconName;
  // Native title attribute — used as a tooltip / hover hint, e.g. for
  // explaining why a disabled button is disabled. Slice 4-B uses it on
  // the Cameras page lockdown affordances.
  title?: string;
}

export function Btn({
  kind = 'primary',
  children,
  onClick,
  style,
  disabled,
  type = 'button',
  size = 'md',
  icon,
  title,
}: BtnProps) {
  const base: CSSProperties = {
    fontFamily: 'var(--font-sans)',
    fontWeight: 600,
    fontSize: 12,
    height: size === 'sm' ? 26 : size === 'lg' ? 38 : 32,
    padding: size === 'sm' ? '0 10px' : '0 14px',
    borderRadius: 4,
    border: '1px solid transparent',
    cursor: disabled ? 'not-allowed' : 'pointer',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    opacity: disabled ? 0.45 : 1,
    transition: 'background 150ms var(--ease-out), border-color 150ms var(--ease-out)',
    whiteSpace: 'nowrap',
  };
  const kinds: Record<BtnKind, CSSProperties> = {
    primary: { background: '#F97316', color: '#0A0A0A' },
    secondary: {
      background: 'var(--bg-tertiary)',
      color: 'var(--text-primary)',
      borderColor: 'var(--border)',
    },
    danger: {
      background: 'rgba(239,68,68,0.13)',
      color: '#EF4444',
      borderColor: 'rgba(239,68,68,0.27)',
    },
    tactical: {
      background: 'var(--bg-tertiary)',
      color: '#F97316',
      borderColor: 'rgba(249,115,22,0.27)',
      fontFamily: 'var(--font-mono)',
      textTransform: 'uppercase',
      letterSpacing: 1,
      fontSize: 11,
      fontWeight: 500,
    },
    ghost: {
      background: 'transparent',
      color: 'var(--text-secondary)',
      borderColor: 'var(--border)',
    },
    ghostQuiet: {
      background: 'transparent',
      color: 'var(--text-secondary)',
      borderColor: 'transparent',
    },
  };
  return (
    <button
      type={type}
      disabled={disabled}
      title={title}
      style={{ ...base, ...kinds[kind], ...style }}
      onClick={onClick}
    >
      {icon && <Icon name={icon} style={{ width: 14, height: 14 }} />}
      {children}
    </button>
  );
}

/* ---------- Status badge ---------- */

export type StatusBadgeKind =
  | 'online' | 'offline' | 'degraded'
  | 'recording' | 'rec' | 'live'
  | 'paired' | 'unpaired'
  | 'error' | 'warn' | 'info' | 'debug'
  | 'pending' | 'idle';

interface StatusBadgeProps {
  kind?: StatusBadgeKind;
  label?: string;
  size?: 'sm' | 'md';
}

const TONES: Record<StatusBadgeKind, { c: string; l: string }> = {
  online: { c: '#22C55E', l: 'ONLINE' },
  offline: { c: '#737373', l: 'OFFLINE' },
  degraded: { c: '#EAB308', l: 'DEGRADED' },
  recording: { c: '#EF4444', l: 'RECORDING' },
  rec: { c: '#EF4444', l: 'REC' },
  live: { c: '#22C55E', l: 'LIVE' },
  paired: { c: '#22C55E', l: 'PAIRED' },
  unpaired: { c: '#EAB308', l: 'UNPAIRED' },
  error: { c: '#EF4444', l: 'ERROR' },
  warn: { c: '#EAB308', l: 'WARN' },
  info: { c: '#F97316', l: 'INFO' },
  debug: { c: '#737373', l: 'DEBUG' },
  pending: { c: '#F97316', l: 'PENDING' },
  idle: { c: '#737373', l: 'IDLE' },
};

export function StatusBadge({ kind = 'online', label, size = 'md' }: StatusBadgeProps) {
  const t = TONES[kind] ?? TONES.online;
  const text = label || t.l;
  const small = size === 'sm';
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: small ? '2px 6px' : '3px 7px',
        borderRadius: 3,
        fontFamily: 'var(--font-mono)',
        fontSize: small ? 9 : 10,
        letterSpacing: '.5px',
        textTransform: 'uppercase',
        background: `rgba(${hex2rgb(t.c)},0.07)`,
        border: `1px solid rgba(${hex2rgb(t.c)},0.27)`,
        color: t.c,
        whiteSpace: 'nowrap',
      }}
    >
      <span
        style={{
          width: 5,
          height: 5,
          borderRadius: '50%',
          background: t.c,
          boxShadow: `0 0 6px ${t.c}`,
        }}
      />
      {text}
    </span>
  );
}

/* ---------- Toggle ---------- */

interface ToggleProps {
  on: boolean;
  onChange?: (next: boolean) => void;
  label?: string;
}

export function Toggle({ on, onChange, label }: ToggleProps) {
  return (
    <button
      onClick={() => onChange?.(!on)}
      style={{
        background: 'transparent',
        border: 'none',
        cursor: 'pointer',
        display: 'inline-flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 4,
        padding: 0,
      }}
    >
      <div
        style={{
          width: 42,
          height: 22,
          borderRadius: 999,
          background: 'var(--bg-tertiary)',
          border: on ? '2px solid #F97316' : '2px solid var(--border)',
          boxShadow: on ? '0 0 12px rgba(249,115,22,0.55)' : 'none',
          position: 'relative',
          transition: 'all 150ms var(--ease-out)',
        }}
      >
        <div
          style={{
            position: 'absolute',
            top: '50%',
            transform: 'translateY(-50%)',
            width: 12,
            height: 12,
            borderRadius: '50%',
            background: on ? '#F97316' : '#404040',
            boxShadow: on ? '0 0 8px rgba(249,115,22,0.7)' : 'none',
            left: on ? 24 : 4,
            transition: 'left 150ms var(--ease-out)',
          }}
        />
      </div>
      {label !== undefined && (
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            letterSpacing: 1,
            color: on ? '#F97316' : 'var(--text-muted)',
            textTransform: 'uppercase',
          }}
        >
          {label || (on ? 'ON' : 'OFF')}
        </span>
      )}
    </button>
  );
}

/* ---------- Segmented control ---------- */

export type SegmentedOption<T extends string> = T | { value: T; label: string };

interface SegmentedProps<T extends string> {
  options: SegmentedOption<T>[];
  value: T;
  onChange?: (next: T) => void;
  size?: 'sm' | 'md';
}

export function Segmented<T extends string>({
  options,
  value,
  onChange,
  size = 'md',
}: SegmentedProps<T>) {
  const pad = size === 'sm' ? '4px 10px' : '6px 12px';
  return (
    <div
      style={{
        display: 'inline-flex',
        border: '1px solid var(--border)',
        borderRadius: 4,
        overflow: 'hidden',
        background: 'var(--bg-primary)',
      }}
    >
      {options.map((opt, i) => {
        const v = typeof opt === 'string' ? opt : opt.value;
        const lbl = typeof opt === 'string' ? opt : opt.label;
        const active = v === value;
        return (
          <button
            key={v}
            onClick={() => onChange?.(v)}
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: size === 'sm' ? 10 : 11,
              letterSpacing: 1,
              padding: pad,
              background: active ? 'rgba(249,115,22,0.13)' : 'transparent',
              border: 'none',
              color: active ? '#F97316' : 'var(--text-muted)',
              cursor: 'pointer',
              borderRight: i < options.length - 1 ? '1px solid var(--border)' : 'none',
              textTransform: 'uppercase',
            }}
          >
            {lbl}
          </button>
        );
      })}
    </div>
  );
}

/* ---------- Section header ---------- */

interface SectionHeaderProps {
  children: ReactNode;
  right?: ReactNode;
  style?: CSSProperties;
}

export function SectionHeader({ children, right, style }: SectionHeaderProps) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
        marginBottom: 10,
        ...style,
      }}
    >
      <span
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 11,
          letterSpacing: 2,
          fontWeight: 500,
          color: 'var(--text-secondary)',
          textTransform: 'uppercase',
        }}
      >
        {children}
      </span>
      {right}
    </div>
  );
}

/* ---------- Card ---------- */

interface CardProps {
  children: ReactNode;
  style?: CSSProperties;
  accent?: string;
  padding?: number;
}

export function Card({ children, style, accent, padding = 16 }: CardProps) {
  return (
    <div
      style={{
        background: 'var(--bg-secondary)',
        border: accent ? `1px solid ${accent}` : '1px solid var(--border)',
        borderRadius: 6,
        padding,
        position: 'relative',
        ...style,
      }}
    >
      {children}
    </div>
  );
}

/* ---------- Corner brackets ---------- */

type BracketPos = 'tl' | 'tr' | 'bl' | 'br';

export function Bracket({ pos }: { pos: BracketPos }) {
  const size = 12;
  const t = 2;
  const common: CSSProperties = {
    position: 'absolute',
    width: size,
    height: size,
    opacity: 0.4,
    borderColor: '#F97316',
    borderStyle: 'solid',
    pointerEvents: 'none',
  };
  const map: Record<BracketPos, CSSProperties> = {
    tl: { top: 0, left: 0, borderTopWidth: t, borderLeftWidth: t, borderRightWidth: 0, borderBottomWidth: 0 },
    tr: { top: 0, right: 0, borderTopWidth: t, borderRightWidth: t, borderLeftWidth: 0, borderBottomWidth: 0 },
    bl: { bottom: 0, left: 0, borderBottomWidth: t, borderLeftWidth: t, borderRightWidth: 0, borderTopWidth: 0 },
    br: { bottom: 0, right: 0, borderBottomWidth: t, borderRightWidth: t, borderLeftWidth: 0, borderTopWidth: 0 },
  };
  return <div style={{ ...common, ...map[pos] }} />;
}

/* Convenience: render all four brackets at once. */
export function Brackets() {
  return (
    <>
      <Bracket pos="tl" />
      <Bracket pos="tr" />
      <Bracket pos="bl" />
      <Bracket pos="br" />
    </>
  );
}

/* ---------- Input ---------- */

interface InputProps {
  label?: string;
  value: string;
  onChange?: (next: string) => void;
  placeholder?: string;
  type?: 'text' | 'password' | 'number';
  error?: string;
  suffix?: string;
  mono?: boolean;
  style?: CSSProperties;
  autoFocus?: boolean;
  disabled?: boolean;
  width?: number | string;
}

export function Input({
  label,
  value,
  onChange,
  placeholder,
  type = 'text',
  error,
  suffix,
  mono,
  style,
  autoFocus,
  disabled,
  width,
}: InputProps) {
  const [focus, setFocus] = useState(false);
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, width: width ?? '100%', ...style }}>
      {label && (
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            letterSpacing: 1,
            color: error ? '#EF4444' : 'var(--text-secondary)',
            textTransform: 'uppercase',
          }}
        >
          {label}
          {error && <span style={{ marginLeft: 8, textTransform: 'none' }}>— {error}</span>}
        </span>
      )}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          background: 'var(--bg-tertiary)',
          border: `1px solid ${
            error
              ? 'rgba(239,68,68,0.45)'
              : focus
                ? 'rgba(249,115,22,0.45)'
                : 'var(--border)'
          }`,
          borderRadius: 4,
          padding: '0 10px',
          height: 36,
          transition: 'border-color 150ms var(--ease-out)',
          opacity: disabled ? 0.5 : 1,
        }}
      >
        <input
          autoFocus={autoFocus}
          disabled={disabled}
          type={type}
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
          onFocus={() => setFocus(true)}
          onBlur={() => setFocus(false)}
          placeholder={placeholder}
          style={{
            flex: 1,
            background: 'transparent',
            border: 'none',
            outline: 'none',
            color: 'var(--text-primary)',
            fontFamily: mono ? 'var(--font-mono)' : 'var(--font-sans)',
            fontSize: 13,
            fontVariantNumeric: mono ? 'tabular-nums' : 'normal',
          }}
        />
        {suffix && (
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            {suffix}
          </span>
        )}
      </div>
    </label>
  );
}

/* ---------- Stat tile ---------- */

export type Tone = 'accent' | 'success' | 'danger' | 'warning' | 'muted';

interface StatProps {
  label: string;
  value: string | number;
  unit?: string;
  tone?: Tone;
  sub?: string;
  icon?: IconName;
}

const TONE_COLORS: Record<Tone, string> = {
  accent: '#F97316',
  success: '#22C55E',
  danger: '#EF4444',
  warning: '#EAB308',
  muted: '#E5E5E5',
};

export function Stat({ label, value, unit, tone = 'accent', sub, icon }: StatProps) {
  return (
    <div
      style={{
        padding: 14,
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        minHeight: 82,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            letterSpacing: 1,
            color: 'var(--text-muted)',
            textTransform: 'uppercase',
          }}
        >
          {label}
        </span>
        {icon && <Icon name={icon} style={{ width: 14, height: 14, color: 'var(--text-muted)' }} />}
      </div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 4 }}>
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 22,
            color: TONE_COLORS[tone],
            fontVariantNumeric: 'tabular-nums',
            lineHeight: 1,
          }}
        >
          {value}
        </span>
        {unit && (
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              color: 'var(--text-secondary)',
              letterSpacing: 1,
            }}
          >
            {unit}
          </span>
        )}
      </div>
      {sub && (
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            color: 'var(--text-muted)',
            textTransform: 'uppercase',
            letterSpacing: 0.5,
          }}
        >
          {sub}
        </span>
      )}
    </div>
  );
}

/* ---------- Knob (read-only radial gauge) ---------- */

interface KnobProps {
  value: number;
  max?: number;
  label?: string;
  unit?: string;
  size?: number;
  tone?: 'accent' | 'success' | 'danger' | 'warning';
}

export function Knob({
  value,
  max = 100,
  label,
  unit = '%',
  size = 86,
  tone = 'accent',
}: KnobProps) {
  const c = TONE_COLORS[tone];
  const pct = Math.max(0, Math.min(1, value / max));
  const START = 135; // degrees from top (CSS 0° is up after rotate(-90deg))
  const SWEEP = 270;
  const angle = START + pct * SWEEP;
  const radius = size / 2 - 6;
  const center = size / 2;
  const circ = 2 * Math.PI * radius;
  const dash = (SWEEP / 360) * circ;
  const fill = pct * dash;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
      <div style={{ width: size, height: size, position: 'relative' }}>
        <svg width={size} height={size} style={{ transform: 'rotate(135deg)', transformOrigin: 'center' }}>
          <circle
            cx={center}
            cy={center}
            r={radius}
            fill="none"
            stroke="var(--border)"
            strokeWidth="3"
            strokeDasharray={`${dash} ${circ}`}
            strokeLinecap="round"
          />
          <circle
            cx={center}
            cy={center}
            r={radius}
            fill="none"
            stroke={c}
            strokeWidth="3"
            strokeDasharray={`${fill} ${circ}`}
            strokeLinecap="round"
            style={{ filter: `drop-shadow(0 0 4px ${c}aa)`, transition: 'stroke-dasharray 300ms var(--ease-out)' }}
          />
        </svg>
        {/* Inner knob body */}
        <div
          style={{
            position: 'absolute',
            inset: 12,
            borderRadius: '50%',
            background: 'radial-gradient(circle at 35% 25%, #222 0%, #0d0d0d 70%)',
            border: '1px solid var(--border)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexDirection: 'column',
          }}
        >
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 16,
              color: c,
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {typeof value === 'number' ? value.toFixed(value < 10 ? 1 : 0) : value}
          </span>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              color: 'var(--text-muted)',
              letterSpacing: 1,
            }}
          >
            {unit}
          </span>
        </div>
        {/* Indicator line */}
        <div
          style={{
            position: 'absolute',
            top: center - 1,
            left: center,
            height: 2,
            background: c,
            boxShadow: `0 0 6px ${c}`,
            transformOrigin: '0 50%',
            transform: `rotate(${angle}deg) translateX(${radius - 14}px)`,
            width: 8,
            transition: 'transform 300ms var(--ease-out)',
          }}
        />
      </div>
      {label && (
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            letterSpacing: 1,
            color: 'var(--text-secondary)',
            textTransform: 'uppercase',
          }}
        >
          {label}
        </span>
      )}
    </div>
  );
}

/* ---------- Progress bar ---------- */

interface ProgressProps {
  value: number;
  max?: number;
  tone?: 'accent' | 'success' | 'danger' | 'warning';
  height?: number;
  segments?: { weight: number }[];
}

export function Progress({ value, max = 100, tone = 'accent', height = 6, segments }: ProgressProps) {
  const c = TONE_COLORS[tone];
  const pct = Math.max(0, Math.min(1, value / max)) * 100;
  return (
    <div
      style={{
        height,
        background: 'var(--bg-tertiary)',
        border: '1px solid var(--border)',
        borderRadius: 3,
        overflow: 'hidden',
        position: 'relative',
      }}
    >
      <div
        style={{
          height: '100%',
          width: pct + '%',
          background: `linear-gradient(90deg, ${c}, ${c}aa)`,
          transition: 'width 300ms var(--ease-out)',
          boxShadow: `0 0 8px ${c}55`,
        }}
      />
      {segments && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex' }}>
          {segments.map((s, i) => (
            <div
              key={i}
              style={{
                flex: s.weight,
                borderRight: i < segments.length - 1 ? '1px solid rgba(0,0,0,0.6)' : 'none',
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

/* ---------- KV (label / value pair, used in many drawers/cards) ---------- */

interface KVProps {
  k: string;
  v: ReactNode;
}

export function KV({ k, v }: KVProps) {
  return (
    <div>
      <div
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 9,
          color: 'var(--text-muted)',
          letterSpacing: 1,
          textTransform: 'uppercase',
        }}
      >
        {k}
      </div>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', marginTop: 2 }}>
        {v}
      </div>
    </div>
  );
}

/* ---------- MiniBlock (k/v in a tinted tile, used in headers/grids) ---------- */

interface MiniBlockProps {
  k: string;
  v: ReactNode;
}

export function MiniBlock({ k, v }: MiniBlockProps) {
  return (
    <div style={{ padding: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4 }}>
      <div
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 9,
          letterSpacing: 1,
          color: 'var(--text-muted)',
          textTransform: 'uppercase',
        }}
      >
        {k}
      </div>
      <div
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 12,
          color: '#F97316',
          marginTop: 4,
          letterSpacing: 0.5,
        }}
      >
        {v}
      </div>
    </div>
  );
}

/* ---------- SliderField (range input) ---------- */

interface SliderFieldProps {
  label: string;
  value: number;
  min?: number;
  max?: number;
  onChange: (next: number) => void;
}

export function SliderField({ label, value, min = 0, max = 100, onChange }: SliderFieldProps) {
  return (
    <div>
      <div
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 10,
          color: 'var(--text-muted)',
          letterSpacing: 1,
          textTransform: 'uppercase',
          marginBottom: 6,
        }}
      >
        {label}
      </div>
      <input
        type="range"
        min={min}
        max={max}
        value={value}
        onChange={(e) => onChange(+e.target.value)}
        style={{ width: '100%', accentColor: '#F97316' }}
      />
    </div>
  );
}
