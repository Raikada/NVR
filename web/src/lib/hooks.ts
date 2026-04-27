// Lightweight data-fetching hooks for the SPA's /v1/ wire-up. No
// react-query / swr — the call surface is small enough that a typed
// useFetch + usePoll cover every screen, and avoiding a third-party
// dependency keeps the bundle tight.

import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from './api';

export type FetchState<T> =
  | { status: 'loading'; data: undefined; error: undefined }
  | { status: 'ready'; data: T; error: undefined }
  | { status: 'error'; data: undefined; error: ApiError | Error };

const LOADING = { status: 'loading' as const, data: undefined, error: undefined };

// useFetch runs `fetcher()` once on mount (and again whenever any
// item in `deps` changes). Returns a state machine of loading /
// ready / error plus a refetch helper that re-runs without
// touching deps.
export function useFetch<T>(fetcher: () => Promise<T>, deps: unknown[] = []): FetchState<T> & { refetch: () => void } {
  const [state, setState] = useState<FetchState<T>>(LOADING);
  const fetchRef = useRef(fetcher);
  fetchRef.current = fetcher;

  const run = useCallback(() => {
    let cancelled = false;
    setState((s) => (s.status === 'ready' ? s : LOADING));
    fetchRef
      .current()
      .then((data) => {
        if (!cancelled) setState({ status: 'ready', data, error: undefined });
      })
      .catch((err: ApiError | Error) => {
        if (!cancelled) setState({ status: 'error', data: undefined, error: err });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    return run();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { ...state, refetch: run };
}

// usePoll runs `fetcher()` on mount, then again every `intervalMs`
// while the component is mounted and the document is visible. Pauses
// while the tab is hidden to avoid burning recorder CPU on a tab
// no one is looking at.
export function usePoll<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  deps: unknown[] = [],
): FetchState<T> & { refetch: () => void } {
  const [state, setState] = useState<FetchState<T>>(LOADING);
  const fetchRef = useRef(fetcher);
  fetchRef.current = fetcher;

  const tick = useCallback(async () => {
    try {
      const data = await fetchRef.current();
      setState({ status: 'ready', data, error: undefined });
    } catch (err) {
      setState({ status: 'error', data: undefined, error: err as ApiError | Error });
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;

    const loop = async () => {
      if (cancelled) return;
      if (document.visibilityState === 'visible') {
        await tick();
      }
      timer = window.setTimeout(loop, intervalMs);
    };

    loop();
    const onVisibility = () => {
      if (document.visibilityState === 'visible') tick();
    };
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      cancelled = true;
      if (timer !== undefined) clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVisibility);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, ...deps]);

  return { ...state, refetch: tick };
}

/* ---------- Format helpers ---------- */

// Go's time.Duration JSON-marshals to nanoseconds. Render as
// "{D}D {H}H" when ≥ 1 day, "{H}H {M}M" otherwise.
export function formatUptime(ns: number): string {
  if (!Number.isFinite(ns) || ns <= 0) return '—';
  const sec = Math.floor(ns / 1e9);
  const days = Math.floor(sec / 86400);
  const hours = Math.floor((sec % 86400) / 3600);
  const minutes = Math.floor((sec % 3600) / 60);
  if (days > 0) return `${days}D ${String(hours).padStart(2, '0')}H`;
  return `${hours}H ${String(minutes).padStart(2, '0')}M`;
}

// Human-readable byte count (binary units; matches `df -h`).
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return v >= 100 ? `${v.toFixed(0)} ${units[i]}` : `${v.toFixed(1)} ${units[i]}`;
}

// Locale-friendly time string for log/event timestamps.
export function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString('en-GB', { hour12: false });
}
