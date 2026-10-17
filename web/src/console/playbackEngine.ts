// PlaybackEngine — MSE-backed recorded-video playback.
//
// The naive approach (<video src="/playback/get?...">) re-fetches a
// whole progressive stream on every seek and can't range-request
// (the playback server sends Accept-Ranges: none), so scrubbing costs
// a full NAS mux round-trip every time. This engine owns a MediaSource
// instead: it fetches small time-addressed fMP4 windows and appends
// them to a SourceBuffer it controls, so
//
//   - seeks inside buffered footage are instant (just currentTime),
//   - seeks outside cost exactly one small window fetch (~15s of
//     media, aborting any in-flight fetch),
//   - playback prefetches ahead of the playhead and evicts behind it,
//     so steady-state watching never stalls on chunk boundaries.
//
// Wire format: /playback/get?format=fmp4 returns an init segment
// (ftyp+moov) followed by ~1s moof/mdat parts whose tfdt base starts
// at ≈0 relative to the REQUESTED start (internal/playback/
// muxer_fmp4.go) — so a window fetched at wall-time W maps into the
// media timeline with timestampOffset = (W - epoch). The engine keeps
// one fixed epoch per instance; wall-clock ms <-> media seconds are
// linear.
//
// The engine reports wall-clock time per rendered frame
// (requestVideoFrameCallback where available), buffering state, and
// end-of-footage (a window that came back short or 404 — the server
// returns only what exists). Live handoff / coverage-gap policy stays
// in the caller.

import { getStoredToken } from '../lib/api';

export type EngineState = 'idle' | 'loading' | 'playing' | 'buffering' | 'failed';

export interface EngineCallbacks {
  onTime: (ms: number) => void;
  onState: (s: EngineState) => void;
  // Footage ran out at endMs (short window or no-segments response).
  onEndOfFootage: (endMs: number) => void;
  // MSE unusable for this stream (codec unsupported, fatal error) —
  // caller should fall back to the progressive <video src> path.
  onFatal: (reason: string) => void;
}

const WINDOW_SECS = 15; // per-fetch media window
const PREFETCH_AHEAD_SECS = 25; // keep at least this much buffered ahead
const EVICT_BEHIND_SECS = 45; // drop footage further behind than this
const MAX_FORWARD_GAP = 0.75; // buffered-range stitch tolerance (parts are ~1s)

function playbackWindowURL(path: string, startMs: number, durationSecs: number): string {
  const qs = new URLSearchParams({
    path,
    start: new Date(startMs).toISOString(),
    duration: String(durationSecs),
    format: 'fmp4',
  });
  const t = getStoredToken();
  if (t) qs.set('access_token', t);
  return `/playback/get?${qs}`;
}

/* ---------- init-segment codec sniffing ---------- */

// Minimal MP4 box walk: moov → trak → mdia → minf → stbl → stsd
// sample entries, mapped to RFC 6381 codec strings. Enough for what
// cameras actually produce (H.264 / H.265 video, AAC / opus audio).
function parseCodecs(buf: Uint8Array): string[] {
  const codecs: string[] = [];
  const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);

  const fourcc = (off: number) =>
    String.fromCharCode(buf[off], buf[off + 1], buf[off + 2], buf[off + 3]);

  const walk = (start: number, end: number, cb: (type: string, body: number, bodyEnd: number) => void) => {
    let off = start;
    while (off + 8 <= end) {
      let size = view.getUint32(off);
      const type = fourcc(off + 4);
      let header = 8;
      if (size === 1) {
        size = Number(view.getBigUint64(off + 8));
        header = 16;
      } else if (size === 0) {
        size = end - off;
      }
      if (size < header || off + size > end) return;
      cb(type, off + header, off + size);
      off += size;
    }
  };

  const hexByte = (b: number) => b.toString(16).padStart(2, '0').toUpperCase();

  const sampleEntry = (type: string, body: number, bodyEnd: number) => {
    if (type === 'avc1' || type === 'avc3') {
      // sample entry header: 78 bytes before child boxes
      walk(body + 70, bodyEnd, (t, b) => {
        if (t === 'avcC' && b + 4 <= bodyEnd) {
          codecs.push(`avc1.${hexByte(buf[b + 1])}${hexByte(buf[b + 2])}${hexByte(buf[b + 3])}`);
        }
      });
    } else if (type === 'hvc1' || type === 'hev1') {
      // Deriving the exact RFC 6381 HEVC string from hvcC is baroque;
      // Main-profile is what IP cameras ship, and browsers treat the
      // codecs param as a capability hint — decode config comes from
      // the init segment itself.
      codecs.push(`${type}.1.6.L123.B0`);
    } else if (type === 'mp4a') {
      codecs.push('mp4a.40.2');
    } else if (type === 'Opus' || type === 'opus') {
      codecs.push('opus');
    }
  };

  walk(0, buf.byteLength, (t1, b1, e1) => {
    if (t1 !== 'moov') return;
    walk(b1, e1, (t2, b2, e2) => {
      if (t2 !== 'trak') return;
      walk(b2, e2, (t3, b3, e3) => {
        if (t3 !== 'mdia') return;
        walk(b3, e3, (t4, b4, e4) => {
          if (t4 !== 'minf') return;
          walk(b4, e4, (t5, b5, e5) => {
            if (t5 !== 'stbl') return;
            walk(b5, e5, (t6, b6, e6) => {
              if (t6 !== 'stsd') return;
              // stsd payload: version/flags (4) + entry_count (4)
              walk(b6 + 8, e6, sampleEntry);
            });
          });
        });
      });
    });
  });

  return codecs;
}

