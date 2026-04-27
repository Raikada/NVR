// Logs route — live-tail with level/source segmented filters, free-
// text search, expand-row structured fields, export. Faithful port
// of the design's LogsRoute.

import { Fragment, useEffect, useRef, useState } from 'react';
import { Btn, Segmented } from '../components/primitives';
import { Icon } from '../components/Icon';
import { PageHeader } from '../components/PageHeader';
import { seedLogs, nextLog } from '../lib/mockdata';
import type { LogLevel, MockLog } from '../lib/mockdata';
import type { ToastInput } from '../lib/types';

const LEVELS = ['ALL', 'ERROR', 'WARN', 'INFO', 'DEBUG'] as const;
const SOURCES = ['ALL', 'SYSTEM', 'CAMERA', 'RECORDER', 'NETWORK'] as const;

type LevelFilter = (typeof LEVELS)[number];
type SourceFilter = (typeof SOURCES)[number];

function levelColor(l: LogLevel): string {
  return { error: '#EF4444', warn: '#EAB308', info: '#F97316', debug: '#737373' }[l] || '#E5E5E5';
}

interface LogsProps {
  addToast: (t: ToastInput) => void;
}

export function Logs({ addToast }: LogsProps) {
  const [logs, setLogs] = useState<MockLog[]>(() => seedLogs(60));
  const [tailing, setTailing] = useState(true);
  const [query, setQuery] = useState('');
  const [level, setLevel] = useState<LevelFilter>('ALL');
  const [source, setSource] = useState<SourceFilter>('ALL');
  const [expanded, setExpanded] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!tailing) return;
    const i = window.setInterval(() => {
      setLogs((ls) => [...ls, nextLog()].slice(-400));
    }, 1100);
    return () => clearInterval(i);
  }, [tailing]);

  useEffect(() => {
    if (tailing && listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight;
  }, [logs, tailing]);

  const filtered = logs.filter(
    (l) =>
      (level === 'ALL' || (l.level.toUpperCase() as LevelFilter) === level) &&
      (source === 'ALL' || (l.source as SourceFilter) === source) &&
      (!query || (l.msg + ' ' + l.source).toLowerCase().includes(query.toLowerCase())),
  );

  return (
    <div
      style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        minWidth: 0,
        background: 'var(--bg-primary)',
        overflow: 'hidden',
      }}
    >
      <PageHeader
        breadcrumb="RECORDING SERVER / LOGS"
        title="Logs"
        sub={`${filtered.length} visible of ${logs.length} · ${tailing ? 'live tail' : 'paused'}`}
        right={
          <>
            <Btn
              kind="tactical"
              icon={tailing ? 'pause' : 'play'}
              onClick={() => setTailing(!tailing)}
            >
              {tailing ? 'PAUSE TAIL' : 'RESUME TAIL'}
            </Btn>
            <Btn
              kind="secondary"
              icon="download"
              onClick={() =>
                addToast({
                  kind: 'info',
                  title: 'EXPORT QUEUED',
                  body: `${filtered.length} lines · JSON`,
                  icon: 'download',
                })
              }
            >
              Export
            </Btn>
          </>
        }
      />
      <div
        style={{
          padding: '14px 20px',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          gap: 12,
          alignItems: 'center',
          background: 'var(--bg-secondary)',
          flexShrink: 0,
        }}
      >
        <div
          style={{
            flex: 1,
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            background: 'var(--bg-tertiary)',
            border: '1px solid var(--border)',
            borderRadius: 4,
            padding: '0 10px',
            height: 34,
          }}
        >
          <Icon name="search" style={{ width: 14, height: 14, color: 'var(--text-muted)' }} />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter: message, source, camera id…"
            style={{
              flex: 1,
              background: 'transparent',
              border: 'none',
              outline: 'none',
              color: 'var(--text-primary)',
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
            }}
          />
          {query && (
            <button
              onClick={() => setQuery('')}
              style={{
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                color: 'var(--text-muted)',
              }}
            >
              <Icon name="x" style={{ width: 12, height: 12 }} />
            </button>
          )}
        </div>
        <Segmented options={[...LEVELS]} value={level} onChange={setLevel} size="sm" />
        <Segmented options={[...SOURCES]} value={source} onChange={setSource} size="sm" />
      </div>
      <div
        ref={listRef}
        onScroll={(e) => {
          const el = e.currentTarget;
          if (el.scrollTop + el.clientHeight < el.scrollHeight - 20) setTailing(false);
        }}
        style={{
          flex: 1,
          overflow: 'auto',
          fontFamily: 'var(--font-mono)',
          fontSize: 12,
          padding: '6px 0',
        }}
      >
        {filtered.map((l) => (
          <div key={l.id}>
            <button
              onClick={() => setExpanded(expanded === l.id ? null : l.id)}
              style={{
                width: '100%',
                textAlign: 'left',
                background: 'transparent',
                border: 'none',
                cursor: 'pointer',
                display: 'grid',
                gridTemplateColumns: '12px 88px 56px 90px 1fr',
                gap: 12,
                padding: '4px 20px',
                alignItems: 'center',
                borderLeft: '2px solid transparent',
                transition: 'background 120ms var(--ease-out)',
              }}
              onMouseEnter={(e) => (e.currentTarget.style.background = 'rgba(249,115,22,0.04)')}
              onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
            >
              <Icon
                name={expanded === l.id ? 'chevron-down' : 'chevron-right'}
                style={{ width: 10, height: 10, color: 'var(--text-muted)' }}
              />
              <span style={{ color: 'var(--text-muted)', fontVariantNumeric: 'tabular-nums' }}>{l.time}</span>
              <span
                style={{
                  color: levelColor(l.level),
                  textTransform: 'uppercase',
                  letterSpacing: 1,
                  fontSize: 10,
                }}
              >
                {l.level}
              </span>
              <span
                style={{
                  color: 'var(--text-secondary)',
                  fontSize: 10,
                  letterSpacing: 1,
                  textTransform: 'uppercase',
                }}
              >
                {l.source}
              </span>
              <span
                style={{
                  color: 'var(--text-primary)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {l.msg}
              </span>
            </button>
            {expanded === l.id && (
              <div
                style={{
                  margin: '4px 20px 8px 42px',
                  padding: 10,
                  background: 'var(--bg-secondary)',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  display: 'grid',
                  gridTemplateColumns: '110px 1fr',
                  gap: '4px 12px',
                  fontSize: 11,
                }}
              >
                {Object.entries(l.fields).map(([k, v]) => (
                  <Fragment key={k}>
                    <span
                      style={{
                        color: 'var(--text-muted)',
                        textTransform: 'uppercase',
                        letterSpacing: 1,
                        fontSize: 10,
                      }}
                    >
                      {k}
                    </span>
                    <span style={{ color: 'var(--text-primary)' }}>{String(v)}</span>
                  </Fragment>
                ))}
              </div>
            )}
          </div>
        ))}
        {filtered.length === 0 && (
          <div style={{ padding: 40, textAlign: 'center', color: 'var(--text-muted)' }}>
            No logs match the current filters.
          </div>
        )}
      </div>
    </div>
  );
}
