// Event Search — filters (class / time / camera), sort, result grid
// with real snapshot thumbnails, detail flyout with play-in-playback,
// clip export, and acknowledge.
import { useEffect, useMemo, useState } from 'react';
import { Icon } from '../components/Icon';
import { Btn, SectionHeader, Segmented, monoLabel, pageTitle, screenHdr } from '../components/ui';
import { createEventClip, listEvents, waitForClip, type EventRecord } from '../lib/api';
import { fmtAgo, fmtTime } from '../lib/format';
import { useConsole, type ConsoleCamera } from '../lib/console';
import { CLASS_ICON, classOf, labelOf, noteOf, type EventClass } from './events';

const FILTER_CLASSES: EventClass[] = ['PERSON', 'VEHICLE', 'PACKAGE', 'MOTION', 'DOORBELL', 'BOOKMARK'];
const TIME_MINS: Record<string, number> = { '1H': 60, '6H': 360, '24H': 1440, '7D': 10080 };

export function Search({ onOpenPlayback }: { onOpenPlayback: (camId: string, at?: Date) => void }) {
  const data = useConsole();
  const [classes, setClasses] = useState<Set<EventClass>>(new Set(FILTER_CLASSES));
  const [time, setTime] = useState('24H');
  const [camFilter, setCamFilter] = useState('all');
  const [sort, setSort] = useState('newest');
  const [selected, setSelected] = useState<EventRecord | null>(null);
  const [pool, setPool] = useState<EventRecord[]>([]);

  // Camera-filtered search re-fetches (the shared store only holds the
  // recent slice); "all" rides the store so SSE keeps it live.
  useEffect(() => {
    if (camFilter === 'all') { setPool(data.events); return; }
    void listEvents({ cameraId: camFilter, itemsPerPage: 200 })
      .then((r) => setPool(r.items ?? []))
      .catch(() => setPool([]));
  }, [camFilter, data.events]);

  const timeMins = TIME_MINS[time];
  const results = useMemo(() => {
    const nowMs = data.now.getTime();
    const r = pool.filter((e) => {
      const ago = (nowMs - new Date(e.occurred_at).getTime()) / 60000;
      return classes.has(classOf(e.type_id)) && ago <= timeMins && e.type_id !== 'camera_online' && e.type_id !== 'camera_offline';
    });
    r.sort((a, b) => sort === 'oldest'
      ? new Date(a.occurred_at).getTime() - new Date(b.occurred_at).getTime()
      : new Date(b.occurred_at).getTime() - new Date(a.occurred_at).getTime());
    return r;
  }, [pool, classes, timeMins, sort, data.now]);

  const toggleClass = (c: EventClass) => setClasses((s) => {
    const n = new Set(s);
    if (n.has(c)) n.delete(c); else n.add(c);
    return n;
  });

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)' }}>
      <header style={screenHdr}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={pageTitle}>Event Search</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}>{results.length} of {pool.length} events</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={monoLabel}>SORT</span>
          <Segmented options={[{ value: 'newest', label: 'NEWEST' }, { value: 'oldest', label: 'OLDEST' }]} value={sort} onChange={setSort} />
        </div>
      </header>

      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        <aside style={{ width: 240, borderRight: '1px solid var(--border)', background: 'var(--bg-secondary)', display: 'flex', flexDirection: 'column', overflowY: 'auto', flexShrink: 0 }}>
          <div style={{ padding: 14, borderBottom: '1px solid var(--border)' }}>
            <SectionHeader>OBJECT CLASS</SectionHeader>
            <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
              {FILTER_CLASSES.map((c) => {
                const on = classes.has(c);
                return (
                  <button key={c} onClick={() => toggleClass(c)} style={{
                    display: 'flex', alignItems: 'center', gap: 8, padding: '6px 8px',
                    background: on ? 'var(--accent-13)' : 'var(--bg-tertiary)',
                    border: `1px solid ${on ? 'var(--accent-27)' : 'var(--border)'}`,
                    borderRadius: 4, cursor: 'pointer',
                  }}>
                    <Icon name={CLASS_ICON[c]} size={13} color={on ? 'var(--accent)' : 'var(--text-secondary)'} />
                    <span style={{ flex: 1, textAlign: 'left', fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '.5px', color: on ? 'var(--accent)' : 'var(--text-secondary)' }}>{c}</span>
                    {on && <Icon name="check" size={12} color="var(--accent)" />}
                  </button>
                );
              })}
            </div>
          </div>

          <div style={{ padding: 14, borderBottom: '1px solid var(--border)' }}>
            <SectionHeader>TIME RANGE</SectionHeader>
            <div style={{ marginTop: 10 }}>
              <Segmented options={['1H', '6H', '24H', '7D']} value={time} onChange={setTime} />
            </div>
          </div>

          <div style={{ padding: 14 }}>
            <SectionHeader>CAMERA</SectionHeader>
            <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 260, overflowY: 'auto' }}>
              <CamFilterRow label="All cameras" active={camFilter === 'all'} onClick={() => setCamFilter('all')} count={data.events.length} />
              {data.cameras.map((c) => {
                const cnt = data.events.filter((e) => e.camera_id === c.id).length;
                return <CamFilterRow key={c.id} label={c.name} active={camFilter === c.id} onClick={() => setCamFilter(c.id)} count={cnt} />;
              })}
            </div>
          </div>
        </aside>

        <div style={{ flex: 1, padding: 16, overflowY: 'auto' }}>
          {results.length === 0 ? (
            <div style={{ border: '1px dashed var(--border)', padding: 40, borderRadius: 6, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 }}>
              <Icon name="search-x" size={32} color="var(--text-muted)" />
              <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>No events match the current filters.</span>
              <button
                onClick={() => { setClasses(new Set(FILTER_CLASSES)); setTime('7D'); setCamFilter('all'); }}
                style={{ padding: '6px 12px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--accent)', cursor: 'pointer', textTransform: 'uppercase' }}
              >RESET FILTERS</button>
            </div>
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 12, alignContent: 'start' }}>
              {results.map((r) => (
                <ResultCard key={r.id} ev={r} cam={data.getCam(r.camera_id)} onClick={() => setSelected(r)} active={selected?.id === r.id} nowMs={data.now.getTime()} />
              ))}
            </div>
          )}
        </div>

        {selected && (
          <EventDetail
            ev={selected}
            cam={data.getCam(selected.camera_id)}
            onClose={() => setSelected(null)}
            onOpenPlayback={onOpenPlayback}
            onAck={() => void data.acknowledge(selected.id)}
            nowMs={data.now.getTime()}
          />
        )}
      </div>
    </div>
  );
}