export function mseSupported(): boolean {
  return typeof window !== 'undefined' &&
    typeof window.MediaSource !== 'undefined' &&
    MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"');
}

/* ---------- engine ---------- */

interface QueuedAppend {
  offsetSecs: number;
  bytes: Uint8Array;
}

export class PlaybackEngine {
  private video: HTMLVideoElement;
  private path: string;
  private cb: EngineCallbacks;
  private epochMs: number;

  private ms: MediaSource | null = null;
  private sb: SourceBuffer | null = null;
  private objectURL = '';
  private opened = false;

  private appendQueue: QueuedAppend[] = [];
  private fetchCtl: AbortController | null = null;
  private fetching = false;
  private destroyed = false;
  private rvfcHandle = 0;

  // Seek target that couldn't be applied yet (no metadata / target not
  // buffered at seek time). Applied on loadedmetadata/updateend — the
  // constructor seeks before MediaSource has metadata, where a
  // currentTime write is silently lost.
  private pendingSeekT: number | null = null;
  // End of known footage (media seconds), set when a window comes back
  // short; Infinity while footage keeps flowing.
  private footageEndT = Infinity;
  private endReported = false;
  // Highest wall-ms a window fetch has covered (next sequential fetch
  // starts here).
  private fetchedToMs = 0;

  constructor(video: HTMLVideoElement, path: string, startMs: number, cb: EngineCallbacks) {
    this.video = video;
    this.path = path;
    this.cb = cb;
    // Fixed epoch 8 days before the first position: every reachable
    // seek (the console's timeline spans 7 days) maps to a positive
    // media time, which stays small enough for precise float math.
    this.epochMs = startMs - 8 * 24 * 3600_000;

    this.ms = new MediaSource();
    this.objectURL = URL.createObjectURL(this.ms);
    this.ms.addEventListener('sourceopen', this.onSourceOpen);
    video.src = this.objectURL;

    video.addEventListener('waiting', this.onWaiting);
    video.addEventListener('playing', this.onPlaying);
    video.addEventListener('timeupdate', this.onTimeUpdate);
    video.addEventListener('loadedmetadata', this.onLoadedMetadata);
    this.armFrameCallback();

    this.cb.onState('loading');
    this.seek(startMs);
  }

  /* ----- public surface ----- */

  seek(ms: number): void {
    if (this.destroyed) return;
    const t = Math.max(0, this.toT(ms));
    this.endReported = false;
    if (this.isBuffered(t)) {
      this.pendingSeekT = null;
      this.video.currentTime = t;
      return;
    }
    // Outside the buffer: drop all in-flight work and start fresh at
    // the target so the first frame arrives as fast as possible.
    this.abortFetch();
    this.appendQueue = [];
    this.footageEndT = Infinity;
    this.fetchedToMs = ms + WINDOW_SECS * 1000;
    this.pendingSeekT = t;
    try { this.video.currentTime = t; } catch { /* applied after metadata */ }
    this.cb.onState('loading');
    void this.fetchWindow(ms, WINDOW_SECS);
  }

  positionMs(): number {
    return this.fromT(this.video.currentTime);
  }

  setRate(rate: number): void {
    this.video.playbackRate = rate;
  }

  setPlaying(playing: boolean): void {
    if (playing && this.video.paused) this.video.play().catch(() => {});
    if (!playing && !this.video.paused) this.video.pause();
  }

