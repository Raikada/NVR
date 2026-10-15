// CameraTile — feed tile with amber corner brackets and overlays,
// backed by the recorder's real HLS output (hls.js / native). Grid
// tiles prefer the low-res "<name>_sub" path when the camera's probe
// resolved a sub-stream; focus/large tiles pull the main profile.
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import Hls from 'hls.js';
import { Icon } from '../components/Icon';
import { Brackets, StatusBadge } from '../components/ui';
import { ptzHome, ptzMove, ptzStop } from '../lib/api';
import { fmtTime } from '../lib/format';
import { useConsole, type ConsoleCamera } from '../lib/console';

type PlayerState = 'loading' | 'playing' | 'unavailable' | 'error';

// LiveVideo — bare <video> with hls.js lifecycle, no chrome. The tile
// provides every overlay so the design stays pixel-consistent.
export function LiveVideo({ path, cover, onState, videoRef: extRef }: {
  path: string;
  cover?: boolean;
  onState?: (s: PlayerState) => void;
  videoRef?: React.RefObject<HTMLVideoElement>;
}) {
  const ownRef = useRef<HTMLVideoElement>(null);
  const videoRef = extRef ?? ownRef;

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !path) return;
    // Same-origin proxy: the API reverse-proxies /hls/* to the HLS
    // listener, so HTTPS consoles avoid mixed content and the session
    // cookie authenticates the stream.
    const url = `/hls/${encodeURIComponent(path)}/index.m3u8`;
    let cancelled = false;
    let hls: Hls | null = null;
    onState?.('loading');

    if (Hls.isSupported()) {
      hls = new Hls({
        xhrSetup: (xhr) => { xhr.withCredentials = true; },
        liveSyncDurationCount: 3,
        liveMaxLatencyDurationCount: 6,
      });
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        if (cancelled) return;
        onState?.('playing');
        video.play().catch(() => {});
      });
      hls.on(Hls.Events.ERROR, (_, d) => {
        if (cancelled || !d.fatal) return;
        onState?.(d.response?.code === 404 ? 'unavailable' : 'error');
      });
      hls.loadSource(url);
      hls.attachMedia(video);
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      const onLoaded = () => { if (!cancelled) onState?.('playing'); };
      const onError = () => { if (!cancelled) onState?.('error'); };
      video.addEventListener('loadedmetadata', onLoaded);
      video.addEventListener('error', onError);
      video.src = url;
      video.play().catch(() => {});
      return () => {
        cancelled = true;
        video.removeEventListener('loadedmetadata', onLoaded);
        video.removeEventListener('error', onError);
        video.removeAttribute('src');
        video.load();
      };
    } else {
      onState?.('error');
    }

    return () => {
      cancelled = true;
      if (hls) hls.destroy();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  return (
    <video
      ref={videoRef}
      muted
      playsInline
      autoPlay
      style={{
        position: 'absolute', inset: 0, width: '100%', height: '100%',
        objectFit: cover ? 'cover' : 'contain', background: '#000',
      }}
    />
  );
}

export function CameraTile({
  camera, focused, onFocus, large, fill, dim, hideOverlays, hideName,
  showCornerTimecode, ptzControls, media, motion, videoRef,
}: {
  camera: ConsoleCamera;
  focused?: boolean;
  onFocus?: () => void;
  large?: boolean;
  fill?: boolean;
  dim?: boolean;
  hideOverlays?: boolean;
  hideName?: boolean;
  showCornerTimecode?: boolean;
  ptzControls?: boolean;
  // Optional media layer override (Playback supplies its own video).
  media?: ReactNode;
  motion?: boolean;
  videoRef?: React.RefObject<HTMLVideoElement>;
}) {
  const data = useConsole();
  const offline = camera.status === 'offline';
  const degraded = camera.status === 'degraded';
  const [hover, setHover] = useState(false);
  const [player, setPlayer] = useState<PlayerState>('loading');

  // Grid (non-large) tiles ride the sub-stream when the probe found one.
  const path = !large && camera.hasSub ? `${camera.name}_sub` : camera.name;
  const ts = fmtTime(data.now);

  return (
    <div
      onClick={onFocus}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        position: 'relative', background: '#07060a', overflow: 'hidden',
        ...(fill ? { width: '100%', height: '100%' } : { aspectRatio: '16 / 9' }),
        cursor: onFocus ? 'pointer' : 'default',
        outline: focused ? '1px solid var(--accent-40)' : 'none',
        opacity: dim ? 0.55 : 1,
      }}
    >
      {media !== undefined ? media : !offline && (
        <LiveVideo path={path} cover={!large} onState={setPlayer} videoRef={videoRef} />
      )}
      {/* scanline texture over the feed */}
      <div style={{ position: 'absolute', inset: 0, pointerEvents: 'none', background: 'repeating-linear-gradient(0deg, rgba(255,255,255,0.018) 0 1px, transparent 1px 3px)' }} />
      {!hideOverlays && (
        <>
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, height: 44, background: 'linear-gradient(180deg, rgba(10,10,10,0.85), transparent)', pointerEvents: 'none' }} />
          <div style={{ position: 'absolute', left: 0, right: 0, bottom: 0, height: 44, background: 'linear-gradient(0deg, rgba(10,10,10,0.85), transparent)', pointerEvents: 'none' }} />
        </>
      )}
      <Brackets />
      {offline && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column', gap: 8, background: 'rgba(10,10,10,0.5)' }}>
          <Icon name="wifi-off" size={28} color="var(--danger)" />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 1, color: 'var(--danger)' }}>OFFLINE · RETRYING</span>
        </div>
      )}
      {!offline && media === undefined && player !== 'playing' && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }}>
          <span style={{ padding: '4px 10px', background: 'rgba(10,10,10,0.6)', border: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>
            {player === 'loading' ? 'CONNECTING…' : player === 'unavailable' ? 'NO STREAM' : 'STREAM ERROR'}
          </span>
        </div>
      )}
      {!hideOverlays && (
        <>
          <div style={{ position: 'absolute', top: 10, left: 10, display: 'flex', gap: 6 }}>
            {!offline && !degraded && <StatusBadge kind="live" />}
            {offline && <StatusBadge kind="offline" />}
            {degraded && <StatusBadge kind="degraded" />}
            {camera.hasPTZ && !offline && (
              <span style={{ padding: '3px 7px', borderRadius: 3, fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '.5px', background: 'rgba(10,10,10,0.6)', border: '1px solid var(--accent-27)', color: 'var(--accent)' }}>PTZ</span>
            )}
          </div>
          <div style={{ position: 'absolute', top: 10, right: 10, display: 'flex', gap: 6 }}>
            {motion && !offline && <StatusBadge kind="motion" />}
            {camera.recording && !offline && <StatusBadge kind="rec" />}
          </div>
          {!hideName && (
            <div style={{ position: 'absolute', bottom: 10, left: 10, fontFamily: 'var(--font-sans)', fontSize: large ? 14 : 12, fontWeight: 500, color: 'var(--on-video)' }}>
              {camera.name}
            </div>
          )}
          {!offline && (
            <div style={{ position: 'absolute', bottom: 10, right: 10, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--on-video)', display: 'flex', gap: 10, fontVariantNumeric: 'tabular-nums' }}>
              <span>{camera.host}</span>
              {(large || showCornerTimecode) && <span>{ts}</span>}
            </div>
          )}
        </>
      )}
      {ptzControls && camera.hasPTZ && !offline && hover && <PtzOverlay camId={camera.id} />}
    </div>
  );
}

