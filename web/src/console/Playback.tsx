// Playback — direct-manipulation timeline over real footage.
//   video     fmp4 time-range chunks from the playback server (:9996
//             /get?path&start&duration), re-chunked on seek; live edge
//             switches to the HLS live stream.
//   timeline  real coverage (GET /v1/cameras/:id/coverage) renders
//             recorded ranges vs gaps; real event markers; wheel zoom,
//             drag-pan ruler, click/drag seek; 24h overview strip.
//   export    in/out marks → bookmark event + clip export (signed
//             download URL); no-range export uses before/after rolls.
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { Icon } from '../components/Icon';
import { Btn, FormRow, Segmented, Slider, TxtInput, chip, pageTitle } from '../components/ui';
import {
  createBookmark, createEventClip, getCameraCoverage, listEvents, waitForClip,
  type CoverageEntry, type EventRecord,
} from '../lib/api';
import { fmtAgo, fmtDate, fmtDelta, fmtTime } from '../lib/format';
import { useConsole, type ConsoleCamera } from '../lib/console';
import { CameraTile, LiveVideo } from './CameraTile';

const ZOOMS = [5, 10, 30, 60, 180, 360, 1440]; // minutes in view
const MAX_AGO = 7 * 1440; // minutes — matches the 7d coverage fetch
const CHUNK_SECS = 600;

function clsColor(typeId: string): string {
  if (typeId.includes('person')) return 'var(--accent)';
  if (typeId.includes('vehicle')) return '#3B82F6';
  if (typeId.includes('package')) return 'var(--success)';
  if (typeId === 'bookmark') return '#A855F7';
  return '#737373';
}

interface InOut {
  in: number | null; // minutes ago (in > out)
  out: number | null;
}

