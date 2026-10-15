// CameraPanel — 240px slide-out camera list with grouping (by retention
// policy), search, and user-defined tours persisted via /v1/me/prefs.
import { useMemo, useState, type CSSProperties } from 'react';
import { Icon } from '../components/Icon';
import { Brackets, Btn, FormRow, SectionHeader, Slider, StatusBadge, TxtInput, bareBtn, linkBtn } from '../components/ui';
import { useSnapshotURL } from '../lib/hooks';
import { useConsole, type ConsoleCamera } from '../lib/console';

export interface TourCam {
  id: string;
  dwell: number;
}
export interface Tour {
  id: string;
  name: string;
  dwell: number;
  cams: TourCam[];
}

export function useTours(): [Tour[], (ts: Tour[]) => void] {
  const data = useConsole();
  const tours = useMemo<Tour[]>(() => {
    const raw = data.prefs['tours'];
    return Array.isArray(raw) ? (raw as Tour[]) : [];
  }, [data.prefs]);
  const save = (ts: Tour[]) => { void data.savePrefs({ tours: ts }); };
  return [tours, save];
}

export function CameraPanel({ open, onClose, onPick, activeId, onStartTour, activeTour }: {
  open: boolean;
  onClose: () => void;
  onPick: (c: ConsoleCamera) => void;
  activeId: string | null;
  onStartTour: (t: Tour | null) => void;
  activeTour: Tour | null;
}) {
  const data = useConsole();
  const cameras = data.cameras;
  const [tours, saveTours] = useTours();
  const [editTour, setEditTour] = useState<Tour | null>(null);
  const [q, setQ] = useState('');
  const [closedGroups, setClosedGroups] = useState<Record<string, boolean>>({});

  const filtered = useMemo(() => {
    const norm = q.trim().toLowerCase();
    if (!norm) return cameras;
    return cameras.filter((c) =>
      c.name.toLowerCase().includes(norm) ||
      c.host.toLowerCase().includes(norm) ||
      c.group.toLowerCase().includes(norm),
    );
  }, [cameras, q]);

  const groups = useMemo(() => {
    const m: Record<string, ConsoleCamera[]> = {};
    for (const c of filtered) (m[c.group] = m[c.group] || []).push(c);
    return m;
  }, [filtered]);

  if (!open) return null;
  return (
    <aside style={{
      width: 240, background: 'var(--bg-panel)', borderRight: '1px solid var(--border)',
      display: 'flex', flexDirection: 'column', flexShrink: 0,
    }}>
      <div style={{ padding: '12px 14px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--border)' }}>
        <SectionHeader>CAMERAS</SectionHeader>
        <button onClick={onClose} title="Hide panel" style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}>
          <Icon name="panel-left-close" size={14} />
        </button>
      </div>

      <div style={{ padding: 10, borderBottom: '1px solid var(--border)' }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6, height: 30, padding: '0 8px',
          background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4,
        }}>
          <Icon name="search" size={12} color="var(--text-muted)" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search cameras"
            style={{ flex: 1, minWidth: 0, background: 'transparent', border: 'none', outline: 'none', fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-primary)' }}
          />
          {q && (
            <button onClick={() => setQ('')} style={{ ...bareBtn, color: 'var(--text-muted)', display: 'flex' }}>
              <Icon name="x" size={11} />
            </button>
          )}
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto' }}>
        {Object.entries(groups).map(([gname, list]) => {
          const isOpen = !closedGroups[gname];
          const groupTourName = `${gname} (group)`;
          return (
            <div key={gname}>
              <div style={{ padding: '12px 12px 6px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <button
                  onClick={() => setClosedGroups((g) => ({ ...g, [gname]: isOpen }))}
                  style={{ ...bareBtn, display: 'flex', alignItems: 'center', gap: 6 }}
                >
                  <Icon name={isOpen ? 'chevron-down' : 'chevron-right'} size={12} color="var(--text-secondary)" />
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)', textTransform: 'uppercase' }}>{gname} · {list.length}</span>
                </button>
                <button
                  title={`Tour ${gname}`}
                  onClick={() => onStartTour(activeTour?.name === groupTourName ? null : {
                    id: `group-${gname}`, name: groupTourName, dwell: 8,
                    cams: list.filter((c) => c.status !== 'offline').map((c) => ({ id: c.id, dwell: 8 })),
                  })}
                  style={{
                    background: activeTour?.name === groupTourName ? 'var(--accent-13)' : 'none',
                    border: `1px solid ${activeTour?.name === groupTourName ? 'var(--accent-27)' : 'transparent'}`,
                    borderRadius: 3, color: 'var(--accent)', cursor: 'pointer', padding: 3, display: 'flex',
                  }}
                >
                  <Icon name={activeTour?.name === groupTourName ? 'rotate-cw' : 'play'} size={12} />
                </button>
              </div>
              {isOpen && (
                <div style={{ padding: '0 6px' }}>
                  {list.map((c) => (
                    <CameraRow key={c.id} cam={c} active={activeId === c.id} onClick={() => onPick(c)} />
                  ))}
                </div>
              )}
            </div>
          );
        })}
        {filtered.length === 0 && (
          <div style={{ padding: 20, textAlign: 'center' }}>
            <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>
              {cameras.length === 0 ? 'No cameras yet. Add one under Devices.' : `No cameras match "${q}"`}
            </span>
          </div>
        )}
      </div>

      {/* Tours */}
      <div style={{ borderTop: '1px solid var(--border)', padding: 12, maxHeight: 260, overflowY: 'auto' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 2, color: 'var(--text-secondary)', textTransform: 'uppercase' }}>TOURS</span>
          <button onClick={() => setEditTour({ id: '', name: '', dwell: 8, cams: [] })} style={{ ...linkBtn }}>+ NEW</button>
        </div>
        {tours.length === 0 && (
          <div style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)', padding: '4px 0 8px' }}>
            No tours yet. Pick cameras and a dwell time to cycle through them.
          </div>
        )}
        {tours.map((t) => {
          const on = activeTour?.id === t.id;
          const liveCams = t.cams.filter((e) => cameras.some((c) => c.id === e.id && c.status !== 'offline'));
          const loop = liveCams.reduce((a, e) => a + (e.dwell ?? t.dwell), 0);
          const uniform = t.cams.every((e) => (e.dwell ?? t.dwell) === (t.cams[0]?.dwell ?? t.dwell));
          return (
            <div key={t.id} style={{
              marginBottom: 6, padding: 10,
              background: on ? 'var(--accent-7)' : 'var(--bg-secondary)',
              border: `1px solid ${on ? 'var(--accent-27)' : 'var(--border)'}`,
              borderRadius: 4, display: 'flex', flexDirection: 'column', gap: 4,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6 }}>
                <button onClick={() => onStartTour(on ? null : t)} style={{ ...bareBtn, flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 6, textAlign: 'left' }}>
                  <Icon name={on ? 'square' : 'play'} size={12} color="var(--accent)" />
                  <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, fontWeight: 500, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{t.name}</span>
                </button>
                {on ? <StatusBadge kind="live" label="ACTIVE" /> : (
                  <button title="Edit tour" onClick={() => setEditTour(t)} style={{ ...bareBtn, color: 'var(--text-muted)', display: 'flex' }}>
                    <Icon name="settings-2" size={12} />
                  </button>
                )}
              </div>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                {liveCams.length} cameras · {uniform ? `${t.cams[0]?.dwell ?? t.dwell}s dwell` : `${loop}s loop · mixed dwell`}
                {liveCams.length < t.cams.length ? ` · ${t.cams.length - liveCams.length} offline skipped` : ''}
              </span>
            </div>
          );
        })}
      </div>
      {editTour && (
        <TourEditor
          tour={editTour}
          cameras={cameras}
          onClose={() => setEditTour(null)}
          onSave={(t) => {
            const next = t.id && tours.some((x) => x.id === t.id)
              ? tours.map((x) => (x.id === t.id ? t : x))
              : [...tours, { ...t, id: t.id || `T${Date.now()}` }];
            saveTours(next);
            setEditTour(null);
          }}
          onDelete={(id) => {
            saveTours(tours.filter((t) => t.id !== id));
            setEditTour(null);
            if (activeTour?.id === id) onStartTour(null);
          }}
          onStart={(t) => { onStartTour(t); setEditTour(null); }}
        />
      )}
    </aside>
  );
}