/* ---------- In-tile PTZ (hover) — continuous move: hold to pan ---------- */

const ptzMini: CSSProperties = {
  width: 26, height: 26, borderRadius: 4, background: 'rgba(10,10,10,0.7)',
  border: '1px solid var(--border)', color: 'var(--text-primary)', cursor: 'pointer',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
};

// usePtzHold — fires a continuous-move on press, stop on release.
export function usePtzHold(camId: string) {
  const activeRef = useRef(false);
  const start = (pan: number, tilt: number, zoom = 0) => {
    activeRef.current = true;
    void ptzMove(camId, pan, tilt, zoom).catch(() => {});
  };
  const stop = () => {
    if (!activeRef.current) return;
    activeRef.current = false;
    void ptzStop(camId).catch(() => {});
  };
  useEffect(() => () => {
    if (activeRef.current) void ptzStop(camId).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [camId]);
  return { start, stop };
}

function PtzOverlay({ camId }: { camId: string }) {
  const { start, stop } = usePtzHold(camId);
  const arrow = (icon: 'chevron-up' | 'chevron-down' | 'chevron-left' | 'chevron-right', pos: CSSProperties, pan: number, tilt: number) => (
    <button
      onMouseDown={(e) => { e.stopPropagation(); start(pan, tilt); }}
      onMouseUp={(e) => { e.stopPropagation(); stop(); }}
      onMouseLeave={stop}
      onClick={(e) => e.stopPropagation()}
      style={{ position: 'absolute', ...pos, width: 30, height: 30, borderRadius: 4, background: 'rgba(10,10,10,0.7)', border: '1px solid var(--accent-27)', color: 'var(--accent)', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', backdropFilter: 'blur(2px)' }}
    >
      <Icon name={icon} size={14} />
    </button>
  );
  return (
    <div onClick={(e) => e.stopPropagation()} style={{ position: 'absolute', inset: 0, animation: 'fadein 150ms ease-out' }}>
      {arrow('chevron-up', { top: 44, left: '50%', transform: 'translateX(-50%)' }, 0, 0.5)}
      {arrow('chevron-down', { bottom: 44, left: '50%', transform: 'translateX(-50%)' }, 0, -0.5)}
      {arrow('chevron-left', { left: 12, top: '50%', transform: 'translateY(-50%)' }, -0.5, 0)}
      {arrow('chevron-right', { right: 12, top: '50%', transform: 'translateY(-50%)' }, 0.5, 0)}
      <div style={{ position: 'absolute', right: 12, bottom: 44, display: 'flex', flexDirection: 'column', gap: 4 }}>
        <button style={ptzMini} onMouseDown={(e) => { e.stopPropagation(); start(0, 0, 0.5); }} onMouseUp={stop} onMouseLeave={stop}>
          <Icon name="plus" size={12} />
        </button>
        <button style={ptzMini} onMouseDown={(e) => { e.stopPropagation(); start(0, 0, -0.5); }} onMouseUp={stop} onMouseLeave={stop}>
          <Icon name="minus" size={12} />
        </button>
        <button style={ptzMini} title="Home" onClick={(e) => { e.stopPropagation(); void ptzHome(camId).catch(() => {}); }}>
          <Icon name="home" size={12} />
        </button>
      </div>
    </div>
  );
}

export function EmptyTile({ onClick, label = 'EMPTY SLOT', fill }: {
  onClick?: () => void; label?: string; fill?: boolean;
}) {
  return (
    <div onClick={onClick} style={{
      ...(fill ? { width: '100%', height: '100%' } : { aspectRatio: '16 / 9' }),
      border: '1px dashed var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'center',
      flexDirection: 'column', gap: 8, color: 'var(--text-muted)', cursor: onClick ? 'pointer' : 'default',
    }}>
      <Icon name="plus" size={22} />
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, textTransform: 'uppercase' }}>{label}</span>
    </div>
  );
}