export function Playback({ camera, initialAt }: {
  camera: ConsoleCamera | null;
  initialAt?: Date;
}) {
  const data = useConsole();
  const fallback = data.cameras[0];
  const startCam = camera ?? fallback;
  const initialPos = initialAt
    ? Math.max(0, (Date.now() - initialAt.getTime()) / 60000)
    : 45;

  const [playing, setPlaying] = useState(true);
  const [speed, setSpeed] = useState(1);
  const [pos, setPos] = useState(initialPos); // minutes ago; 0 = live edge
  const [viewLen, setViewLen] = useState(60);
  const [viewEnd, setViewEnd] = useState(Math.max(0, initialPos - 30));
  const [activeId, setActiveId] = useState(startCam?.id ?? '');
  const [tracks, setTracks] = useState<string[]>(() => (startCam ? [startCam.id] : []));
  const [inOut, setInOut] = useState<InOut>({ in: null, out: null });
  const [exporting, setExporting] = useState(false);
  const [gotoOpen, setGotoOpen] = useState(false);
  const [sidebar, setSidebar] = useState('events');
  const posRef = useRef(pos);
  posRef.current = pos;
  const inOutRef = useRef(inOut);
  inOutRef.current = inOut;

  // Keep track list sane when cameras load after mount.
  useEffect(() => {
    if (tracks.length === 0 && fallback) {
      setTracks([fallback.id]);
      setActiveId(fallback.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fallback?.id]);

  const cam = data.getCam(activeId) ?? fallback;
  const phTime = new Date(data.now.getTime() - pos * 60000);
  const viewStart = viewEnd + viewLen;

  /* ----- coverage + per-track events ----- */
  const [coverage, setCoverage] = useState<Record<string, CoverageEntry[]>>({});
  const [trackEvents, setTrackEvents] = useState<Record<string, EventRecord[]>>({});
  useEffect(() => {
    let dead = false;
    const load = () => {
      for (const tid of tracks) {
        const c = data.getCam(tid);
        if (!c) continue;
        const from = new Date(Date.now() - MAX_AGO * 60000).toISOString();
        void getCameraCoverage(tid, from, new Date().toISOString())
          .then((entries) => { if (!dead) setCoverage((m) => ({ ...m, [tid]: entries })); })
          .catch(() => {});
        void listEvents({ cameraId: tid, itemsPerPage: 200 })
          .then((r) => { if (!dead) setTrackEvents((m) => ({ ...m, [tid]: r.items ?? [] })); })
          .catch(() => {});
      }
    };
    load();
    const t = window.setInterval(load, 60_000);
    return () => { dead = true; window.clearInterval(t); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tracks.join(',')]);

  const agoOf = useCallback(
    (iso: string) => (data.now.getTime() - new Date(iso).getTime()) / 60000,
    [data.now],
  );

  const inGap = useCallback((tid: string, m: number): boolean => {
    const entries = coverage[tid];
    if (!entries || entries.length === 0) return true;
    const t = data.now.getTime() - m * 60000;
    // Treat "near live edge" as covered — the current segment is still
    // being written and may not appear in coverage yet.
    if (m < 2) return false;
    return !entries.some((e) => {
      const s = new Date(e.start).getTime();
      return t >= s && t <= s + e.duration * 1000 + 15_000;
    });
  }, [coverage, data.now]);

  /* ----- transport ----- */
  const seek = useCallback((m: number, opts: { center?: boolean } = {}) => {
    const p = Math.max(0, Math.min(MAX_AGO, m));
    setPos(p);
    setViewEnd((ve) => {
      const vs = ve + viewLen;
      if (opts.center || p < ve || p > vs) return Math.max(0, p - viewLen / 2);
      return ve;
    });
  }, [viewLen]);

  useEffect(() => {
    if (!playing) return;
    const t = window.setInterval(() => {
      setPos((p) => {
        const n = Math.max(0, p - speed / 60);
        if (n === 0 && p !== 0) setPlaying(false);
        return n;
      });
    }, 1000);
    return () => window.clearInterval(t);
  }, [playing, speed]);

  useEffect(() => {
    if (pos < viewEnd) setViewEnd(Math.max(0, pos - viewLen * 0.85));
    else if (pos > viewEnd + viewLen) setViewEnd(Math.max(0, pos - viewLen * 0.15));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pos]);

  const zoomTo = (len: number, anchor = pos) => {
    const L = Math.max(1, Math.min(1440, len));
    const frac = (viewStart - anchor) / viewLen;
    const newStart = anchor + frac * L;
    setViewLen(L);
    setViewEnd(Math.max(0, newStart - L));
  };
  const zoomStep = (dir: number, anchor?: number) => {
    const i = ZOOMS.findIndex((z) => z >= viewLen);
    const j = Math.max(0, Math.min(ZOOMS.length - 1, (i < 0 ? ZOOMS.length - 1 : i) + dir));
    zoomTo(ZOOMS[j], anchor ?? pos);
  };

  const camEvents = useMemo(
    () => (trackEvents[activeId] ?? []).slice().sort((a, b) => agoOf(a.occurred_at) - agoOf(b.occurred_at)),
    [trackEvents, activeId, agoOf],
  );
  const prevEvent = () => {
    const p = posRef.current;
    const e = camEvents.find((x) => agoOf(x.occurred_at) > p + 0.05);
    if (e) seek(agoOf(e.occurred_at));
  };
  const nextEvent = () => {
    const p = posRef.current;
    const e = [...camEvents].reverse().find((x) => agoOf(x.occurred_at) < p - 0.05);
    if (e) seek(agoOf(e.occurred_at));
  };
  const step = (secs: number) => seek(posRef.current - secs / 60);
  const setIn = () => { const p = posRef.current; setInOut((r) => ({ in: p, out: r.out != null && r.out < p ? r.out : null })); };
  const setOut = () => { const p = posRef.current; setInOut((r) => ({ out: p, in: r.in != null && r.in > p ? r.in : null })); };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement).tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
      const k = e.key.toLowerCase();
      if (e.key === ' ') { e.preventDefault(); setPlaying((p) => !p); }
      else if (e.key === 'ArrowLeft') { e.preventDefault(); step(e.shiftKey ? -60 : -5); }
      else if (e.key === 'ArrowRight') { e.preventDefault(); step(e.shiftKey ? 60 : 5); }
      else if (k === 'j') setSpeed((s) => Math.max(0.25, s / 2));
      else if (k === 'l') setSpeed((s) => Math.min(16, s * 2));
      else if (k === 'k') setPlaying((p) => !p);
      else if (e.key === '[') prevEvent();
      else if (e.key === ']') nextEvent();
      else if (k === 'm') {
        if (inOutRef.current.in == null || inOutRef.current.out != null) { setInOut({ in: null, out: null }); setIn(); }
        else setOut();
      }
      else if (k === 'i') setIn();
      else if (k === 'o') setOut();
      else if (e.key === '+' || e.key === '=') zoomStep(-1);
      else if (e.key === '-') zoomStep(1);
      else if (k === 'g') setGotoOpen(true);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewLen, viewEnd, camEvents]);

  if (!cam) {
    return (
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-muted)' }}>No cameras yet — add one under Devices.</span>
      </div>
    );
  }

  const currentEvent = camEvents.find((e) => Math.abs(agoOf(e.occurred_at) - pos) < 0.2);
  const dayLabel = fmtDate(phTime) === fmtDate(data.now) ? 'Today' : fmtDate(phTime);
  const rangeSecs = inOut.in != null && inOut.out != null ? Math.round((inOut.in - inOut.out) * 60) : null;

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)' }}>
      <header style={{ height: 52, padding: '0 16px 0 20px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, flexShrink: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, minWidth: 0 }}>
          <span style={pageTitle}>Playback</span>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}>{cam.name}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {rangeSecs != null && (
            <span style={{ ...chip, border: '1px solid var(--accent-27)', background: 'var(--accent-7)' }}>CLIP · {rangeSecs}s</span>
          )}
          <Btn kind="tactical" onClick={() => setExporting(true)}>
            <Icon name="scissors" size={12} />{rangeSecs != null ? 'EXPORT CLIP' : 'EXPORT'}
          </Btn>
        </div>
      </header>

      <div style={{ flex: 1, minHeight: 0, display: 'flex' }}>
        <div style={{ flex: 1, minWidth: 0, padding: 12, position: 'relative', display: 'flex', flexDirection: 'column', gap: 8 }}>
          <SyncedGrid
            tracks={tracks}
            activeId={activeId}
            setActiveId={setActiveId}
            pos={pos}
            playing={playing}
            speed={speed}
            phTime={phTime}
            inGap={inGap}
            trackEvents={trackEvents}
            agoOf={agoOf}
          />
        </div>
        <aside style={{ width: 272, flexShrink: 0, borderLeft: '1px solid var(--border)', background: 'var(--bg-secondary)', display: 'flex', flexDirection: 'column', minHeight: 0 }}>
          <div style={{ padding: 10, borderBottom: '1px solid var(--border)' }}>
            <Segmented
              options={[{ value: 'events', label: `EVENTS · ${camEvents.length}` }, { value: 'tracks', label: `TRACKS · ${tracks.length}` }]}
              value={sidebar}
              onChange={setSidebar}
            />
          </div>
          {sidebar === 'events' ? (
            <div style={{ flex: 1, overflowY: 'auto' }}>
              {camEvents.length === 0 && (
                <div style={{ padding: 20, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No events on {cam.name} yet.</div>
              )}
              {camEvents.map((e) => {
                const m = agoOf(e.occurred_at);
                const on = currentEvent?.id === e.id;
                return (
                  <button key={e.id} onClick={() => seek(m)} style={{ width: '100%', padding: '8px 12px', background: on ? 'var(--accent-7)' : 'transparent', border: 'none', borderLeft: `2px solid ${on ? 'var(--accent)' : 'transparent'}`, borderBottom: '1px solid var(--border)', cursor: 'pointer', textAlign: 'left', display: 'grid', gridTemplateColumns: '8px 1fr auto', gap: 10, alignItems: 'center' }}>
                    <span style={{ width: 8, height: 8, borderRadius: 2, background: clsColor(e.type_id) }} />
                    <span style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', letterSpacing: '.5px', textTransform: 'uppercase' }}>{e.type_id}</span>
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{fmtAgo(m * 60)}</span>
                    </span>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: on ? 'var(--accent)' : 'var(--text-secondary)', fontVariantNumeric: 'tabular-nums' }}>
                      {fmtTime(new Date(e.occurred_at)).slice(0, 5)}
                    </span>
                  </button>
                );
              })}
            </div>
          ) : (
            <div style={{ flex: 1, overflowY: 'auto', padding: 8 }}>
              <div style={{ padding: '4px 6px 8px', fontFamily: 'var(--font-sans)', fontSize: 11, color: 'var(--text-muted)' }}>Tracks play in sync. Click a track to make it the main view.</div>
              {data.cameras.map((c) => {
                const on = tracks.includes(c.id);
                const isMain = c.id === activeId;
                return (
                  <div key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 8px', borderRadius: 4, background: isMain ? 'var(--accent-13)' : 'transparent', border: `1px solid ${isMain ? 'var(--accent-27)' : 'transparent'}`, marginBottom: 2 }}>
                    <button
                      onClick={() => setTracks((t) => (on ? (t.length > 1 ? t.filter((x) => x !== c.id) : t) : [...t, c.id]))}
                      title={on ? 'Remove track' : 'Add track'}
                      style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0, display: 'flex' }}
                    >
                      <Icon name={on ? 'check-square' : 'square'} size={14} color={on ? 'var(--accent)' : 'var(--text-muted)'} />
                    </button>
                    <button
                      onClick={() => { if (!on) setTracks((t) => [...t, c.id]); setActiveId(c.id); }}
                      style={{ flex: 1, minWidth: 0, background: 'none', border: 'none', cursor: 'pointer', padding: 0, textAlign: 'left', display: 'flex', flexDirection: 'column' }}
                    >
                      <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: isMain ? 'var(--accent)' : 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.name}</span>
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)' }}>
                        {c.host}{c.status === 'offline' ? ' · OFFLINE · RECORDINGS ONLY' : ''}
                      </span>
                    </button>
                    {isMain && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--accent)', letterSpacing: '.5px' }}>MAIN</span>}
                  </div>
                );
              })}
            </div>
          )}
        </aside>
      </div>

      {/* transport */}
      <div style={{ borderTop: '1px solid var(--border)', padding: '10px 20px', display: 'grid', gridTemplateColumns: '1fr auto 1fr', alignItems: 'center', gap: 16, background: 'var(--bg-secondary)', flexShrink: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <button onClick={prevEvent} title="Previous event  [" style={transBtn}><Icon name="skip-back" size={13} /></button>
          <button
            onClick={() => setPlaying(!playing)}
            title="Space play/pause · ←/→ ±5s · J/L speed · +/− or wheel zoom · [ ] events · M mark · G go to"
            style={{ width: 38, height: 38, borderRadius: '50%', background: 'var(--accent)', border: 'none', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', color: 'var(--on-accent)', boxShadow: 'var(--glow-accent-md)', margin: '0 4px' }}
          >
            <Icon name={playing ? 'pause' : 'play'} size={16} />
          </button>
          <button onClick={nextEvent} title="Next event  ]" style={transBtn}><Icon name="skip-forward" size={13} /></button>
          <span style={{ width: 1, height: 20, background: 'var(--border)', margin: '0 6px' }} />
          <SpeedSelect value={speed} onChange={setSpeed} />
        </div>
        <button onClick={() => setGotoOpen(true)} title="Go to date & time  G" style={{ justifySelf: 'center', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 3, background: 'none', border: 'none', cursor: 'pointer', padding: '2px 10px', borderRadius: 4 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 24, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums', lineHeight: 1 }}>{fmtTime(phTime)}</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>
            {dayLabel.toUpperCase()}{pos < 0.05 ? ' · LIVE' : ` · −${fmtDelta(pos)}`}
          </span>
        </button>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, justifySelf: 'end' }}>
          <ClipMarker inOut={inOut} onStart={setIn} onEnd={setOut} onClear={() => setInOut({ in: null, out: null })} rangeSecs={rangeSecs} />
          <span style={{ width: 1, height: 20, background: 'var(--border)', margin: '0 6px' }} />
          <button
            onClick={() => { seek(0, { center: true }); setPlaying(true); }}
            title="Jump to live"
            style={{ padding: '6px 10px', background: 'var(--success-7)', border: '1px solid var(--success-27)', color: 'var(--success)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, borderRadius: 3, cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 6 }}
          >
            <span style={{ width: 5, height: 5, borderRadius: '50%', background: 'var(--success)', boxShadow: '0 0 6px var(--success)' }} />LIVE
          </button>
        </div>
      </div>

      <Timeline
        tracks={tracks}
        activeId={activeId}
        setActiveId={setActiveId}
        pos={pos}
        viewLen={viewLen}
        viewEnd={viewEnd}
        setViewEnd={setViewEnd}
        onSeek={seek}
        onZoom={zoomStep}
        inOut={inOut}
        coverage={coverage}
        trackEvents={trackEvents}
        agoOf={agoOf}
      />

      {exporting && (
        <ExportModal cam={cam} phTime={phTime} range={rangeSecs != null ? inOut : null} nowMs={data.now.getTime()} onClose={() => setExporting(false)} />
      )}
      {gotoOpen && (
        <GotoModal now={data.now} onClose={() => setGotoOpen(false)} onGo={(m) => { seek(m, { center: true }); setGotoOpen(false); }} />
      )}
    </div>
  );
}

/* ---------- chunked fmp4 time-range video ---------- */

function playbackURL(path: string, startMs: number): string {
  const qs = new URLSearchParams({
    path,
    start: new Date(startMs).toISOString(),
    duration: String(CHUNK_SECS),
    format: 'fmp4',
  });
  return `${window.location.protocol}//${window.location.hostname}:9996/get?${qs}`;
}

function PlaybackVideo({ path, pos, playing, speed, gap }: {
  path: string;
  pos: number; // minutes ago
  playing: boolean;
  speed: number;
  gap: boolean;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [chunk, setChunk] = useState<{ startMs: number; url: string } | null>(null);
  const [failed, setFailed] = useState(false);
  const live = pos < 0.05;

  // Re-chunk when the playhead leaves the current chunk (or none yet).
  useEffect(() => {
    if (live || gap) return;
    const playheadMs = Date.now() - pos * 60000;
    const offset = chunk ? (playheadMs - chunk.startMs) / 1000 : -1;
    if (chunk && offset >= 0 && offset < CHUNK_SECS - 5) return;
    const startMs = playheadMs;
    setFailed(false);
    setChunk({ startMs, url: playbackURL(path, startMs) });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [Math.floor(pos * 6), path, live, gap]); // re-evaluate every ~10s of pos drift

  // Keep the <video> element synced to the logical playhead.
  useEffect(() => {
    const v = videoRef.current;
    if (!v || live || gap || !chunk) return;
    const playheadMs = Date.now() - pos * 60000;
    const want = (playheadMs - chunk.startMs) / 1000;
    if (Number.isFinite(v.currentTime) && Math.abs(v.currentTime - want) > 2.5) {
      try { v.currentTime = Math.max(0, want); } catch { /* not seekable yet */ }
    }
    v.playbackRate = speed;
    if (playing && v.paused) v.play().catch(() => {});
    if (!playing && !v.paused) v.pause();
  }, [pos, playing, speed, chunk, live, gap]);

  if (live) {
    return <LiveVideo path={path} />;
  }
  if (gap) {
    return <div style={{ position: 'absolute', inset: 0, background: '#07060a' }} />;
  }
  return (
    <>
      <video
        key={chunk?.url}
        ref={videoRef}
        src={chunk?.url}
        muted
        playsInline
        autoPlay
        onError={() => setFailed(true)}
        style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'contain', background: '#000' }}
      />
      {failed && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }}>
          <span style={{ padding: '4px 10px', background: 'rgba(10,10,10,0.75)', border: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>FOOTAGE UNAVAILABLE</span>
        </div>
      )}
    </>
  );
}

/* ---------- synced multi-track grid ---------- */

function SyncedGrid({ tracks, activeId, setActiveId, pos, playing, speed, phTime, inGap, trackEvents, agoOf }: {
  tracks: string[];
  activeId: string;
  setActiveId: (id: string) => void;
  pos: number;
  playing: boolean;
  speed: number;
  phTime: Date;
  inGap: (tid: string, m: number) => boolean;
  trackEvents: Record<string, EventRecord[]>;
  agoOf: (iso: string) => number;
}) {
  const data = useConsole();
  const n = tracks.length;
  const cols = n <= 1 ? 1 : n === 2 ? 2 : n <= 4 ? 2 : 3;
  const rows = Math.ceil(n / cols);
  const badge = (extra: CSSProperties): CSSProperties => ({
    position: 'absolute', padding: '3px 8px', background: 'rgba(10,10,10,0.7)', border: '1px solid var(--border)',
    fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, pointerEvents: 'none', ...extra,
  });
  return (
    <div style={{ flex: 1, minHeight: 0, display: 'grid', gap: 8, gridTemplateColumns: `repeat(${cols}, minmax(0,1fr))`, gridTemplateRows: `repeat(${rows}, minmax(0,1fr))` }}>
      {tracks.map((tid) => {
        const c = data.getCam(tid);
        if (!c) return null;
        const isMain = tid === activeId;
        const gap = inGap(tid, pos);
        const ev = (trackEvents[tid] ?? []).find((e) => Math.abs(agoOf(e.occurred_at) - pos) < 0.2);
        return (
          <div key={tid} style={{ position: 'relative', minHeight: 0, minWidth: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <div
              onClick={() => setActiveId(tid)}
              style={{ position: 'relative', aspectRatio: '16 / 9', maxWidth: '100%', maxHeight: '100%', width: '100%', outline: isMain && n > 1 ? '2px solid var(--accent)' : 'none', outlineOffset: -2, cursor: n > 1 ? 'pointer' : 'default' }}
            >
              <CameraTile
                camera={c}
                large={n <= 2}
                fill
                dim={gap}
                media={<PlaybackVideo path={c.name} pos={pos} playing={playing} speed={speed} gap={gap} />}
              />
              {/* playback-time corner overlay */}
              <div style={badge({ bottom: 10, right: 10, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums' })}>{fmtTime(phTime)}</div>
              {pos >= 0.05 && <div style={badge({ top: 10, left: 10, color: 'var(--danger)' })}>PLAYBACK</div>}
              {gap && (
                <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }}>
                  <span style={{ padding: '4px 10px', background: 'rgba(10,10,10,0.75)', border: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>NO RECORDING</span>
                </div>
              )}
              {isMain && !playing && <div style={badge({ top: 44, left: 12, color: 'var(--warning)' })}>PAUSED</div>}
              {ev && <div style={badge({ top: 44, right: 12, color: clsColor(ev.type_id), borderColor: clsColor(ev.type_id), textTransform: 'uppercase' })}>{ev.type_id} EVENT</div>}
            </div>
          </div>
        );
      })}
    </div>
  );
}

/* ---------- clip marker + speed ---------- */

const transBtn: CSSProperties = { width: 30, height: 30, borderRadius: 4, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', cursor: 'pointer', color: 'var(--text-primary)', display: 'flex', alignItems: 'center', justifyContent: 'center' };

function ClipMarker({ inOut, onStart, onEnd, onClear, rangeSecs }: {
  inOut: InOut; onStart: () => void; onEnd: () => void; onClear: () => void; rangeSecs: number | null;
}) {
  const stage = inOut.in == null ? 0 : inOut.out == null ? 1 : 2;
  const base: CSSProperties = { ...transBtn, width: 'auto', padding: '0 10px', gap: 6, fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1 };
  if (stage === 0) {
    return <button onClick={onStart} title="Mark where the clip starts  M" style={base}><Icon name="scissors" size={12} />MARK CLIP</button>;
  }
  if (stage === 1) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <button onClick={onEnd} title="Mark where the clip ends  M" style={{ ...base, background: 'var(--accent-13)', borderColor: 'var(--accent-27)', color: 'var(--accent)' }}>
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--accent)', boxShadow: 'var(--glow-accent-sm)' }} />SET CLIP END
        </button>
        <button onClick={onClear} title="Cancel" style={transBtn}><Icon name="x" size={12} /></button>
      </div>
    );
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
      <span style={{ ...base, cursor: 'default', color: 'var(--accent)', borderColor: 'var(--accent-27)' }}><Icon name="scissors" size={12} />CLIP · {rangeSecs}s</span>
      <button onClick={onClear} title="Clear clip" style={transBtn}><Icon name="x" size={12} /></button>
    </div>
  );
}

function SpeedSelect({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  return (
    <label title="Playback speed  J / L" style={{ position: 'relative', display: 'inline-flex', alignItems: 'center' }}>
      <select
        value={value}
        onChange={(e) => onChange(parseFloat(e.target.value))}
        style={{ appearance: 'none', WebkitAppearance: 'none', height: 30, padding: '0 26px 0 10px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, color: 'var(--text-primary)', fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '.5px', cursor: 'pointer', outline: 'none' }}
      >
        {[0.25, 0.5, 1, 2, 4, 8, 16].map((v) => <option key={v} value={v}>{v}× speed</option>)}
      </select>
      <Icon name="chevron-down" size={12} color="var(--text-secondary)" style={{ position: 'absolute', right: 8, pointerEvents: 'none' }} />
    </label>
  );
}

/* ---------- Timeline ---------- */

function Timeline({ tracks, activeId, setActiveId, pos, viewLen, viewEnd, setViewEnd, onSeek, onZoom, inOut, coverage, trackEvents, agoOf }: {
  tracks: string[];
  activeId: string;
  setActiveId: (id: string) => void;
  pos: number;
  viewLen: number;
  viewEnd: number;
  setViewEnd: (f: (v: number) => number) => void;
  onSeek: (m: number, opts?: { center?: boolean }) => void;
  onZoom: (dir: number, anchor?: number) => void;
  inOut: InOut;
  coverage: Record<string, CoverageEntry[]>;
  trackEvents: Record<string, EventRecord[]>;
  agoOf: (iso: string) => number;
}) {
  const data = useConsole();
  const ref = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{ x: number; m: number } | null>(null);
  const [drag, setDrag] = useState<'seek' | 'pan' | null>(null);
  const LABEL = 140;
  const viewStart = viewEnd + viewLen;
  const xOf = (m: number) => (viewStart - m) / viewLen; // 0..1
  const mAt = (clientX: number) => {
    const el = ref.current;
    if (!el) return pos;
    const r = el.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (clientX - r.left) / r.width));
    return viewStart - f * viewLen;
  };

  const begin = (mode: 'seek' | 'pan') => (e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startEnd = viewEnd;
    setDrag(mode);
    if (mode === 'seek') onSeek(mAt(e.clientX));
    const move = (ev: globalThis.MouseEvent) => {
      if (mode === 'seek') onSeek(mAt(ev.clientX));
      else {
        const el = ref.current;
        if (!el) return;
        const r = el.getBoundingClientRect();
        const dm = ((ev.clientX - startX) / r.width) * viewLen;
        setViewEnd(() => Math.max(0, Math.min(MAX_AGO - viewLen, startEnd + dm)));
      }
    };
    const up = () => { setDrag(null); window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up); };
    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', up);
  };

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) {
        const d = ((e.deltaX || e.deltaY) / 600) * viewLen;
        setViewEnd((v) => Math.max(0, Math.min(MAX_AGO - viewLen, v - d)));
      } else {
        onZoom(e.deltaY > 0 ? 1 : -1, mAt(e.clientX));
      }
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  });

  const stepMin = [0.5, 1, 2, 5, 10, 15, 30, 60, 120, 180, 360].find((s) => viewLen / s <= 12) || 360;
  const nowMs = data.now.getTime();
  const ticks: number[] = [];
  {
    const firstAbs = Math.ceil((nowMs - viewStart * 60000) / (stepMin * 60000)) * stepMin * 60000;
    for (let t = firstAbs; t <= nowMs - viewEnd * 60000; t += stepMin * 60000) ticks.push(t);
  }

  const inX = inOut.in != null ? xOf(inOut.in) : null;
  const outX = inOut.out != null ? xOf(inOut.out) : null;
  const phX = xOf(pos);
  const phVisible = phX >= 0 && phX <= 1;

  return (
    <div style={{ borderTop: '1px solid var(--border)', background: 'var(--bg-secondary)', flexShrink: 0, userSelect: 'none' }}>
      <div style={{ display: 'grid', gridTemplateColumns: `${LABEL}px 1fr`, padding: '8px 20px 0' }}>
        <div style={{ height: 22 }} />
        <div onMouseDown={begin('pan')} title="Drag to pan · wheel to zoom" style={{ position: 'relative', height: 22, cursor: drag === 'pan' ? 'grabbing' : 'grab', borderBottom: '1px solid var(--border)' }}>
          {ticks.map((t) => {
            const m = (nowMs - t) / 60000;
            const x = xOf(m);
            const major = new Date(t).getMinutes() % (stepMin >= 60 ? 60 : stepMin * 2) === 0;
            return (
              <div key={t} style={{ position: 'absolute', left: `${x * 100}%`, bottom: 0, transform: 'translateX(-50%)', display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap', marginBottom: 2 }}>
                  {major || stepMin < 2 ? fmtTime(new Date(t)).slice(0, 5) : ''}
                </span>
                <span style={{ width: 1, height: major ? 6 : 3, background: 'var(--border)' }} />
              </div>
            );
          })}
          {viewEnd === 0 && <span style={{ position: 'absolute', right: 0, top: 0, fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: 1, color: 'var(--success)' }}>LIVE ▸</span>}
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: `${LABEL}px 1fr`, padding: '4px 20px 0' }}>
        <div>
          {tracks.map((tid) => {
            const c = data.getCam(tid);
            if (!c) return null;
            const on = tid === activeId;
            return (
              <button key={tid} onClick={() => setActiveId(tid)} title={c.name} style={{ width: '100%', height: 28, marginBottom: 2, padding: '0 8px', display: 'flex', alignItems: 'center', gap: 6, background: on ? 'var(--accent-13)' : 'var(--bg-primary)', border: `1px solid ${on ? 'var(--accent-27)' : 'var(--border)'}`, borderRight: 'none', borderRadius: '3px 0 0 3px', cursor: 'pointer' }}>
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 11, color: on ? 'var(--accent)' : 'var(--text-secondary)', letterSpacing: '.5px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.name}</span>
              </button>
            );
          })}
        </div>
        <div
          ref={ref}
          onMouseDown={begin('seek')}
          onMouseMove={(e) => {
            const el = ref.current;
            if (!el) return;
            setHover({ x: e.clientX - el.getBoundingClientRect().left, m: mAt(e.clientX) });
          }}
          onMouseLeave={() => setHover(null)}
          style={{ position: 'relative', cursor: drag === 'seek' ? 'grabbing' : 'crosshair' }}
        >
          {tracks.map((tid) => {
            const c = data.getCam(tid);
            if (!c) return null;
            const on = tid === activeId;
            const entries = coverage[tid] ?? [];
            const evs = (trackEvents[tid] ?? []).filter((e) => {
              const m = agoOf(e.occurred_at);
              return m <= viewStart && m >= viewEnd;
            });
            return (
              <div key={tid} style={{ position: 'relative', height: 28, marginBottom: 2, background: 'var(--bg-primary)', border: `1px solid ${on ? 'var(--accent-27)' : 'var(--border)'}`, borderLeft: 'none', overflow: 'hidden' }}>
                {/* gap texture behind, covered ranges drawn solid on top */}
                <div style={{ position: 'absolute', left: 0, right: 0, top: 5, height: 7, background: 'var(--bg-primary)', backgroundImage: 'repeating-linear-gradient(45deg, var(--border) 0 3px, transparent 3px 6px)' }} />
                {entries.map((e, i) => {
                  const startM = (nowMs - new Date(e.start).getTime()) / 60000;
                  const endM = startM - e.duration / 60;
                  const l = xOf(startM);
                  const r = xOf(Math.max(0, endM));
                  if (r < 0 || l > 1) return null;
                  return (
                    <div key={i} style={{ position: 'absolute', left: `${Math.max(0, l) * 100}%`, width: `${(Math.min(1, r) - Math.max(0, l)) * 100}%`, top: 5, height: 7, background: on ? 'var(--accent-27)' : 'var(--accent-13)' }} />
                  );
                })}
                {evs.map((e) => {
                  const m = agoOf(e.occurred_at);
                  const l = xOf(m);
                  const w = Math.max(0.004, (10 / 60) / viewLen);
                  return (
                    <div
                      key={e.id}
                      onMouseDown={(ev) => { ev.stopPropagation(); setActiveId(tid); onSeek(m); }}
                      title={`${e.type_id} · ${fmtTime(new Date(e.occurred_at))}`}
                      style={{ position: 'absolute', left: `${l * 100}%`, width: `${w * 100}%`, minWidth: 3, top: 15, height: 8, background: clsColor(e.type_id), opacity: on ? 1 : 0.6, cursor: 'pointer', borderRadius: 1 }}
                    />
                  );
                })}
              </div>
            );
          })}

          {(inX != null || outX != null) && (
            <div style={{ position: 'absolute', top: 0, bottom: 2, left: `${Math.max(0, inX ?? 0) * 100}%`, right: `${(1 - Math.min(1, outX ?? 1)) * 100}%`, background: 'var(--accent-7)', borderLeft: inX != null ? '1px dashed var(--accent)' : 'none', borderRight: outX != null ? '1px dashed var(--accent)' : 'none', pointerEvents: 'none' }} />
          )}
          {inX != null && inX >= 0 && inX <= 1 && <Flag x={inX} label="START" />}
          {outX != null && outX >= 0 && outX <= 1 && <Flag x={outX} label="END" right />}

          {hover && !drag && (
            <div style={{ position: 'absolute', left: hover.x, top: 0, bottom: 2, width: 1, background: 'var(--text-secondary)', pointerEvents: 'none', zIndex: 2 }}>
              <span style={{ position: 'absolute', top: -22, left: 0, transform: `translateX(${hover.x < 40 ? '0' : hover.x > (ref.current?.clientWidth ?? 0) - 40 ? '-100%' : '-50%'})`, padding: '2px 6px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
                {fmtTime(new Date(nowMs - hover.m * 60000))}
              </span>
            </div>
          )}

          {phVisible && (
            <div style={{ position: 'absolute', left: `${phX * 100}%`, top: -4, bottom: 0, width: 2, marginLeft: -1, background: 'var(--accent)', boxShadow: 'var(--glow-accent-md)', pointerEvents: 'none', zIndex: 3 }}>
              <div style={{ position: 'absolute', top: -2, left: '50%', transform: 'translateX(-50%)', width: 0, height: 0, borderLeft: '6px solid transparent', borderRight: '6px solid transparent', borderTop: '7px solid var(--accent)' }} />
            </div>
          )}
          {!phVisible && (
            <button
              onClick={() => onSeek(pos, { center: true })}
              style={{ position: 'absolute', top: 4, [phX < 0 ? 'left' : 'right']: 6, padding: '2px 8px', background: 'var(--accent)', color: 'var(--on-accent)', border: 'none', borderRadius: 3, fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: 1, cursor: 'pointer', zIndex: 3 } as CSSProperties}
            >
              {phX < 0 ? '◂ ' : ''}PLAYHEAD{phX < 0 ? '' : ' ▸'}
            </button>
          )}
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: `${LABEL}px 1fr`, padding: '6px 20px 10px' }}>
        <div />
        <Overview pos={pos} viewLen={viewLen} viewEnd={viewEnd} setViewEnd={setViewEnd} onSeek={onSeek} events={trackEvents[activeId] ?? []} agoOf={agoOf} />
      </div>
    </div>
  );
}

function Flag({ x, label, right }: { x: number; label: string; right?: boolean }) {
  return (
    <div style={{ position: 'absolute', left: `${x * 100}%`, top: -4, bottom: 2, width: 0, pointerEvents: 'none', zIndex: 2 }}>
      <span style={{ position: 'absolute', top: -2, [right ? 'right' : 'left']: 0, padding: '1px 5px', background: 'var(--accent)', color: 'var(--on-accent)', fontFamily: 'var(--font-mono)', fontSize: 8, letterSpacing: 1, fontWeight: 600, borderRadius: right ? '2px 0 0 2px' : '0 2px 2px 0' } as CSSProperties}>{label}</span>
    </div>
  );
}

function Overview({ pos, viewLen, viewEnd, setViewEnd, onSeek, events, agoOf }: {
  pos: number;
  viewLen: number;
  viewEnd: number;
  setViewEnd: (f: (v: number) => number) => void;
  onSeek: (m: number, opts?: { center?: boolean }) => void;
  events: EventRecord[];
  agoOf: (iso: string) => number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const DAY = 1440;
  const xOf = (m: number) => 1 - Math.min(DAY, m) / DAY;
  const begin = (e: React.MouseEvent) => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const toM = (cx: number) => (1 - Math.max(0, Math.min(1, (cx - r.left) / r.width))) * DAY;
    const startM = toM(e.clientX);
    const inWin = startM >= viewEnd && startM <= viewEnd + viewLen;
    const startEnd = viewEnd;
    if (!inWin) onSeek(startM, { center: true });
    const move = (ev: globalThis.MouseEvent) => {
      if (inWin) setViewEnd(() => Math.max(0, Math.min(DAY - viewLen, startEnd + (startM - toM(ev.clientX)))));
      else onSeek(toM(ev.clientX));
    };
    const up = () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up); };
    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', up);
  };
  const buckets = new Array(48).fill(0) as number[];
  events.forEach((e) => {
    const m = agoOf(e.occurred_at);
    if (m < DAY) buckets[Math.min(47, Math.floor(xOf(m) * 48))]++;
  });
  const max = Math.max(1, ...buckets);
  return (
    <div ref={ref} onMouseDown={begin} title="Last 24 hours · drag the window or click to jump" style={{ position: 'relative', height: 14, border: '1px solid var(--border)', background: 'var(--bg-primary)', cursor: 'pointer', overflow: 'hidden' }}>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 5, height: 3, background: 'var(--accent-13)' }} />
      {buckets.map((n, i) => n > 0 && (
        <div key={i} style={{ position: 'absolute', left: `${(i / 48) * 100}%`, width: `${100 / 48}%`, bottom: 1, height: Math.max(2, (n / max) * 8), background: 'var(--accent)', opacity: 0.5 }} />
      ))}
      <div style={{ position: 'absolute', top: -1, bottom: -1, left: `${xOf(viewEnd + viewLen) * 100}%`, width: `${Math.max(0.4, (viewLen / DAY) * 100)}%`, border: '1px solid var(--accent)', background: 'var(--accent-13)', cursor: 'grab' }} />
      <div style={{ position: 'absolute', top: 0, bottom: 0, left: `${xOf(pos) * 100}%`, width: 2, marginLeft: -1, background: 'var(--accent)' }} />
    </div>
  );
}