function CamFilterRow({ label, active, onClick, count }: { label: string; active: boolean; onClick: () => void; count: number }) {
  return (
    <button onClick={onClick} style={{
      display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '5px 8px',
      background: active ? 'var(--accent-13)' : 'transparent',
      border: `1px solid ${active ? 'var(--accent-27)' : 'transparent'}`,
      borderRadius: 4, cursor: 'pointer',
    }}>
      <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: active ? 'var(--accent)' : 'var(--text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 140 }}>{label}</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: active ? 'var(--accent)' : 'var(--text-muted)' }}>{count}</span>
    </button>
  );
}

// Thumb — the event's signed snapshot/thumbnail when captured; a
// bracketed placeholder otherwise.
function Thumb({ ev, tall }: { ev: EventRecord; tall?: boolean }) {
  const url = ev.thumbnail_url || ev.snapshot_url;
  return (
    <div style={{ position: 'relative', aspectRatio: '16 / 9', background: 'linear-gradient(135deg, #1a1410, #0d0c0a)', overflow: 'hidden' }}>
      {url ? (
        <img src={url} alt={ev.type_id} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover' }} />
      ) : (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <Icon name={CLASS_ICON[classOf(ev.type_id)]} size={tall ? 28 : 20} color="var(--accent-40)" />
        </div>
      )}
    </div>
  );
}

function ResultCard({ ev, cam, onClick, active, nowMs }: {
  ev: EventRecord; cam?: ConsoleCamera; onClick: () => void; active: boolean; nowMs: number;
}) {
  const agoSecs = (nowMs - new Date(ev.occurred_at).getTime()) / 1000;
  return (
    <div onClick={onClick} style={{
      background: 'var(--bg-secondary)',
      border: `1px solid ${active ? 'var(--accent-40)' : 'var(--border)'}`,
      borderRadius: 6, overflow: 'hidden', cursor: 'pointer',
    }}>
      <div style={{ position: 'relative' }}>
        <Thumb ev={ev} />
        <div style={{ position: 'absolute', top: 8, left: 8, padding: '2px 6px', background: 'rgba(10,10,10,0.7)', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--on-video)' }}>
          {fmtAgo(agoSecs)}
        </div>
        {!ev.acknowledged_at && (
          <span style={{ position: 'absolute', top: 8, right: 8, width: 8, height: 8, borderRadius: '50%', background: 'var(--accent)', boxShadow: '0 0 6px var(--accent)' }} />
        )}
      </div>
      <div style={{ padding: 10, display: 'flex', flexDirection: 'column', gap: 4 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, fontWeight: 500, color: 'var(--text-primary)' }}>{cam?.name ?? ev.camera_id}</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--accent)', letterSpacing: '.5px' }}>{labelOf(ev)}</span>
        </div>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
          {fmtTime(new Date(ev.occurred_at))}{noteOf(ev) ? ` · ${noteOf(ev)}` : ''}{ev.clip_id ? ' · clip' : ''}
        </span>
      </div>
    </div>
  );
}

