// Alerts — the events stream viewed through the acknowledge lens.
// AlertsPanel: right-side slide-out (live via the store's SSE refresh).
// AlertsFeed: full-screen filterable feed with thumbnails + ack/dismiss.
import { useMemo, useState } from 'react';
import { Icon } from '../components/Icon';
import { Btn, SectionHeader, Segmented, bareBtn, pageTitle, screenHdr } from '../components/ui';
import type { EventRecord } from '../lib/api';
import { fmtAgo } from '../lib/format';
import { useConsole } from '../lib/console';
import { CLASS_ICON, classOf, labelOf, noteOf, toneOf } from './events';

function msgOf(e: EventRecord, camName: string): string {
  switch (e.type_id) {
    case 'camera_offline': return 'Camera offline';
    case 'camera_online': return 'Camera reconnected';
    case 'bookmark': return noteOf(e) ? `Bookmark · ${noteOf(e)}` : 'Bookmark';
    default: return `${labelOf(e).charAt(0)}${labelOf(e).slice(1).toLowerCase()} detected · ${camName.toLowerCase()}`;
  }
}

const CRITICAL = new Set(['camera_offline', 'tamper', 'audio_alarm']);

export function AlertsPanel({ open, onClose, onOpenFull, onOpenPlayback }: {
  open: boolean;
  onClose: () => void;
  onOpenFull: () => void;
  onOpenPlayback: (camId: string, at?: Date) => void;
}) {
  const data = useConsole();
  const [filter, setFilter] = useState('all');
  if (!open) return null;
  const list = data.events.filter((a) =>
    filter === 'all' ? true : filter === 'unread' ? !a.acknowledged_at : CRITICAL.has(a.type_id));

  return (
    <aside style={{ width: 340, background: 'var(--bg-panel)', borderLeft: '1px solid var(--border)', display: 'flex', flexDirection: 'column', flexShrink: 0 }}>
      <div style={{ padding: '12px 14px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--border)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <SectionHeader>ALERTS</SectionHeader>
          {data.unreadCount > 0 && (
            <span style={{ padding: '1px 6px', fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 600, background: 'var(--danger)', color: 'var(--on-accent)', borderRadius: 8 }}>{data.unreadCount}</span>
          )}
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            onClick={() => void data.acknowledgeAll()}
            style={{ ...bareBtn, fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '.5px', color: 'var(--accent)', textTransform: 'uppercase' }}
          >MARK READ</button>
          <button onClick={onClose} style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}>
            <Icon name="x" size={14} />
          </button>
        </div>
      </div>

      <div style={{ padding: '10px 14px', borderBottom: '1px solid var(--border)' }}>
        <Segmented options={[{ value: 'all', label: 'ALL' }, { value: 'unread', label: 'UNREAD' }, { value: 'critical', label: 'CRITICAL' }]} value={filter} onChange={setFilter} />
      </div>

      <div style={{ flex: 1, overflowY: 'auto' }}>
        {list.length === 0 && <div style={{ padding: 24, textAlign: 'center', fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>Nothing here.</div>}
        {list.map((a) => {
          const c = toneOf(a);
          const cam = data.getCam(a.camera_id);
          const agoSecs = (data.now.getTime() - new Date(a.occurred_at).getTime()) / 1000;
          return (
            <div key={a.id} style={{
              padding: '12px 14px', borderBottom: '1px solid var(--border)',
              background: !a.acknowledged_at ? 'color-mix(in srgb, var(--accent) 4%, transparent)' : 'transparent',
              display: 'flex', gap: 10,
            }}>
              <span style={{ width: 6, height: 6, borderRadius: '50%', marginTop: 6, flexShrink: 0, background: c, boxShadow: `0 0 6px ${c}` }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
                  <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cam?.name ?? 'System'}</span>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', flexShrink: 0 }}>{fmtAgo(agoSecs)}</span>
                </div>
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>{msgOf(a, cam?.name ?? '')}</span>
                <div style={{ marginTop: 6, display: 'flex', gap: 6 }}>
                  <span style={{ padding: '2px 6px', background: 'var(--accent-13)', border: '1px solid var(--accent-27)', color: 'var(--accent)', fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', textTransform: 'uppercase' }}>{labelOf(a)}</span>
                  {cam && (
                    <button
                      onClick={() => onOpenPlayback(a.camera_id, new Date(a.occurred_at))}
                      style={{ padding: '2px 6px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', cursor: 'pointer' }}
                    >OPEN CLIP</button>
                  )}
                </div>
              </div>
            </div>
          );
        })}
      </div>
      <div style={{ padding: 12, borderTop: '1px solid var(--border)' }}>
        <button onClick={onOpenFull} style={{ width: '100%', padding: '8px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 1, color: 'var(--accent)', cursor: 'pointer', textTransform: 'uppercase' }}>
          View all alerts →
        </button>
      </div>
    </aside>
  );
}

export function AlertsFeed({ onOpenPlayback }: { onOpenPlayback: (camId: string, at?: Date) => void }) {
  const data = useConsole();
  const [filter, setFilter] = useState('all');
  const typeIds = useMemo(() => {
    const s = new Set<string>();
    for (const e of data.events) s.add(e.type_id);
    return [...s].sort();
  }, [data.events]);
  const [types, setTypes] = useState<Set<string> | null>(null); // null = all

  const filtered = useMemo(() => {
    let r = data.events;
    if (filter === 'unread') r = r.filter((a) => !a.acknowledged_at);
    if (filter === 'critical') r = r.filter((a) => CRITICAL.has(a.type_id));
    if (types) r = r.filter((a) => types.has(a.type_id));
    return r;
  }, [data.events, filter, types]);

  const counts = useMemo(() => {
    const m: Record<string, number> = {};
    for (const e of data.events) m[e.type_id] = (m[e.type_id] ?? 0) + 1;
    return m;
  }, [data.events]);

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)' }}>
      <header style={screenHdr}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={pageTitle}>Alerts</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>{data.events.length} RECENT · {data.unreadCount} UNREAD</span>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <Btn kind="ghost" onClick={() => void data.acknowledgeAll()}>Mark all read</Btn>
        </div>
      </header>

      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        <aside style={{ width: 240, borderRight: '1px solid var(--border)', background: 'var(--bg-secondary)', overflowY: 'auto', flexShrink: 0 }}>
          <div style={{ padding: 14, borderBottom: '1px solid var(--border)' }}>
            <SectionHeader>VIEW</SectionHeader>
            <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 4 }}>
              {([['all', 'All alerts'], ['unread', 'Unread'], ['critical', 'Critical']] as Array<[string, string]>).map(([v, l]) => (
                <button key={v} onClick={() => setFilter(v)} style={{
                  padding: '6px 8px', textAlign: 'left',
                  background: filter === v ? 'var(--accent-13)' : 'transparent',
                  border: `1px solid ${filter === v ? 'var(--accent-27)' : 'transparent'}`,
                  borderRadius: 4, cursor: 'pointer',
                  fontFamily: 'var(--font-sans)', fontSize: 12, color: filter === v ? 'var(--accent)' : 'var(--text-secondary)',
                }}>{l}</button>
              ))}
            </div>
          </div>
          <div style={{ padding: 14 }}>
            <SectionHeader right={types ? <button onClick={() => setTypes(null)} style={{ ...bareBtn, fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--accent)' }}>ALL</button> : undefined}>BY TYPE</SectionHeader>
            <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 4 }}>
              {typeIds.map((t) => {
                const on = !types || types.has(t);
                return (
                  <button
                    key={t}
                    onClick={() => setTypes((s) => {
                      const base = s ?? new Set(typeIds);
                      const n = new Set(base);
                      if (n.has(t)) n.delete(t); else n.add(t);
                      return n;
                    })}
                    style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '5px 8px', background: on ? 'var(--accent-7)' : 'transparent', border: '1px solid transparent', borderRadius: 4, cursor: 'pointer' }}
                  >
                    <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <Icon name={CLASS_ICON[classOf(t)]} size={12} color={on ? 'var(--text-primary)' : 'var(--text-muted)'} />
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '.5px', color: on ? 'var(--text-primary)' : 'var(--text-muted)', textTransform: 'uppercase' }}>{t.replace(/_/g, ' ')}</span>
                    </span>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{counts[t] ?? 0}</span>
                  </button>
                );
              })}
            </div>
          </div>
        </aside>

        <div style={{ flex: 1, overflowY: 'auto' }}>
          {filtered.map((a) => {
            const cam = data.getCam(a.camera_id);
            const c = toneOf(a);
            const thumb = a.thumbnail_url || a.snapshot_url;
            const agoSecs = (data.now.getTime() - new Date(a.occurred_at).getTime()) / 1000;
            return (
              <div key={a.id} style={{
                padding: 16, borderBottom: '1px solid var(--border)',
                display: 'grid', gridTemplateColumns: thumb ? '160px 1fr auto' : '1fr auto', gap: 16, alignItems: 'center',
                background: !a.acknowledged_at ? 'color-mix(in srgb, var(--accent) 4%, transparent)' : 'transparent',
              }}>
                {thumb && (
                  <div style={{ width: 160, height: 90, overflow: 'hidden', background: '#000' }}>
                    <img src={thumb} alt={a.type_id} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                  </div>
                )}
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <span style={{ width: 6, height: 6, borderRadius: '50%', background: c, boxShadow: `0 0 6px ${c}` }} />
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: c, textTransform: 'uppercase' }}>{labelOf(a)}</span>
                    {!a.acknowledged_at && <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--accent)', boxShadow: '0 0 6px var(--accent)' }} />}
                    <span style={{ fontFamily: 'var(--font-sans)', fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>{cam?.name ?? 'System'}</span>
                  </div>
                  <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)', lineHeight: 1.5 }}>{msgOf(a, cam?.name ?? '')}</span>
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)', fontVariantNumeric: 'tabular-nums' }}>{fmtAgo(agoSecs)}</span>
                  <div style={{ display: 'flex', gap: 4 }}>
                    {cam && <Btn kind="tactical" onClick={() => onOpenPlayback(a.camera_id, new Date(a.occurred_at))}>PLAY</Btn>}
                    {!a.acknowledged_at && (
                      <button title="Acknowledge" onClick={() => void data.acknowledge(a.id)} style={{ width: 24, height: 24, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, cursor: 'pointer', color: 'var(--text-muted)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
                        <Icon name="check" size={11} />
                      </button>
                    )}
                  </div>
                </div>
              </div>
            );
          })}
          {filtered.length === 0 && (
            <div style={{ padding: 60, textAlign: 'center' }}>
              <Icon name="bell-off" size={32} color="var(--text-muted)" />
              <div style={{ marginTop: 8, fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>No alerts match this filter.</div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
