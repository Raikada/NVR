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
  createBookmark, createEventClip, getCameraCoverage, getStoredToken, listEvents, waitForClip,
  type CoverageEntry, type EventRecord,
} from '../lib/api';
import { fmtAgo, fmtDate, fmtDelta, fmtTime } from '../lib/format';
import { useConsole, type ConsoleCamera } from '../lib/console';
import { CameraTile, LiveVideo } from './CameraTile';

const ZOOMS = [5, 10, 30, 60, 180, 360, 1440]; // minutes in view
const MAX_AGO = 7 * 1440; // minutes — matches the 7d coverage fetch
// Chunk length per /playback/get fetch. The browser downloads the whole
// response, so every seek costs up to a full chunk of NAS mux + transfer
// — 3 minutes keeps that bounded while boundary refetches stay rare.
const CHUNK_SECS = 180;
// Within this distance of "now" the playhead rides the live edge and the
// tiles switch to the HLS live stream.
const LIVE_EDGE_MS = 3000;
// Playing forward to within this distance of "now" hands off to live.
const CATCHUP_LIVE_MS = 15_000;

// What the active track's <video> is doing — drives whether the shared
// clock may free-run (gap/failed) or must wait for the video (buffering).
type DriverMediaState = 'none' | 'loading' | 'playing' | 'failed';

function clsColor(typeId: string): string {
  if (typeId.includes('person')) return 'var(--accent)';
  if (typeId.includes('vehicle')) return '#3B82F6';
  if (typeId.includes('package')) return 'var(--success)';
  if (typeId === 'bookmark') return '#A855F7';
  return '#737373';
}

// Clip marks as absolute instants (epoch ms); startMs < endMs.
interface InOut {
  startMs: number | null;
  endMs: number | null;
}

