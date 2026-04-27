// Logs route — live-tail with level/source segmented filters, free-
// text search, expand-row structured fields, export. Faithful port
// of the design's LogsRoute.

import { Fragment, useEffect, useRef, useState } from 'react';
import { Btn, Segmented } from '../components/primitives';
import { Icon } from '../components/Icon';
import { PageHeader } from '../components/PageHeader';
import { fetchEvents } from '../lib/api';
import type { Event as ApiEvent } from '../lib/api';
import { usePoll, formatTime } from '../lib/hooks';
import type { ToastInput } from '../lib/types';

// /v1/events surfaces canonical Event severities only — info /
// warning / error. The design's prototype carried a DEBUG filter
// because the mock log generator had a debug tier; canonical
// Events don't, so we don't expose it on the real wiring.
const LEVELS = ['ALL', 'ERROR', 'WARN', 'INFO'] as const;
// Subject-kind filter — maps to canonical Event.subject_kind. The
// design's NETWORK option is dropped: no canonical subject kind
// models a network-plane event today. SERVER covers server-scoped
// emissions (auth.failed_login, config.applied, etc.); CAMERA
// scopes to camera lifecycle; RECORDING covers stream + segment
// (the two recorder-data subjects).
const SOURCES = ['ALL', 'SERVER', 'CAMERA', 'RECORDING'] as const;

type LevelFilter = (typeof LEVELS)[number];
type SourceFilter = (typeof SOURCES)[number];

function severityColor(s: ApiEvent['severity']): string {
  return { error: '#EF4444', warning: '#EAB308', info: '#F97316' }[s] || '#E5E5E5';
}

// Map the source filter onto canonical Event.subject_kind values.
// Returns null when the filter is ALL.
function subjectKindForSource(s: SourceFilter): string[] | null {
  if (s === 'ALL') return null;
  if (s === 'SERVER') return ['server'];
  if (s === 'CAMERA') return ['camera'];
  return ['stream', 'segment']; // RECORDING
}

interface LogsProps {
  addToast: (t: ToastInput) => void;
}

export function Logs({ addToast }: LogsProps) {
  const [tailing, setTailing] = useState(true);
  const [query, setQuery] = useState('');
  const [level, setLevel] = useState<LevelFilter>('ALL');
  const [source, setSource] = useState<SourceFilter>('ALL');
  const [expanded, setExpanded] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Live-tail by polling /v1/events. Matches the design's "live tail
  // toggle"; pause keeps the current snapshot visible. The events
  // ring buffer is bounded server-side, so we get at most ~1000
  // back per request — fine for the design's 400-line cap.
  const events = usePoll(
    () => fetchEvents({ perPage: 200 }),
    tailing ? 1100 : 60_000,
    [tailing],
  );

  // Auto-scroll to bottom on new data while tailing.
  useEffect(() => {
    if (tailing && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
  }, [events.data, tailing]);

  const all: ApiEvent[] = events.data?.items ?? [];
  const subjectFilter = subjectKindForSource(source);
  const filtered = all.filter((e) => {
    if (level !== 'ALL') {
      const map: Record<Exclude<LevelFilter, 'ALL'>, ApiEvent['severity']> = {
        ERROR: 'error',
        WARN: 'warning',
        INFO: 'info',
      };
      if (map[level] !== e.severity) return false;
    }
    if (subjectFilter && !subjectFilter.includes(e.subject_kind)) return false;
    if (query) {
      const hay = (e.message + ' ' + e.kind + ' ' + e.subject_kind).toLowerCase();
      if (!hay.includes(query.toLowerCase())) return false;
    }
    return true;
  });

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
        sub={
          events.status === 'error'
            ? `Recorder unreachable — ${events.error.message}`
            : `${filtered.length} visible of ${all.length} · ${tailing ? 'live tail' : 'paused'}`
        }
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
        {filtered.map((e) => {
          const fields: Record<string, string> = {
            kind: e.kind,
            subject_kind: e.subject_kind,
            subject_id: e.subject_id,
            ...(e.attributes ?? {}),
          };
          if (e.correlation_id) fields.correlation_id = e.correlation_id;
          return (
            <div key={e.id}>
              <button
                onClick={() => setExpanded(expanded === e.id ? null : e.id)}
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
                onMouseEnter={(ev) => (ev.currentTarget.style.background = 'rgba(249,115,22,0.04)')}
                onMouseLeave={(ev) => (ev.currentTarget.style.background = 'transparent')}
              >
                <Icon
                  name={expanded === e.id ? 'chevron-down' : 'chevron-right'}
                  style={{ width: 10, height: 10, color: 'var(--text-muted)' }}
                />
                <span style={{ color: 'var(--text-muted)', fontVariantNumeric: 'tabular-nums' }}>
                  {formatTime(e.occurred_at)}
                </span>
                <span
                  style={{
                    color: severityColor(e.severity),
                    textTransform: 'uppercase',
                    letterSpacing: 1,
                    fontSize: 10,
                  }}
                >
                  {e.severity}
                </span>
                <span
                  style={{
                    color: 'var(--text-secondary)',
                    fontSize: 10,
                    letterSpacing: 1,
                    textTransform: 'uppercase',
                  }}
                >
                  {e.subject_kind}
                </span>
                <span
                  style={{
                    color: 'var(--text-primary)',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {e.message}
                </span>
              </button>
              {expanded === e.id && (
                <div
                  style={{
                    margin: '4px 20px 8px 42px',
                    padding: 10,
                    background: 'var(--bg-secondary)',
                    border: '1px solid var(--border)',
                    borderRadius: 4,
                    display: 'grid',
                    gridTemplateColumns: '140px 1fr',
                    gap: '4px 12px',
                    fontSize: 11,
                  }}
                >
                  {Object.entries(fields).map(([k, v]) => (
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
          );
        })}
        {filtered.length === 0 && (
          <div style={{ padding: 40, textAlign: 'center', color: 'var(--text-muted)' }}>
            No logs match the current filters.
          </div>
        )}
      </div>
    </div>
  );
}