/* ---------- Go to time ---------- */

function GotoModal({ now, onClose, onGo }: { now: Date; onClose: () => void; onGo: (m: number) => void }) {
  const [date, setDate] = useState(fmtDate(now));
  const [time, setTime] = useState(fmtTime(now).slice(0, 5));
  const target = new Date(`${date}T${time}:00`);
  const m = (now.getTime() - target.getTime()) / 60000;
  const ok = !Number.isNaN(m) && m >= 0 && m <= MAX_AGO;
  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'var(--scrim)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 380, background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6 }}>
        <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)', fontFamily: 'var(--font-sans)', fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>Go to time</div>
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <FormRow label="Date"><TxtInput type="date" value={date} onChange={setDate} /></FormRow>
          <FormRow label="Time"><TxtInput type="time" value={time} onChange={setTime} /></FormRow>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: ok ? 'var(--text-muted)' : 'var(--danger)' }}>
            {ok ? (m < 1 ? 'LIVE EDGE' : `${fmtDelta(m)} AGO`) : m < 0 ? 'IN THE FUTURE' : 'OLDER THAN THE 7-DAY TIMELINE'}
          </span>
        </div>
        <div style={{ padding: 14, borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Btn kind="ghost" onClick={onClose}>Cancel</Btn>
          <Btn kind="primary" disabled={!ok} onClick={() => onGo(m)}>Go</Btn>
        </div>
      </div>
    </div>
  );
}

