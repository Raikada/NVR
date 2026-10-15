// LiveView — N×N grid (paged) with tours and focus mode (single camera
// + PTZ sidebar). Grid size persists via /v1/me/prefs.
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { Icon } from '../components/Icon';
import { SectionHeader, Segmented, StatusBadge, chip, iconBtn, linkBtn, monoLabel, pageTitle, screenHdr } from '../components/ui';
import { getCameraStats, ptzHome, ptzPresetGoto, ptzPresetSave, ptzPresets, type EventRecord, type PTZPreset } from '../lib/api';
import { fmtAgo, fmtTime } from '../lib/format';
import { useConsole, type ConsoleCamera } from '../lib/console';
import { CameraTile, EmptyTile, usePtzHold } from './CameraTile';
import type { Tour } from './CameraPanel';

const GRIDS = ['1x1', '2x2', '3x3', '4x4'];

// A camera is "in motion" when a motion-family event landed in the
// last 60 seconds.
function motionSet(events: EventRecord[], now: Date): Set<string> {
  const cutoff = now.getTime() - 60_000;
  const s = new Set<string>();
  for (const e of events) {
    if (!e.type_id.includes('motion') && e.type_id !== 'person' && e.type_id !== 'vehicle') continue;
    if (new Date(e.occurred_at).getTime() >= cutoff) s.add(e.camera_id);
  }
  return s;
}

export function LiveView({ focusId, setFocusId, onOpenPlayback, tour, onStopTour }: {
  focusId: string | null;
  setFocusId: (id: string | null) => void;
  onOpenPlayback: (camId: string, at?: Date) => void;
  tour: Tour | null;
  onStopTour: () => void;
}) {
  const data = useConsole();
  const defaultGrid = typeof data.prefs['default_grid'] === 'string' ? (data.prefs['default_grid'] as string) : '2x2';
  const [grid, setGrid] = useState(defaultGrid);
  useEffect(() => { setGrid(defaultGrid); }, [defaultGrid]);
  const [page, setPage] = useState(0);
  const [tourLeft, setTourLeft] = useState(8);

  // Tour: cycle focus through the sequence, skipping offline cameras.
  useEffect(() => {
    if (!tour) return;
    const cams = tour.cams
      .map((e) => {
        const c = data.cameras.find((x) => x.id === e.id);
        return c && c.status !== 'offline' ? { cam: c, dwell: e.dwell ?? tour.dwell } : null;
      })
      .filter((x): x is { cam: ConsoleCamera; dwell: number } => x !== null);
    if (!cams.length) { onStopTour(); return; }
    let i = 0;
    setFocusId(cams[0].cam.id);
    setTourLeft(cams[0].dwell);
    const t = window.setInterval(() => {
      setTourLeft((s) => {
        if (s <= 1) {
          i = (i + 1) % cams.length;
          setFocusId(cams[i].cam.id);
          return cams[i].dwell;
        }
        return s - 1;
      });
    }, 1000);
    return () => window.clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tour]);

  const motion = useMemo(() => motionSet(data.events, data.now), [data.events, data.now]);
  const focusCam = focusId ? data.getCam(focusId) : null;

  const step = useCallback((d: number) => {
    if (!focusId || data.cameras.length === 0) return;
    const i = data.cameras.findIndex((c) => c.id === focusId);
    const n = (i + d + data.cameras.length) % data.cameras.length;
    setFocusId(data.cameras[n].id);
  }, [focusId, data.cameras, setFocusId]);

  if (focusCam) {
    return (
      <FocusMode
        cam={focusCam}
        onExit={() => { onStopTour(); setFocusId(null); }}
        onOpenPlayback={onOpenPlayback}
        tour={tour}
        tourLeft={tourLeft}
        onStopTour={onStopTour}
        onPrev={() => step(-1)}
        onNext={() => step(1)}
        motion={motion.has(focusCam.id)}
      />
    );
  }

  const cols = parseInt(grid.split('x')[0], 10);
  const slots = cols * cols;
  const pages = Math.max(1, Math.ceil(data.cameras.length / slots));
  const pg = Math.min(page, pages - 1);
  const filled = data.cameras.slice(pg * slots, pg * slots + slots);
  const empties = Math.max(0, slots - filled.length);
  const motionCount = data.cameras.filter((c) => motion.has(c.id)).length;

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
      <header style={screenHdr}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={pageTitle}>Live View</span>
          <span style={chip}>ALL · {data.cameras.length}</span>
          {motionCount > 0 && <span style={{ ...chip, borderColor: 'var(--accent-40)' }}>{motionCount} MOTION</span>}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {pages > 1 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <button style={iconBtn} onClick={() => setPage((p) => (p - 1 + pages) % pages)}><Icon name="chevron-left" size={14} /></button>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>PAGE {pg + 1}/{pages}</span>
              <button style={iconBtn} onClick={() => setPage((p) => (p + 1) % pages)}><Icon name="chevron-right" size={14} /></button>
            </div>
          )}
          <span style={{ width: 1, height: 20, background: 'var(--border)' }} />
          <span style={monoLabel}>GRID</span>
          <Segmented
            options={GRIDS.map((v) => ({ value: v, label: v.replace('x', '×') }))}
            value={grid}
            onChange={(v) => { setGrid(v); setPage(0); void data.savePrefs({ default_grid: v }); }}
          />
          <span style={{ width: 1, height: 20, background: 'var(--border)' }} />
          <button
            title="Fullscreen"
            style={iconBtn}
            onClick={() => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen?.())}
          ><Icon name="maximize" size={14} /></button>
        </div>
      </header>
      <div style={{
        flex: 1, padding: 12, display: 'grid', gap: 8,
        gridTemplateColumns: `repeat(${cols}, minmax(0,1fr))`,
        gridTemplateRows: `repeat(${cols}, minmax(0,1fr))`,
        background: 'var(--bg-primary)', minHeight: 0,
      }}>
        {filled.map((c) => (
          <CameraTile key={c.id} camera={c} fill ptzControls motion={motion.has(c.id)} onFocus={() => setFocusId(c.id)} />
        ))}
        {Array.from({ length: empties }).map((_, i) => (
          <EmptyTile key={`e${i}`} fill label="ADD CAMERA" onClick={() => { window.location.hash = 'devices'; }} />
        ))}
      </div>
    </div>
  );
}