  destroy(): void {
    this.destroyed = true;
    this.abortFetch();
    this.appendQueue = [];
    this.video.removeEventListener('waiting', this.onWaiting);
    this.video.removeEventListener('playing', this.onPlaying);
    this.video.removeEventListener('timeupdate', this.onTimeUpdate);
    this.video.removeEventListener('loadedmetadata', this.onLoadedMetadata);
    if (this.rvfcHandle && 'cancelVideoFrameCallback' in this.video) {
      (this.video as HTMLVideoElement & { cancelVideoFrameCallback(h: number): void })
        .cancelVideoFrameCallback(this.rvfcHandle);
    }
    try {
      if (this.ms && this.ms.readyState === 'open') this.ms.endOfStream();
    } catch { /* already closed */ }
    this.video.removeAttribute('src');
    this.video.load();
    URL.revokeObjectURL(this.objectURL);
    this.ms = null;
    this.sb = null;
  }

  /* ----- time mapping ----- */

  private toT(ms: number): number {
    return (ms - this.epochMs) / 1000;
  }

  private fromT(t: number): number {
    return this.epochMs + t * 1000;
  }

  /* ----- MediaSource lifecycle ----- */

  private onSourceOpen = () => {
    if (!this.ms || this.opened) return;
    this.opened = true;
    // A very long duration lets currentTime be set anywhere up front;
    // real playable extent is whatever ends up buffered.
    try { this.ms.duration = 14 * 24 * 3600; } catch { /* non-fatal */ }
    this.pump();
  };

  private ensureSourceBuffer(initBytes: Uint8Array): boolean {
    if (this.sb || !this.ms) return this.sb != null;
    const codecs = parseCodecs(initBytes);
    const mime = `video/mp4; codecs="${codecs.length ? codecs.join(',') : 'avc1.42E01E'}"`;
    if (!MediaSource.isTypeSupported(mime)) {
      this.cb.onFatal(`unsupported codecs: ${mime}`);
      return false;
    }
    try {
      this.sb = this.ms.addSourceBuffer(mime);
    } catch (e) {
      this.cb.onFatal(`addSourceBuffer failed: ${(e as Error).message}`);
      return false;
    }
    this.sb.mode = 'segments';
    this.sb.addEventListener('updateend', () => {
      this.applyPendingSeek();
      this.evict();
      this.pump();
      this.maybeEndOfFootage();
      this.ensureAhead();
    });
    this.sb.addEventListener('error', () => {
      this.cb.onFatal('sourcebuffer error');
    });
    return true;
  }

  /* ----- fetching ----- */

  private abortFetch(): void {
    if (this.fetchCtl) {
      this.fetchCtl.abort();
      this.fetchCtl = null;
    }
    this.fetching = false;
  }

  private async fetchWindow(startMs: number, durationSecs: number): Promise<void> {
    if (this.destroyed || this.fetching) return;
    this.fetching = true;
    const ctl = new AbortController();
    this.fetchCtl = ctl;
    try {
      const res = await fetch(playbackWindowURL(this.path, startMs, durationSecs), { signal: ctl.signal });
      if (res.status === 404 || res.status === 400) {
        // No segments at/after this instant — footage ends here.
        this.footageEndT = Math.min(this.footageEndT, this.toT(startMs));
        this.fetching = false;
        this.fetchCtl = null;
        this.maybeEndOfFootage(true);
        return;
      }
      if (!res.ok) throw new Error(`playback fetch ${res.status}`);
      const bytes = new Uint8Array(await res.arrayBuffer());
      if (ctl.signal.aborted || this.destroyed) return;
      this.fetching = false;
      this.fetchCtl = null;
      if (bytes.byteLength < 200) {
        // Effectively empty response — treat as end of footage.
        this.footageEndT = Math.min(this.footageEndT, this.toT(startMs));
        this.maybeEndOfFootage(true);
        return;
      }
      this.appendQueue.push({ offsetSecs: this.toT(startMs), bytes });
      this.pump();
    } catch (e) {
      if (ctl.signal.aborted || this.destroyed) return;
      this.fetching = false;
      this.fetchCtl = null;
      this.cb.onState('failed');
      // Transient server/mux failure: retry from the playhead after a
      // beat rather than wedging.
      window.setTimeout(() => {
        if (!this.destroyed) this.seek(this.fromT(this.video.currentTime));
      }, 3000);
      void e;
    }
  }

  /* ----- append pipeline ----- */