function EventDetail({ ev, cam, onClose, onOpenPlayback, onAck, nowMs }: {
  ev: EventRecord;
  cam?: ConsoleCamera;
  onClose: () => void;
  onOpenPlayback: (camId: string, at?: Date) => void;
  onAck: () => void;
  nowMs: number;
}) {
  const [exportState, setExportState] = useState<'idle' | 'working' | 'ready' | 'failed'>('idle');
  const [downloadURL, setDownloadURL] = useState('');
  const doExport = () => {
    setExportState('working');
    void createEventClip(ev.id)
      .then(async (r) => {
        await waitForClip(r.clip.id);
        setDownloadURL(r.download_url);
        setExportState('ready');
      })
      .catch(() => setExportState('failed'));
  };
  return (
    <aside style={{ width: 380, flexShrink: 0, background: 'var(--bg-secondary)', borderLeft: '1px solid var(--border)', display: 'flex', flexDirection: 'column', overflowY: 'auto' }}>
      <div style={{ padding: '12px 14px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--border)' }}>
        <SectionHeader>EVENT DETAIL</SectionHeader>
        <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-secondary)', padding: 0, display: 'flex' }}>
          <Icon name="x" size={14} />
        </button>
      </div>
      <div style={{ padding: 14 }}>
        {ev.snapshot_url ? (
          <div style={{ position: 'relative', aspectRatio: '16 / 9', overflow: 'hidden' }}>
            <img src={ev.snapshot_url} alt={ev.type_id} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'contain', background: '#000' }} />
          </div>
        ) : (
          <Thumb ev={ev} tall />
        )}
      </div>
      <div style={{ padding: '0 14px 14px', display: 'flex', flexDirection: 'column', gap: 10 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ padding: '4px 8px', background: 'var(--accent)', color: 'var(--on-accent)', fontFamily: 'var(--font-mono)', fontSize: 11, fontWeight: 600, letterSpacing: '.5px' }}>{labelOf(ev)}</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>· {ev.source}</span>
          {!ev.acknowledged_at && (
            <button onClick={onAck} style={{ marginLeft: 'auto', padding: '3px 8px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', color: 'var(--text-secondary)', cursor: 'pointer' }}>ACK</button>
          )}
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 12, rowGap: 6, marginTop: 4 }}>
          {([
            ['CAMERA', cam?.name ?? ev.camera_id],
            ['TIME', `${fmtTime(new Date(ev.occurred_at))} · ${fmtAgo((nowMs - new Date(ev.occurred_at).getTime()) / 1000)}`],
            ['SEVERITY', ev.severity || '—'],
            ['SOURCE', ev.source],
            ['ACK', ev.acknowledged_at ? `by ${ev.acknowledged_by ?? '—'}` : 'unacknowledged'],
            ...(noteOf(ev) ? ([['NOTE', noteOf(ev) as string]] as Array<[string, string]>) : []),
          ] as Array<[string, string]>).map(([k, v]) => (
            <span key={k} style={{ display: 'contents' }}>
              <span style={monoLabel}>{k}</span>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' }}>{v}</span>
            </span>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
          <Btn kind="primary" onClick={() => onOpenPlayback(ev.camera_id, new Date(ev.occurred_at))}>
            <Icon name="play" size={12} />Play in Playback
          </Btn>
          {exportState === 'ready' ? (
            <a href={downloadURL} download style={{ textDecoration: 'none' }}>
              <Btn kind="secondary"><Icon name="download" size={12} />Download clip</Btn>
            </a>
          ) : (
            <Btn kind="secondary" onClick={doExport} disabled={exportState === 'working'}>
              <Icon name="download" size={12} />{exportState === 'working' ? 'Exporting…' : exportState === 'failed' ? 'Export failed — retry' : 'Export'}
            </Btn>
          )}
        </div>
      </div>
    </aside>
  );
}