/* ---------- Focus Mode ---------- */

function FocusMode({ cam, onExit, onOpenPlayback, tour, tourLeft, onStopTour, onPrev, onNext, motion }: {
  cam: ConsoleCamera;
  onExit: () => void;
  onOpenPlayback: (camId: string, at?: Date) => void;
  tour: Tour | null;
  tourLeft: number;
  onStopTour: () => void;
  onPrev: () => void;
  onNext: () => void;
  motion: boolean;
}) {
  const data = useConsole();
  const [snapped, setSnapped] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const { start, stop } = usePtzHold(cam.id);
  const flash = (msg: string) => { setToast(msg); window.setTimeout(() => setToast(null), 1800); };

  // Live bitrate via stats deltas.
  const [bitrate, setBitrate] = useState<string>('');
  useEffect(() => {
    let prev: { bytes: number; ts: number } | null = null;
    const poll = () => {
      void getCameraStats(cam.id).then((s) => {
        const now = Date.now();
        if (prev && now > prev.ts) {
          const bps = ((s.bytes_received - prev.bytes) * 8) / ((now - prev.ts) / 1000);
          if (bps >= 0) setBitrate(`${(bps / 1e6).toFixed(1)} Mbps`);
        }
        prev = { bytes: s.bytes_received, ts: now };
      }).catch(() => {});
    };
    poll();
    const t = window.setInterval(poll, 5000);
    return () => window.clearInterval(t);
  }, [cam.id]);

  // Keyboard PTZ: hold an arrow to move, release to stop.
  useEffect(() => {
    if (!cam.hasPTZ) return;
    const held = new Set<string>();
    const onDown = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).tagName === 'INPUT') return;
      const m: Record<string, [number, number, number]> = {
        ArrowUp: [0, 0.5, 0], ArrowDown: [0, -0.5, 0], ArrowLeft: [-0.5, 0, 0], ArrowRight: [0.5, 0, 0],
        '+': [0, 0, 0.5], '=': [0, 0, 0.5], '-': [0, 0, -0.5],
      };
      const v = m[e.key];
      if (!v || held.has(e.key)) return;
      e.preventDefault();
      held.add(e.key);
      start(v[0], v[1], v[2]);
    };
    const onUp = (e: KeyboardEvent) => {
      if (!held.delete(e.key)) return;
      if (held.size === 0) stop();
    };
    window.addEventListener('keydown', onDown);
    window.addEventListener('keyup', onUp);
    return () => {
      window.removeEventListener('keydown', onDown);
      window.removeEventListener('keyup', onUp);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cam.id, cam.hasPTZ]);

  const snapshot = () => {
    const video = videoRef.current;
    if (!video || video.videoWidth === 0) { flash('No frame available yet'); return; }
    const canvas = document.createElement('canvas');
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    canvas.getContext('2d')?.drawImage(video, 0, 0);
    const name = `${cam.name}_${fmtTime(data.now).replace(/:/g, '')}.jpg`;
    canvas.toBlob((blob) => {
      if (!blob) return;
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = name;
      a.click();
      URL.revokeObjectURL(a.href);
    }, 'image/jpeg', 0.92);
    setSnapped(true);
    window.setTimeout(() => setSnapped(false), 800);
    flash(`Snapshot saved · ${name}`);
  };

  const camEvents = data.events.filter((e) => e.camera_id === cam.id).slice(0, 6);

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)' }}>
      <header style={{ ...screenHdr, padding: '0 16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, minWidth: 0 }}>
          <button onClick={onExit} title="Back to grid (Esc)" style={{ ...iconBtn, width: 32, height: 32 }}><Icon name="arrow-left" size={14} /></button>
          <button onClick={onPrev} title="Previous camera" style={iconBtn}><Icon name="chevron-left" size={14} /></button>
          <button onClick={onNext} title="Next camera" style={iconBtn}><Icon name="chevron-right" size={14} /></button>
          <span style={pageTitle}>{cam.name}</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}>
            {cam.model || cam.host}{cam.resolution ? ` · ${cam.resolution}` : ''}
          </span>
          <StatusBadge kind={cam.status} />
          {tour && (
            <span style={{ ...chip, display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              <Icon name="rotate-cw" size={11} />{tour.name} · NEXT IN {tourLeft}S
              <button onClick={onStopTour} style={{ background: 'none', border: 'none', color: 'var(--accent)', cursor: 'pointer', padding: 0, display: 'flex' }}><Icon name="x" size={11} /></button>
            </span>
          )}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <button title="Snapshot (downloads current frame)" onClick={snapshot} style={iconBtn}><Icon name="camera" size={14} /></button>
          <button title="Open in Playback" onClick={() => onOpenPlayback(cam.id)} style={iconBtn}><Icon name="play-circle" size={14} /></button>
          <button title="Fullscreen" onClick={() => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen?.())} style={iconBtn}><Icon name="maximize" size={14} /></button>
        </div>
      </header>

      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        <div style={{ flex: 1, padding: 16, minWidth: 0, position: 'relative', display: 'flex' }}>
          <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
            <CameraTile camera={cam} large fill ptzControls motion={motion} videoRef={videoRef} />
            {snapped && <div style={{ position: 'absolute', inset: 0, background: 'rgba(255,255,255,0.7)', animation: 'flash 600ms ease-out forwards', pointerEvents: 'none' }} />}
            <div style={{ position: 'absolute', top: 42, right: 12, display: 'flex', flexDirection: 'column', gap: 3, alignItems: 'flex-end', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--on-video)', pointerEvents: 'none' }}>
              {(bitrate || cam.codec) && <span>{[bitrate, cam.codec].filter(Boolean).join(' · ')}</span>}
              <span style={{ color: 'var(--accent)' }}>{fmtTime(data.now)}</span>
            </div>
            {toast && (
              <div style={{ position: 'absolute', top: 12, left: '50%', transform: 'translateX(-50%)', padding: '6px 12px', background: 'rgba(10,10,10,0.85)', border: '1px solid var(--accent-27)', fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--accent)', animation: 'fadein 150ms ease-out', pointerEvents: 'none' }}>
                {toast}
              </div>
            )}
          </div>
        </div>

        <aside style={{ width: 280, borderLeft: '1px solid var(--border)', background: 'var(--bg-secondary)', display: 'flex', flexDirection: 'column', flexShrink: 0, overflowY: 'auto' }}>
          {cam.hasPTZ ? (
            <PTZSection cam={cam} flash={flash} />
          ) : (
            <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
              <SectionHeader>PTZ</SectionHeader>
              <div style={{ marginTop: 10, padding: 12, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, display: 'flex', alignItems: 'center', gap: 8 }}>
                <Icon name="info" size={14} color="var(--text-muted)" />
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>Fixed-mount camera.</span>
              </div>
            </div>
          )}
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader right={<button onClick={() => onOpenPlayback(cam.id)} style={linkBtn}>TIMELINE →</button>}>RECENT EVENTS</SectionHeader>
            <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
              {camEvents.map((e) => (
                <button key={e.id} onClick={() => onOpenPlayback(cam.id, new Date(e.occurred_at))} style={{ padding: '6px 8px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, display: 'flex', alignItems: 'center', justifyContent: 'space-between', cursor: 'pointer' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ width: 5, height: 5, borderRadius: '50%', background: 'var(--accent)', boxShadow: '0 0 6px var(--accent)' }} />
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--accent)', letterSpacing: '.5px', textTransform: 'uppercase' }}>{e.type_id}</span>
                  </div>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                    {fmtAgo((data.now.getTime() - new Date(e.occurred_at).getTime()) / 1000)}
                  </span>
                </button>
              ))}
              {camEvents.length === 0 && (
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No recent events on this camera.</span>
              )}
            </div>
          </div>
          <div style={{ padding: '14px 16px' }}>
            <SectionHeader>DETAILS</SectionHeader>
            <div style={{ marginTop: 10, display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 12, rowGap: 6 }}>
              {([
                ['MODEL', cam.model || '—'],
                ['VENDOR', cam.manufacturer || '—'],
                ['HOST', cam.host || '—'],
                ['CODEC', cam.codec || '—'],
                ['RES', cam.resolution || '—'],
                ['RTSP', cam.health?.rtsp_state ?? '—'],
                ['TIER', cam.group],
              ] as Array<[string, string]>).map(([k, v]) => (
                <span key={k} style={{ display: 'contents' }}>
                  <span style={monoLabel}>{k}</span>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums' }}>{v}</span>
                </span>
              ))}
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

function PTZSection({ cam, flash }: { cam: ConsoleCamera; flash: (m: string) => void }) {
  const { start, stop } = usePtzHold(cam.id);
  const [presets, setPresets] = useState<PTZPreset[]>([]);
  const [activePreset, setActivePreset] = useState<string | null>(null);
  const reload = useCallback(() => {
    void ptzPresets(cam.id).then(setPresets).catch(() => setPresets([]));
  }, [cam.id]);
  useEffect(() => { reload(); }, [reload]);

  const btn: CSSProperties = { width: 36, height: 36, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-primary)' };

  const hold = (pan: number, tilt: number, zoom = 0) => ({
    onMouseDown: () => start(pan, tilt, zoom),
    onMouseUp: () => stop(),
    onMouseLeave: () => stop(),
  });

  return (
    <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
      <SectionHeader right={
        <button onClick={() => { void ptzHome(cam.id).catch(() => {}); setActivePreset(null); }} style={{ ...linkBtn, color: 'var(--text-secondary)' }}>HOME</button>
      }>PTZ CONTROL</SectionHeader>
      <div style={{ marginTop: 14, display: 'grid', gridTemplateColumns: '1fr auto', gap: 14, alignItems: 'center' }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 36px)', gap: 4, justifyContent: 'center' }}>
          <div />
          <button style={btn} {...hold(0, 0.5)}><Icon name="chevron-up" size={14} /></button>
          <div />
          <button style={btn} {...hold(-0.5, 0)}><Icon name="chevron-left" size={14} /></button>
          <div style={{ ...btn, background: 'var(--accent-13)', borderColor: 'var(--accent-27)', color: 'var(--accent)', cursor: 'default' }}><Icon name="circle" size={10} /></div>
          <button style={btn} {...hold(0.5, 0)}><Icon name="chevron-right" size={14} /></button>
          <div />
          <button style={btn} {...hold(0, -0.5)}><Icon name="chevron-down" size={14} /></button>
          <div />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4 }}>
          <button style={{ ...iconBtn, width: 28, height: 28 }} {...hold(0, 0, 0.5)}><Icon name="plus" size={12} /></button>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', letterSpacing: 1 }}>ZOOM</span>
          <button style={{ ...iconBtn, width: 28, height: 28 }} {...hold(0, 0, -0.5)}><Icon name="minus" size={12} /></button>
        </div>
      </div>
      <div style={{ marginTop: 10, display: 'flex', justifyContent: 'space-between', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)' }}>
        <span>HOLD TO MOVE</span>
        <span>ARROWS / + −</span>
      </div>
      <div style={{ marginTop: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={monoLabel}>PRESETS</span>
          <button
            onClick={() => {
              const name = window.prompt('Preset name (saves current position)');
              if (!name) return;
              void ptzPresetSave(cam.id, name).then(() => { flash(`Preset saved · ${name}`); reload(); }).catch(() => flash('Preset save failed'));
            }}
            style={linkBtn}
          >+ SAVE</button>
        </div>
        <div style={{ marginTop: 8, display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 6 }}>
          {presets.map((p, i) => {
            const on = activePreset === p.token;
            return (
              <button
                key={p.token}
                onClick={() => {
                  setActivePreset(p.token);
                  void ptzPresetGoto(cam.id, p.token).catch(() => flash('Preset move failed'));
                }}
                style={{ padding: '6px 8px', background: on ? 'var(--accent-13)' : 'var(--bg-tertiary)', border: `1px solid ${on ? 'var(--accent-27)' : 'var(--border)'}`, borderRadius: 4, cursor: 'pointer', textAlign: 'left', display: 'flex', alignItems: 'center', gap: 6 }}
              >
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--accent)' }}>P{i + 1}</span>
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: on ? 'var(--accent)' : 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name || p.token}</span>
              </button>
            );
          })}
          {presets.length === 0 && (
            <span style={{ gridColumn: '1 / -1', fontFamily: 'var(--font-sans)', fontSize: 11, color: 'var(--text-muted)' }}>No presets on this camera yet.</span>
          )}
        </div>
      </div>
    </div>
  );
}