/* ---------- Export ---------- */

function ExportModal({ cam, phTime, range, nowMs, onClose }: {
  cam: ConsoleCamera;
  phTime: Date;
  range: InOut | null;
  nowMs: number;
  onClose: () => void;
}) {
  const [before, setBefore] = useState(30);
  const [after, setAfter] = useState(30);
  const [state, setState] = useState<'idle' | 'working' | 'ready' | 'failed'>('idle');
  const [error, setError] = useState('');
  const [downloadURL, setDownloadURL] = useState('');

  const secs = range && range.in != null && range.out != null ? Math.round((range.in - range.out) * 60) : before + after;
  const startT = range && range.in != null
    ? new Date(nowMs - range.in * 60000)
    : new Date(phTime.getTime() - before * 1000);

  // A clip rides an event: for a marked range we drop a bookmark at the
  // start and export [0, range] around it; for a centered export we drop
  // the bookmark at the playhead and use before/after rolls.
  const start = async () => {
    setState('working');
    setError('');
    try {
      const anchor = range && range.in != null ? new Date(nowMs - range.in * 60000) : phTime;
      const bm = await createBookmark(cam.id, 'clip export', anchor.toISOString());
      const body = range
        ? { pre_roll_seconds: 0, post_roll_seconds: secs }
        : { pre_roll_seconds: before, post_roll_seconds: after };
      const clip = await createEventClip(bm.id, body);
      await waitForClip(clip.clip.id);
      setDownloadURL(clip.download_url);
      setState('ready');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'export failed');
      setState('failed');
    }
  };

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'var(--scrim)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 520, background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6 }}>
        <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 15, fontWeight: 600, color: 'var(--text-primary)' }}>Export clip · {cam.name}</span>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-secondary)', padding: 0, display: 'flex' }}><Icon name="x" size={16} /></button>
        </div>
        <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
          {range ? (
            <div style={{ padding: 12, background: 'var(--bg-tertiary)', border: '1px solid var(--accent-27)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', display: 'flex', justifyContent: 'space-between' }}>
              <span><span style={{ color: 'var(--text-muted)' }}>START </span>{fmtTime(startT)}</span>
              <span><span style={{ color: 'var(--text-muted)' }}>END </span>{fmtTime(new Date(startT.getTime() + secs * 1000))}</span>
              <span style={{ color: 'var(--accent)' }}>{secs}s</span>
            </div>
          ) : (
            <>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
                CENTERED ON <span style={{ color: 'var(--accent)' }}>{fmtTime(phTime)}</span> · {fmtDate(phTime)} · use Mark clip on the timeline for an exact range
              </div>
              <FormRow label="Before"><Slider value={before} onChange={setBefore} min={5} max={300} suffix="s" ticks={11} /></FormRow>
              <FormRow label="After"><Slider value={after} onChange={setAfter} min={5} max={300} suffix="s" ticks={11} /></FormRow>
            </>
          )}
          <FormRow label="Format"><span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)' }}>MP4 · original stream, no re-encode</span></FormRow>
          {state === 'working' && (
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)', letterSpacing: '.5px' }}>REMUXING…</span>
          )}
          {state === 'ready' && (
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--success)', letterSpacing: '.5px' }}>READY · signed link valid ~15 minutes</span>
          )}
          {state === 'failed' && (
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--danger)', letterSpacing: '.5px' }}>FAILED · {error}</span>
          )}
        </div>
        <div style={{ padding: 14, borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between' }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)', alignSelf: 'center' }}>{secs}s</span>
          <div style={{ display: 'flex', gap: 8 }}>
            <Btn kind="ghost" onClick={onClose}>{state === 'ready' ? 'Close' : 'Cancel'}</Btn>
            {state === 'ready' ? (
              <a href={downloadURL} download style={{ textDecoration: 'none' }}>
                <Btn kind="primary"><Icon name="download" size={12} />Download</Btn>
              </a>
            ) : (
              <Btn kind="primary" onClick={start} disabled={state === 'working'}>Export</Btn>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