export function Playback({ camera, initialAt }: {
  camera: ConsoleCamera | null;
  initialAt?: Date;
}) {
  const data = useConsole();
  const fallback = data.cameras[0];
  const startCam = camera ?? fallback;
  const initialMs = initialAt ? initialAt.getTime() : Date.now() - 45 * 60000;
  const initialPos = Math.max(0, (Date.now() - initialMs) / 60000);

  const [playing, setPlaying] = useState(true);
  const [speed, setSpeed] = useState(1);
  // The playhead is an absolute instant. While a recorded chunk plays,
  // the active track's <video> drives it via onTime so timeline and
  // footage cannot drift apart; paused it is frozen; in coverage gaps
  // and at the live edge a wall-clock ticker advances it.
  const [absMs, setAbsMs] = useState(initialMs);
  const [viewLen, setViewLen] = useState(60);
  const [viewEnd, setViewEnd] = useState(Math.max(0, initialPos - 30));
  const [activeId, setActiveId] = useState(startCam?.id ?? '');
  const [tracks, setTracks] = useState<string[]>(() => (startCam ? [startCam.id] : []));
  const [inOut, setInOut] = useState<InOut>({ startMs: null, endMs: null });
  const [exporting, setExporting] = useState(false);
  const [gotoOpen, setGotoOpen] = useState(false);
  const [sidebar, setSidebar] = useState('events');
  // pos — minutes behind now; derived, used only for timeline geometry.
  const pos = Math.max(0, (data.now.getTime() - absMs) / 60000);
  const live = data.now.getTime() - absMs < LIVE_EDGE_MS;
  const absRef = useRef(absMs);
  absRef.current = absMs;
  const playingRef = useRef(playing);
  playingRef.current = playing;
  const speedRef = useRef(speed);
  speedRef.current = speed;
  const inOutRef = useRef(inOut);
  inOutRef.current = inOut;
  // Stamped on each video-driven clock update; the ticker only advances
  // the clock when the video hasn't reported recently.
  const lastDriveRef = useRef(0);
  // Driver media state (state for the BUFFERING badge, ref for the
  // ticker, which must read it inside setInterval without re-arming).
  const [driverMedia, setDriverMedia] = useState<DriverMediaState>('none');
  const driverMediaRef = useRef<DriverMediaState>('none');
  const onMediaState = useCallback((s: DriverMediaState) => {
    driverMediaRef.current = s;
    setDriverMedia(s);
  }, []);

  // Keep track list sane when cameras load after mount.
  useEffect(() => {
    if (tracks.length === 0 && fallback) {
      setTracks([fallback.id]);
      setActiveId(fallback.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fallback?.id]);

  const cam = data.getCam(activeId) ?? fallback;
  const phTime = new Date(absMs);
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
  const seekAbs = useCallback((ms: number, opts: { center?: boolean } = {}) => {
    const now = Date.now();
    const clamped = Math.min(now, Math.max(now - MAX_AGO * 60000, ms));
    lastDriveRef.current = 0; // the seek wins over in-flight timeupdates
    setAbsMs(clamped);
    const p = (now - clamped) / 60000;
    setViewEnd((ve) => {
      const vs = ve + viewLen;
      if (opts.center || p < ve || p > vs) return Math.max(0, p - viewLen / 2);
      return ve;
    });
  }, [viewLen]);
  const seek = useCallback((m: number, opts: { center?: boolean } = {}) => {
    seekAbs(Date.now() - m * 60000, opts);
  }, [seekAbs]);

  // The active track's <video> publishing its clock. Reports far from
  // the current playhead are stale timeupdates racing a fresh seek —
  // drop them so the seek wins. Legit reports trail by ≤ one timeupdate
  // interval (≈250ms wall) × speed.
  const onTime = useCallback((ms: number) => {
    if (!playingRef.current) return;
    if (Math.abs(ms - absRef.current) > 2000 + 1000 * speedRef.current) return;
    lastDriveRef.current = Date.now();
    setAbsMs(ms);
  }, []);

  // Wall-clock ticker: rides the live edge, and advances through
  // coverage gaps / failed fetches when no video is driving. A driver
  // that is loading or stalled mid-play is BUFFERING, not a gap — hold
  // the clock so timeline and frames can't diverge (running ahead here
  // is what caused the repeated-frames/forced-seek loop on slow NAS
  // muxes); the video resumes driving when data arrives.
  useEffect(() => {
    if (!playing) return;
    let prev = Date.now();
    const t = window.setInterval(() => {
      const now = Date.now();
      const dt = now - prev;
      prev = now;
      setAbsMs((a) => {
        if (now - a < LIVE_EDGE_MS) return now; // live: follow the clock
        if (now - lastDriveRef.current < 1200) return a; // video is driving
        const st = driverMediaRef.current;
        if (st === 'loading' || st === 'playing') return a; // buffering — wait
        return Math.min(now, a + dt * speed); // gap / failed / no media
      });
    }, 500);
    return () => window.clearInterval(t);
  }, [playing, speed]);

  // The driver's media ran out before the requested chunk length — the
  // server returns only what exists (always the case near the live
  // edge). Jump the clock to the end of the returned media (the sender
  // already floors it past the chunk start, so a sliver response can't
  // replay the same frames in a loop); catching up to "now" hands off
  // to live.
  const onMediaEnded = useCallback((endMs: number) => {
    if (!playingRef.current) return;
    const now = Date.now();
    lastDriveRef.current = 0;
    if (now - endMs < CATCHUP_LIVE_MS) {
      setAbsMs(now); // caught up — go live
      return;
    }
    setAbsMs((a) => Math.max(a, endMs));
  }, []);

  // Playing into a gap: skip ahead to the next recorded range on the
  // active track rather than crawling through dead air.
  useEffect(() => {
    if (!playing || live) return;
    const entries = coverage[activeId];
    if (!entries || entries.length === 0) return;
    if (!inGap(activeId, pos)) return;
    const t = window.setTimeout(() => {
      let next = Infinity;
      for (const e of entries) {
        const s = new Date(e.start).getTime();
        if (s > absRef.current && s < next) next = s;
      }
      seekAbs(next === Infinity ? Date.now() : next + 250);
    }, 400);
    return () => window.clearTimeout(t);
  }, [playing, live, pos, activeId, coverage, inGap, seekAbs]);

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
    const a = absRef.current;
    const e = camEvents.find((x) => new Date(x.occurred_at).getTime() < a - 3000);
    if (e) seekAbs(new Date(e.occurred_at).getTime());
  };
  const nextEvent = () => {
    const a = absRef.current;
    const e = [...camEvents].reverse().find((x) => new Date(x.occurred_at).getTime() > a + 3000);
    if (e) seekAbs(new Date(e.occurred_at).getTime());
  };
  const step = (secs: number) => seekAbs(absRef.current + secs * 1000);
  const setIn = () => {
    const a = absRef.current;
    setInOut((r) => ({ startMs: a, endMs: r.endMs != null && r.endMs > a ? r.endMs : null }));
  };
  const setOut = () => {
    const a = absRef.current;
    setInOut((r) => ({ endMs: a, startMs: r.startMs != null && r.startMs < a ? r.startMs : null }));
  };

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
        if (inOutRef.current.startMs == null || inOutRef.current.endMs != null) { setInOut({ startMs: null, endMs: null }); setIn(); }
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

  const currentEvent = camEvents.find((e) => Math.abs(new Date(e.occurred_at).getTime() - absMs) < 12_000);
  const dayLabel = fmtDate(phTime) === fmtDate(data.now) ? 'Today' : fmtDate(phTime);
  // Driver is loading a chunk, or claims to play but hasn't published a
  // clock tick recently (stalled decoder) — surface it as buffering.
  const buffering = playing && !live && !inGap(activeId, pos) &&
    (driverMedia === 'loading' ||
      (driverMedia === 'playing' && data.now.getTime() - lastDriveRef.current > 1500));
  const rangeSecs = inOut.startMs != null && inOut.endMs != null
    ? Math.round((inOut.endMs - inOut.startMs) / 1000)
    : null;

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
            absMs={absMs}
            pos={pos}
            live={live}
            playing={playing}
            speed={speed}
            phTime={phTime}
            inGap={inGap}
            trackEvents={trackEvents}
            onTime={onTime}
            onMediaState={onMediaState}
            onMediaEnded={onMediaEnded}
            buffering={buffering}
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
            {dayLabel.toUpperCase()}{live ? ' · LIVE' : ` · −${fmtDelta(pos)}`}
          </span>
        </button>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, justifySelf: 'end' }}>
          <ClipMarker inOut={inOut} onStart={setIn} onEnd={setOut} onClear={() => setInOut({ startMs: null, endMs: null })} rangeSecs={rangeSecs} />
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
        <ExportModal
          cam={cam}
          phTime={phTime}
          range={inOut.startMs != null && inOut.endMs != null ? { startMs: inOut.startMs, endMs: inOut.endMs } : null}
          onClose={() => setExporting(false)}
        />
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
  // Same-origin proxy to the playback server (:9996 is plain HTTP and
  // often unpublished; the console itself is HTTPS). <video src> can't
  // send headers; the token rides the query string (the proxy promotes
  // it). Each chunk is rebuilt with the current token, so a long
  // scrubbing session never loses auth.
  const t = getStoredToken();
  if (t) qs.set('access_token', t);
  return `/playback/get?${qs}`;
}

