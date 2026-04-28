// Live HLS preview tile. Replaces the design-port placeholder with a
// real <video> element backed by hls.js (Chrome / Firefox / Edge) or
// native HLS (Safari / iOS). Falls back to the original placeholder
// aesthetic — gradient background, video icon, scanline — when the
// camera is offline, the stream isn't published, or the player errors.
//
// URL: <protocol>//<hostname>:8888/<camera-name>/index.m3u8. Per
// recorder ADR 0009, Camera.name is the recorder's path-name (the key
// in mediamtx.yml's paths map), so no extra lookup is needed.
//
// Auth: SPA hits the recorder's API listener (default-open Control
// API) without credentials today; the HLS server inherits the same
// auth.Manager config (default-open). withCredentials is set on
// hls.js's xhr so the cookieCheck redirect works when credentials
// are required.

import { useEffect, useRef, useState } from 'react';
import Hls from 'hls.js';
import { Icon } from './Icon';
import { Brackets } from './primitives';

type PlayerState =
  | 'idle' // not started yet (manualStart variants)
  | 'loading' // hls.js attached, waiting for manifest
  | 'playing' // first manifest parsed; video element should be live
  | 'unavailable' // path 404 / no stream published
  | 'error' // unrecoverable hls.js error
  | 'offline'; // camera.runtime.online === false

interface HLSPreviewProps {
  cameraName: string;
  online: boolean;
  // When true, renders the click-to-play placeholder until the user
  // taps Play. Used for the camera grid to avoid 6 concurrent HLS
  // sessions firing the moment the page loads.
  manualStart?: boolean;
  // Top-left corner label, e.g. "● LIVE · MAIN". Defaults to "● LIVE".
  liveLabel?: string;
  // Bottom-left corner overlay, e.g. "1920×1080 · H.264 · 30fps".
  meta?: string;
  // Inline style passthrough — caller controls width / aspect-ratio /
  // positioning. The component supplies the inner gradient, video,
  // and overlay layers.
  style?: React.CSSProperties;
}