/* ---------- Tour editor ---------- */

function TourEditor({ tour, cameras, onClose, onSave, onDelete, onStart }: {
  tour: Tour;
  cameras: ConsoleCamera[];
  onClose: () => void;
  onSave: (t: Tour) => void;
  onDelete: (id: string) => void;
  onStart: (t: Tour) => void;
}) {
  const [name, setName] = useState(tour.name);
  const [dwell, setDwell] = useState(tour.dwell);
  const [cams, setCams] = useState<TourCam[]>(tour.cams);
  const isNew = !tour.id;
  const valid = name.trim().length > 0 && cams.length >= 2;
  const has = (id: string) => cams.some((e) => e.id === id);
  const toggle = (id: string) => setCams((c) => (c.some((e) => e.id === id) ? c.filter((e) => e.id !== id) : [...c, { id, dwell }]));
  const setDwellAt = (i: number, v: number) => setCams((c) => c.map((e, j) => (j === i ? { ...e, dwell: Math.max(3, Math.min(60, v)) } : e)));
  const applyAll = () => setCams((c) => c.map((e) => ({ ...e, dwell })));
  const loop = cams.reduce((a, e) => a + e.dwell, 0);
  const move = (i: number, d: number) => setCams((c) => {
    const n = [...c]; const j = i + d;
    if (j < 0 || j >= n.length) return c;
    [n[i], n[j]] = [n[j], n[i]];
    return n;
  });
  const groups: Record<string, ConsoleCamera[]> = {};
  for (const c of cameras) (groups[c.group] = groups[c.group] || []).push(c);
  const built: Tour = { ...tour, name: name.trim(), dwell, cams };
  const rowBtn: CSSProperties = { width: 22, height: 22, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, cursor: 'pointer', color: 'var(--text-secondary)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', padding: 0 };

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'var(--scrim)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 880, maxWidth: '94vw', maxHeight: '85vh', background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>{isNew ? 'New tour' : `Edit · ${tour.name}`}</span>
          <button onClick={onClose} style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}><Icon name="x" size={16} /></button>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.35fr', minHeight: 0, flex: 1 }}>
          <div style={{ padding: 16, borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
            <FormRow label="Name"><TxtInput value={name} onChange={setName} placeholder="e.g. Night sweep" /></FormRow>
            <FormRow label="Default dwell"><Slider value={dwell} onChange={setDwell} min={3} max={60} suffix="s" ticks={8} /></FormRow>
            <div style={{ marginTop: -6, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontFamily: 'var(--font-sans)', fontSize: 11, color: 'var(--text-muted)' }}>Applied to cameras as you add them.</span>
              {cams.length > 0 && <button onClick={applyAll} style={{ ...linkBtn, whiteSpace: 'nowrap' }}>APPLY TO ALL</button>}
            </div>
            <div>
              <SectionHeader>ADD CAMERAS</SectionHeader>
              <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 10 }}>
                {Object.entries(groups).map(([g, list]) => (
                  <div key={g}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>{g}</span>
                      <button
                        onClick={() => setCams((c) => [...c, ...list.filter((x) => !c.some((e) => e.id === x.id)).map((x) => ({ id: x.id, dwell }))])}
                        style={{ ...linkBtn, fontSize: 10 }}
                      >ADD ALL</button>
                    </div>
                    {list.map((c) => {
                      const on = has(c.id);
                      return (
                        <button key={c.id} onClick={() => toggle(c.id)} style={{
                          width: '100%', display: 'flex', alignItems: 'center', gap: 8, padding: '5px 8px', marginBottom: 2,
                          background: on ? 'var(--accent-13)' : 'transparent',
                          border: `1px solid ${on ? 'var(--accent-27)' : 'transparent'}`,
                          borderRadius: 4, cursor: 'pointer', textAlign: 'left',
                        }}>
                          <Icon name={on ? 'check-square' : 'square'} size={13} color={on ? 'var(--accent)' : 'var(--text-muted)'} />
                          <span style={{ flex: 1, fontFamily: 'var(--font-sans)', fontSize: 12, color: on ? 'var(--accent)' : 'var(--text-primary)' }}>{c.name}</span>
                          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: c.status === 'offline' ? 'var(--danger)' : 'var(--text-muted)' }}>
                            {c.status === 'offline' ? 'OFFLINE' : c.host}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                ))}
              </div>
            </div>
          </div>
          <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 8, overflowY: 'auto' }}>
            <SectionHeader right={<span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{cams.length} · {loop}S LOOP</span>}>SEQUENCE</SectionHeader>
            {cams.length === 0 && (
              <div style={{ marginTop: 8, padding: 20, border: '1px dashed var(--border)', borderRadius: 4, textAlign: 'center', fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>
                Pick at least two cameras from the left.
              </div>
            )}
            {cams.map((entry, i) => {
              const c = cameras.find((x) => x.id === entry.id);
              if (!c) return null;
              return (
                <div key={entry.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 8px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4 }}>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--accent)', width: 18 }}>{i + 1}</span>
                  <span style={{ flex: 1, minWidth: 0, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {c.name} <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)' }}>{c.host}</span>
                  </span>
                  <div title="Dwell for this camera (seconds)" style={{ display: 'inline-flex', alignItems: 'center', border: '1px solid var(--border)', borderRadius: 3, background: 'var(--bg-primary)' }}>
                    <button style={{ ...rowBtn, width: 18, border: 'none', borderRadius: 0 }} onClick={() => setDwellAt(i, entry.dwell - 1)}><Icon name="minus" size={10} /></button>
                    <input
                      value={`${entry.dwell}s`}
                      onChange={(e) => setDwellAt(i, parseInt(e.target.value, 10) || 3)}
                      style={{ width: 32, height: 22, textAlign: 'center', background: 'transparent', border: 'none', borderLeft: '1px solid var(--border)', borderRight: '1px solid var(--border)', outline: 'none', fontFamily: 'var(--font-mono)', fontSize: 11, color: entry.dwell !== dwell ? 'var(--accent)' : 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' }}
                    />
                    <button style={{ ...rowBtn, width: 18, border: 'none', borderRadius: 0 }} onClick={() => setDwellAt(i, entry.dwell + 1)}><Icon name="plus" size={10} /></button>
                  </div>
                  <button style={rowBtn} onClick={() => move(i, -1)} disabled={i === 0}><Icon name="chevron-up" size={11} /></button>
                  <button style={rowBtn} onClick={() => move(i, 1)} disabled={i === cams.length - 1}><Icon name="chevron-down" size={11} /></button>
                  <button style={{ ...rowBtn, color: 'var(--danger)' }} onClick={() => toggle(entry.id)}><Icon name="x" size={11} /></button>
                </div>
              );
            })}
          </div>
        </div>
        <div style={{ padding: 14, borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between', gap: 8 }}>
          <div style={{ display: 'flex', gap: 8 }}>
            <Btn kind="ghost" onClick={onClose}>Cancel</Btn>
            {!isNew && <Btn kind="danger" onClick={() => onDelete(tour.id)}>Delete</Btn>}
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <Btn kind="secondary" disabled={!valid} onClick={() => { const t = { ...built, id: built.id || `T${Date.now()}` }; onSave(t); onStart(t); }}>
              <Icon name="play" size={12} />Save & start
            </Btn>
            <Btn kind="primary" disabled={!valid} onClick={() => onSave(built)}>{isNew ? 'Create tour' : 'Save'}</Btn>
          </div>
        </div>
      </div>
    </div>
  );
}

function CameraRow({ cam, onClick, active }: { cam: ConsoleCamera; onClick: () => void; active: boolean }) {
  const c = cam.status === 'online' ? 'var(--success)' : cam.status === 'degraded' ? 'var(--warning)' : 'var(--danger)';
  const [hover, setHover] = useState(false);
  const snap = useSnapshotURL(cam.id, cam.status !== 'offline', 30_000);
  return (
    <div
      onClick={onClick}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '6px 6px',
        borderRadius: 4, cursor: 'pointer', userSelect: 'none',
        background: active ? 'var(--accent-13)' : hover ? 'var(--bg-tertiary)' : 'transparent',
        border: active ? '1px solid var(--accent-27)' : '1px solid transparent',
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: c, boxShadow: `0 0 6px ${c}`, flexShrink: 0 }} />
      <div style={{ width: 44, height: 26, background: 'linear-gradient(135deg, #1a1410, #0d0c0a)', position: 'relative', flexShrink: 0, overflow: 'hidden' }}>
        {snap && (
          <img src={snap} alt="" style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover' }} />
        )}
        <Brackets />
        {cam.status === 'offline' ? (
          <Icon name="wifi-off" size={10} color="var(--danger)" style={{ position: 'absolute', inset: 0, margin: 'auto' }} />
        ) : cam.recording && (
          <span style={{ position: 'absolute', top: 2, right: 2, width: 4, height: 4, borderRadius: '50%', background: 'var(--danger)', boxShadow: '0 0 4px var(--danger)' }} />
        )}
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontFamily: 'var(--font-sans)', fontSize: 12, fontWeight: 500, color: active ? 'var(--accent)' : 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cam.name}</div>
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', letterSpacing: '.5px' }}>{cam.host}</div>
      </div>
      {cam.hasPTZ && (
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 8, letterSpacing: '.5px', color: 'var(--accent)', border: '1px solid var(--accent-27)', padding: '1px 3px', borderRadius: 2, flexShrink: 0 }}>PTZ</span>
      )}
    </div>
  );
}
