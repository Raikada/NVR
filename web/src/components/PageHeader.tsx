import type { ReactNode } from 'react';

interface PageHeaderProps {
  title: string;
  sub?: ReactNode;
  right?: ReactNode;
  breadcrumb?: ReactNode;
}

export function PageHeader({ title, sub, right, breadcrumb }: PageHeaderProps) {
  return (
    <header
      style={{
        padding: '18px 24px',
        borderBottom: '1px solid var(--border)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 16,
        flexShrink: 0,
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
        {breadcrumb && (
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              letterSpacing: 2,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            {breadcrumb}
          </span>
        )}
        <h1
          style={{
            margin: 0,
            fontFamily: 'var(--font-sans)',
            fontSize: 18,
            fontWeight: 600,
            color: 'var(--text-primary)',
            letterSpacing: '-0.01em',
          }}
        >
          {title}
        </h1>
        {sub && (
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)' }}>
            {sub}
          </span>
        )}
      </div>
      {right && <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>{right}</div>}
    </header>
  );
}