export function HLSPreview({
  cameraName,
  online,
  manualStart = false,
  liveLabel = '● LIVE',
  meta,
  style,
}: HLSPreviewProps) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  // `active` decouples "should the player be running?" from the
  // player's own loading/playing/error sub-state. manualStart=true
  // starts inactive; clicking PLAY flips active=true and the effect
  // begins. autoplay variants (drawer, wizard) start active=true.
  const [active, setActive] = useState(!manualStart);
  const [state, setState] = useState<PlayerState>(
    !online ? 'offline' : !active ? 'idle' : 'loading',
  );

  useEffect(() => {
    if (!online) {
      setState('offline');
      return;
    }
    if (!active) {
      setState('idle');
      return;
    }
    if (!cameraName) return;

    setState('loading');
    const video = videoRef.current;
    if (!video) return;

    const url = `${window.location.protocol}//${window.location.hostname}:8888/${encodeURIComponent(cameraName)}/index.m3u8`;
    let cancelled = false;

    if (Hls.isSupported()) {
      // hls.js path (Chrome, Firefox, Edge, modern non-Safari).
      const hls = new Hls({
        // Cookies must round-trip so the HLS server's cookieCheck
        // redirect resolves cleanly.
        xhrSetup: (xhr) => {
          xhr.withCredentials = true;
        },
        // Live tuning. The recorder's HLS muxer emits ~2-6s segments
        // depending on GOP; keeping a 3-segment sync buffer balances
        // latency against rebuffer risk on a typical LAN.
        liveSyncDurationCount: 3,
        liveMaxLatencyDurationCount: 6,
      });
      hlsRef.current = hls;

      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        if (cancelled) return;
        setState('playing');
        // muted + playsInline lets autoplay through on most browsers.
        video.play().catch(() => {
          /* autoplay blocked; the muted poster is fine */
        });
      });

      hls.on(Hls.Events.ERROR, (_, data) => {
        if (cancelled) return;
        if (!data.fatal) return;
        // 404 on the playlist means the path isn't publishing yet —
        // common for a camera that's online (config exists) but
        // hasn't connected upstream, or for the simulated cameras in
        // the manual-add wizard. Surface that distinctly from a
        // genuine player error.
        const code = data.response?.code;
        if (code === 404) {
          setState('unavailable');
        } else {
          setState('error');
        }
      });

      hls.loadSource(url);
      hls.attachMedia(video);
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      // Safari / iOS: native HLS, no library needed.
      const onLoaded = () => {
        if (!cancelled) setState('playing');
      };
      const onError = () => {
        if (!cancelled) setState('error');
      };
      video.addEventListener('loadedmetadata', onLoaded);
      video.addEventListener('error', onError);
      video.src = url;
      video.play().catch(() => {
        /* autoplay blocked */
      });

      return () => {
        cancelled = true;
        video.removeEventListener('loadedmetadata', onLoaded);
        video.removeEventListener('error', onError);
        video.removeAttribute('src');
        video.load();
      };
    } else {
      setState('error');
    }

    return () => {
      cancelled = true;
      if (hlsRef.current) {
        hlsRef.current.destroy();
        hlsRef.current = null;
      }
    };
  }, [cameraName, online, active]);

  const wrapperStyle: React.CSSProperties = {
    position: 'relative',
    aspectRatio: '16/9',
    background: 'linear-gradient(135deg, #1a1410, #0d0c0a)',
    overflow: 'hidden',
    ...style,
  };

  const showVideo = state === 'loading' || state === 'playing';
  const showFallback = !showVideo;

  return (
    <div style={wrapperStyle}>
      <Brackets />
      {showVideo && (
        <video
          ref={videoRef}
          muted
          playsInline
          autoPlay
          style={{
            position: 'absolute',
            inset: 0,
            width: '100%',
            height: '100%',
            objectFit: 'contain',
            background: '#000',
          }}
        />
      )}
      {showFallback && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          {state === 'idle' ? (
            <PlayButton onClick={() => setActive(true)} />
          ) : (
            <Icon
              name="video"
              style={{ width: 28, height: 28, color: 'rgba(249,115,22,0.4)' }}
            />
          )}
        </div>
      )}
      {/* Top-left status / live label */}
      <div
        style={{
          position: 'absolute',
          top: 6,
          left: 8,
          fontFamily: 'var(--font-mono)',
          fontSize: 9,
          letterSpacing: 1,
          color: state === 'playing' ? '#F97316' : 'rgba(249,115,22,0.55)',
        }}
      >
        {labelFor(state, liveLabel)}
      </div>
      {/* Bottom-left meta — only shown when actively playing or
          when caller explicitly supplied one (so the placeholder
          can still render its codec/resolution hint). */}
      {meta && (
        <div
          style={{
            position: 'absolute',
            bottom: 6,
            left: 8,
            fontFamily: 'var(--font-mono)',
            fontSize: 9,
            color: 'rgba(255,255,255,0.7)',
          }}
        >
          {meta}
        </div>
      )}
      {/* Animated scanline — preserves the original aesthetic on
          both the live video and the fallback. */}
      <div
        className="scanline"
        style={{
          position: 'absolute',
          top: 0,
          bottom: 0,
          width: 2,
          background: 'linear-gradient(to bottom, transparent, rgba(249,115,22,0.6), transparent)',
        }}
      />
    </div>
  );
}

function labelFor(state: PlayerState, liveLabel: string): string {
  switch (state) {
    case 'playing':
      return liveLabel;
    case 'loading':
      return '◌ CONNECTING';
    case 'unavailable':
      return '○ NO STREAM';
    case 'error':
      return '○ STREAM ERROR';
    case 'offline':
      return '○ OFFLINE';
    case 'idle':
      return '○ TAP TO PLAY';
  }
}

function PlayButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      type="button"
      style={{
        background: 'rgba(249,115,22,0.12)',
        border: '1px solid rgba(249,115,22,0.4)',
        borderRadius: 3,
        padding: '6px 14px',
        cursor: 'pointer',
        fontFamily: 'var(--font-mono)',
        fontSize: 10,
        letterSpacing: 1,
        color: '#F97316',
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
      }}
    >
      <Icon name="video" style={{ width: 12, height: 12 }} />
      PLAY
    </button>
  );
}
