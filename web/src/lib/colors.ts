// Convert a hex color string ("#RRGGBB" or "RRGGBB") to a comma-
// separated rgb triple suitable for use inside `rgba(${...},alpha)`.
// Ported from the design's hex2rgb helper. Used heavily by the
// Toast and StatusBadge components for tinted-but-tone-correct
// backgrounds and borders.
export function hex2rgb(h: string): string {
  const x = h.replace('#', '');
  return [
    parseInt(x.slice(0, 2), 16),
    parseInt(x.slice(2, 4), 16),
    parseInt(x.slice(4, 6), 16),
  ].join(',');
}

// Brand and status color constants. Mirror the CSS custom properties
// in tokens.css; duplicated here so TS code can pass them inline
// (e.g. into a style={{ ... }} prop) without going through getComputedStyle.
export const COLORS = {
  accent: '#F97316',
  accentHover: '#EA580C',
  success: '#22C55E',
  danger: '#EF4444',
  warning: '#EAB308',
  textPrimary: '#E5E5E5',
  textSecondary: '#737373',
  textMuted: '#404040',
} as const;

// Clamp a number into [a, b]. Used by the live-data simulators for
// CPU / bandwidth random walks.
export function clamp(v: number, a: number, b: number): number {
  return Math.max(a, Math.min(b, v));
}