  private pump(): void {
    if (this.destroyed || !this.opened || this.appendQueue.length === 0) return;
    const next = this.appendQueue[0];
    if (!this.ensureSourceBuffer(next.bytes)) {
      this.appendQueue = [];
      return;
    }
    const sb = this.sb;
    if (!sb || sb.updating) return;
    this.appendQueue.shift();
    try {
      sb.timestampOffset = next.offsetSecs;
      sb.appendBuffer(next.bytes as BufferSource);
    } catch (e) {
      if ((e as Error).name === 'QuotaExceededError') {
        // Evict hard and re-queue.
        this.appendQueue.unshift(next);
        this.evict(true);
      } else {
        this.cb.onFatal(`append failed: ${(e as Error).message}`);
      }
    }
  }

  private evict(hard = false): void {
    const sb = this.sb;
    if (!sb || sb.updating) return;
    const behind = hard ? 10 : EVICT_BEHIND_SECS;
    const cut = this.video.currentTime - behind;
    try {
      if (cut > 0 && sb.buffered.length > 0 && sb.buffered.start(0) < cut) {
        sb.remove(0, cut);
      }
    } catch { /* retried on next updateend */ }
  }

  /* ----- steady-state prefetch ---- */

  private bufferedEndAt(t: number): number | null {
    const sb = this.sb;
    if (!sb) return null;
    try {
      const b = sb.buffered;
      for (let i = 0; i < b.length; i++) {
        if (t >= b.start(i) - MAX_FORWARD_GAP && t <= b.end(i) + MAX_FORWARD_GAP) return b.end(i);
      }
    } catch { /* detached */ }
    return null;
  }

  private isBuffered(t: number): boolean {
    const end = this.bufferedEndAt(t);
    return end != null && end - t > 0.05;
  }

  private ensureAhead(): void {
    if (this.destroyed || this.fetching || this.appendQueue.length > 0) return;
    const t = this.video.currentTime;
    const end = this.bufferedEndAt(t);
    if (end == null) return;
    if (end >= this.footageEndT - 0.5) return; // nothing more to fetch
    const aheadWanted = PREFETCH_AHEAD_SECS * Math.max(1, this.video.playbackRate);
    if (end - t >= aheadWanted) return;
    // Continue from wherever coverage actually reaches (the server may
    // have returned less than requested).
    const nextMs = Math.max(this.fromT(end), this.fetchedToMs - 2000);
    this.fetchedToMs = nextMs + WINDOW_SECS * 1000;
    void this.fetchWindow(nextMs, WINDOW_SECS);
  }

  // A window resolved to less media than requested → footage ends
  // where the buffer does (near the live edge this is the normal
  // case: the server returns only what exists so far).
  private maybeEndOfFootage(force = false): void {
    if (this.destroyed || this.endReported) return;
    const t = this.video.currentTime;
    const end = this.bufferedEndAt(t);
    const atEnd =
      (end != null && this.footageEndT !== Infinity && end >= this.footageEndT - 1 && t >= end - 0.25) ||
      (force && end == null);
    if (atEnd) {
      this.endReported = true;
      this.cb.onEndOfFootage(this.fromT(end ?? this.footageEndT));
    }
  }

  private applyPendingSeek(): void {
    if (this.pendingSeekT == null) return;
    const t = this.pendingSeekT;
    if (this.isBuffered(t)) {
      this.pendingSeekT = null;
      try { this.video.currentTime = t; } catch { /* retried on next updateend */ }
    }
  }

  /* ----- state / clock reporting ----- */

  private onLoadedMetadata = () => {
    if (!this.destroyed) this.applyPendingSeek();
  };

  private onWaiting = () => {
    if (this.destroyed) return;
    this.cb.onState(this.fetching || this.appendQueue.length > 0 ? 'loading' : 'buffering');
    this.ensureAhead();
    this.maybeEndOfFootage();
  };

  private onPlaying = () => {
    if (!this.destroyed) this.cb.onState('playing');
  };

  private onTimeUpdate = () => {
    if (this.destroyed) return;
    if (!this.video.seeking) this.cb.onTime(this.fromT(this.video.currentTime));
    this.ensureAhead();
    this.maybeEndOfFootage();
  };

  private armFrameCallback(): void {
    const v = this.video as HTMLVideoElement & {
      requestVideoFrameCallback?: (cb: (now: number, meta: { mediaTime: number }) => void) => number;
    };
    if (!v.requestVideoFrameCallback) return; // timeupdate fallback is wired
    const tick = (_: number, meta: { mediaTime: number }) => {
      if (this.destroyed) return;
      this.cb.onTime(this.fromT(meta.mediaTime));
      this.rvfcHandle = v.requestVideoFrameCallback!(tick);
    };
    this.rvfcHandle = v.requestVideoFrameCallback(tick);
  }
}