function PlaybackVideo({ path, absMs, live, playing, speed, gap, driver, onTime, onMediaState, onMediaEnded }: {
  path: string;
  absMs: number; // absolute playhead (epoch ms)
  live: boolean;
  playing: boolean;
  speed: number;
  gap: boolean;
  // The active track drives the shared playhead clock from its <video>.
  driver: boolean;
  onTime: (ms: number) => void;
  onMediaState: (s: DriverMediaState) => void;
  onMediaEnded: (endMs: number) => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [chunk, setChunk] = useState<{ startMs: number; url: string } | null>(null);
  const [failed, setFailed] = useState(false);
  // Only the driver reports media state to the shared clock.
  const report = (s: DriverMediaState) => { if (driver) onMediaState(s); };

  // No recorded media mounted (live / gap / driver handoff) → 'none' so
  // the clock may free-run; also resets on unmount.
  useEffect(() => {
    if (!driver) return;
    if (live || gap) onMediaState('none');
    return () => onMediaState('none');
  }, [driver, live, gap, onMediaState]);

  // (Re)load a chunk when the playhead falls outside the current one.
  useEffect(() => {
    if (live || gap) return;
    const offset = chunk ? (absMs - chunk.startMs) / 1000 : -1;
    if (chunk && offset >= 0 && offset < CHUNK_SECS - 2) return;
    setFailed(false);
    setChunk({ startMs: absMs, url: playbackURL(path, absMs) });
    if (driver) onMediaState('loading');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [absMs, path, live, gap, chunk]);

  // A failed chunk (transient mux error, expired token refreshed on the
  // next build) retries from the current playhead after a beat.
  useEffect(() => {
    if (!failed) return;
    const t = window.setTimeout(() => { setFailed(false); setChunk(null); }, 3000);
    return () => window.clearTimeout(t);
  }, [failed]);

  // Driver publishes its clock: the shared playhead follows the actual
  // decoded position, so timeline and footage cannot drift apart.
  useEffect(() => {
    const v = videoRef.current;
    if (!v || !driver || !chunk || live || gap) return;
    const startMs = chunk.startMs;
    const h = () => { if (!v.seeking) onTime(startMs + v.currentTime * 1000); };
    v.addEventListener('timeupdate', h);
    return () => v.removeEventListener('timeupdate', h);
  }, [chunk, driver, live, gap, onTime]);

  // Keep the element aligned with the shared playhead. The driver only
  // corrects gross jumps (external seeks); followers track tightly.
  // Never correct a video that hasn't buffered to the target yet
  // (readyState < 2) — seeking an unbuffered, non-range stream just
  // forces a re-stall.
  useEffect(() => {
    const v = videoRef.current;
    if (!v || live || gap || !chunk) return;
    const want = (absMs - chunk.startMs) / 1000;
    const tolerance = driver && playing ? 1.5 : 0.35;
    if (v.readyState >= 2 && Number.isFinite(v.currentTime) && Math.abs(v.currentTime - want) > tolerance) {
      try { v.currentTime = Math.max(0, want); } catch { /* not seekable yet */ }
    }
    v.playbackRate = speed;
    if (playing && v.paused) v.play().catch(() => {});
    if (!playing && !v.paused) v.pause();
  }, [absMs, playing, speed, chunk, live, gap, driver]);

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
        preload="auto"
        onWaiting={() => report('loading')}
        onPlaying={() => report('playing')}
        onError={() => { setFailed(true); report('failed'); }}
        onEnded={(e) => {
          // Media ran out before CHUNK_SECS — the server only returns
          // what exists. Tell the clock where the media actually ended
          // (it jumps forward / goes live), then fetch from there. The
          // 1.5s floor past the chunk start guarantees the next fetch
          // moves forward even on a sliver/empty response — without it
          // a short response replays the same frames in a loop.
          const v = e.currentTarget;
          const playedSecs = Number.isFinite(v.duration) ? v.duration : v.currentTime;
          if (driver && chunk) onMediaEnded(chunk.startMs + Math.max(1500, playedSecs * 1000));
          setChunk(null);
        }}
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

function SyncedGrid({ tracks, activeId, setActiveId, absMs, pos, live, playing, speed, phTime, inGap, trackEvents, onTime, onMediaState, onMediaEnded, buffering }: {
  tracks: string[];
  activeId: string;
  setActiveId: (id: string) => void;
  absMs: number;
  pos: number;
  live: boolean;
  playing: boolean;
  speed: number;
  phTime: Date;
  inGap: (tid: string, m: number) => boolean;
  trackEvents: Record<string, EventRecord[]>;
  onTime: (ms: number) => void;
  onMediaState: (s: DriverMediaState) => void;
  onMediaEnded: (endMs: number) => void;
  buffering: boolean;
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
        const gap = !live && inGap(tid, pos);
        const ev = (trackEvents[tid] ?? []).find((e) => Math.abs(new Date(e.occurred_at).getTime() - absMs) < 12_000);
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
                media={
                  <PlaybackVideo
                    path={c.path}
                    absMs={absMs}
                    live={live}
                    playing={playing}
                    speed={speed}
                    gap={gap}
                    driver={isMain}
                    onTime={onTime}
                    onMediaState={onMediaState}
                    onMediaEnded={onMediaEnded}
                  />
                }
              />
              {/* playback-time corner overlay */}
              <div style={badge({ bottom: 10, right: 10, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums' })}>{fmtTime(phTime)}</div>
              {!live && <div style={badge({ top: 10, left: 10, color: 'var(--danger)' })}>PLAYBACK</div>}
              {gap && (
                <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }}>
                  <span style={{ padding: '4px 10px', background: 'rgba(10,10,10,0.75)', border: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>NO RECORDING</span>
                </div>
              )}
              {isMain && !playing && <div style={badge({ top: 44, left: 12, color: 'var(--warning)' })}>PAUSED</div>}
              {isMain && playing && buffering && <div style={badge({ top: 44, left: 12, color: 'var(--warning)' })}>BUFFERING</div>}
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
  const stage = inOut.startMs == null ? 0 : inOut.endMs == null ? 1 : 2;
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

  const inX = inOut.startMs != null ? xOf((nowMs - inOut.startMs) / 60000) : null;
  const outX = inOut.endMs != null ? xOf((nowMs - inOut.endMs) / 60000) : null;
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

function ExportModal({ cam, phTime, range, onClose }: {
  cam: ConsoleCamera;
  phTime: Date;
  range: { startMs: number; endMs: number } | null;
  onClose: () => void;
}) {
  const [before, setBefore] = useState(30);
  const [after, setAfter] = useState(30);
  const [state, setState] = useState<'idle' | 'working' | 'ready' | 'failed'>('idle');
  const [error, setError] = useState('');
  const [downloadURL, setDownloadURL] = useState('');

  const secs = range ? Math.round((range.endMs - range.startMs) / 1000) : before + after;
  const startT = range ? new Date(range.startMs) : new Date(phTime.getTime() - before * 1000);

  // A clip rides an event: for a marked range we drop a bookmark at the
  // start and export [0, range] around it; for a centered export we drop
  // the bookmark at the playhead and use before/after rolls.
  const start = async () => {
    setState('working');
    setError('');
    try {
      const anchor = range ? new Date(range.startMs) : phTime;
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
