// Console data layer — replaces the design prototype's simulated
// DataProvider with real recorder state:
//   cameras   /v1/cameras + /health + /capabilities (merged, polled)
//   events    /v1/events (DB-backed records) + SSE live refresh
//   policies  /v1/recording-policies (tier names for grouping/storage)
//   prefs     /v1/me/prefs (grid, tours, clock…)
// All times are RFC3339 from the server; consumers convert via Date.
import {
  createContext, useCallback, useContext, useEffect, useMemo, useRef, useState,
  type ReactNode,
} from 'react';
import {
  ackAllEvents, ackEvent, fetchCameras, getCameraCapabilities, getCameraHealth,
  getMyPrefs, listEvents, listCameraGroups, putMyPrefs, subscribeEventsStream,
  fetchRecordingPolicies,
  type Camera, type CameraCapabilities, type CameraHealth, type EventRecord,
  type MyPrefs, type RecordingPolicy,
} from './api';
import type { CameraGroup } from './types';

export type CameraStatus = 'online' | 'degraded' | 'offline';

// vendorInfo — parsed slice of the capabilities row's vendor JSON.
interface VendorInfo {
  device?: { manufacturer?: string; model?: string; firmware_version?: string; serial_number?: string };
  snapshot_uri?: string;
  sub_stream_uri?: string;
  onvif_xaddr?: string;
}

export interface ConsoleCamera {
  id: string;
  // name — the label shown in the UI (display_name when set, else the
  // path name). path — the immutable recorder stream path; every media
  // URL (HLS, playback, snapshot path lookups) must use path, not name.
  name: string;
  path: string;
  status: CameraStatus;
  recording: boolean;
  hasPTZ: boolean;
  hasAudio: boolean;
  hasSub: boolean;
  manufacturer: string;
  model: string;
  host: string;
  resolution: string;
  codec: string;
  group: string;
  policyId: string;
  raw: Camera;
  health?: CameraHealth;
  caps?: CameraCapabilities;
  vendor?: VendorInfo;
}

interface ConsoleState {
  now: Date;
  cameras: ConsoleCamera[];
  camerasLoaded: boolean;
  getCam: (id: string) => ConsoleCamera | undefined;
  refreshCameras: () => void;
  events: EventRecord[];
  eventsLoaded: boolean;
  unreadCount: number;
  refreshEvents: () => void;
  acknowledge: (id: string) => Promise<void>;
  acknowledgeAll: (cameraID?: string) => Promise<number>;
  policies: RecordingPolicy[];
  groups: CameraGroup[];
  prefs: MyPrefs;
  savePrefs: (patch: MyPrefs) => Promise<void>;
}

const ConsoleContext = createContext<ConsoleState | null>(null);

export function useConsole(): ConsoleState {
  const v = useContext(ConsoleContext);
  if (!v) throw new Error('useConsole outside ConsoleProvider');
  return v;
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    const m = url.match(/\/\/([^/:@]+@)?([^/:]+)/);
    return m ? m[2] : '';
  }
}

function statusOf(cam: Camera, health?: CameraHealth): CameraStatus {
  if (health) {
    if (health.rtsp_state === 'connected') return 'online';
    if (health.rtsp_state === 'reconnecting') return 'degraded';
    if (health.rtsp_state === 'failed') return 'offline';
  }
  if (cam.runtime) return cam.runtime.available ? 'online' : 'offline';
  return 'offline';
}

function parseVendor(caps?: CameraCapabilities): VendorInfo | undefined {
  if (!caps || !caps.vendor) return undefined;
  try {
    return JSON.parse(caps.vendor) as VendorInfo;
  } catch {
    return undefined;
  }
}

export function ConsoleProvider({ children }: { children: ReactNode }) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(t);
  }, []);

  /* ----- cameras + health + capabilities ----- */
  const [rawCams, setRawCams] = useState<Camera[]>([]);
  const [camerasLoaded, setCamerasLoaded] = useState(false);
  const [healths, setHealths] = useState<Record<string, CameraHealth>>({});
  const capsRef = useRef<Record<string, CameraCapabilities | null>>({});
  const [capsTick, setCapsTick] = useState(0);

  const refreshCameras = useCallback(() => {
    void fetchCameras(0, 200)
      .then((r) => {
        const items = r.items ?? [];
        setRawCams(items);
        setCamerasLoaded(true);
        // Health per camera (parallel, tolerant).
        void Promise.all(
          items.map((c) =>
            getCameraHealth(c.id)
              .then((h) => [c.id, h] as const)
              .catch(() => null),
          ),
        ).then((hs) => {
          const m: Record<string, CameraHealth> = {};
          for (const e of hs) if (e) m[e[0]] = e[1];
          setHealths(m);
        });
        // Capabilities once per camera (adoption-time snapshot).
        for (const c of items) {
          if (capsRef.current[c.id] !== undefined) continue;
          capsRef.current[c.id] = null; // in flight
          void getCameraCapabilities(c.id)
            .then((caps) => {
              capsRef.current[c.id] = caps;
              setCapsTick((t) => t + 1);
            })
            .catch(() => {
              capsRef.current[c.id] = null;
            });
        }
      })
      .catch(() => setCamerasLoaded(true));
  }, []);

  useEffect(() => {
    refreshCameras();
    const t = window.setInterval(refreshCameras, 10_000);
    return () => window.clearInterval(t);
  }, [refreshCameras]);

  /* ----- recording policies + camera groups ----- */
  const [policies, setPolicies] = useState<RecordingPolicy[]>([]);
  const [groups, setGroups] = useState<CameraGroup[]>([]);
  useEffect(() => {
    void fetchRecordingPolicies().then((r) => setPolicies(r.items ?? [])).catch(() => {});
    void listCameraGroups().then(setGroups).catch(() => {});
  }, []);

  const cameras = useMemo<ConsoleCamera[]>(() => {
    void capsTick; // re-derive when a capabilities fetch lands
    return rawCams.map((c) => {
      const health = healths[c.id];
      const caps = capsRef.current[c.id] || undefined;
      const vendor = parseVendor(caps);
      const prof = caps?.profiles?.find((p) => p.token === caps.selected_profile_token) || caps?.profiles?.[0];
      const policy = policies.find((p) => p.id === c.recording_policy_id);
      const policyName = policy ? policy.name.toUpperCase() : 'DEFAULT';
      return {
        id: c.id,
        name: c.display_name || c.name,
        path: c.name,
        status: statusOf(c, health),
        recording: statusOf(c, health) === 'online' && (policy ? policy.mode !== 'off' : true),
        hasPTZ: caps?.has_ptz ?? false,
        hasAudio: caps?.has_audio ?? false,
        hasSub: Boolean(vendor?.sub_stream_uri),
        manufacturer: vendor?.device?.manufacturer ?? '',
        model: vendor?.device?.model ?? '',
        host: hostOf(c.source_url),
        resolution: prof ? `${prof.width}×${prof.height}` : '',
        codec: prof?.video_codec ?? '',
        group: policyName,
        policyId: c.recording_policy_id ?? '',
        raw: c,
        health,
        caps,
        vendor,
      };
    });
  }, [rawCams, healths, capsTick, policies]);

  const getCam = useCallback(
    (id: string) => cameras.find((c) => c.id === id),
    [cameras],
  );

  /* ----- events (DB-backed) + SSE live refresh ----- */
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [eventsLoaded, setEventsLoaded] = useState(false);
  const refreshEvents = useCallback(() => {
    void listEvents({ itemsPerPage: 200 })
      .then((r) => {
        setEvents(r.items ?? []);
        setEventsLoaded(true);
      })
      .catch(() => setEventsLoaded(true));
  }, []);

  useEffect(() => {
    refreshEvents();
  }, [refreshEvents]);

  // SSE: any live event triggers a debounced re-list so the console
  // picks up the full DB record (snapshot URLs, ack state) rather than
  // trying to splice the stream's lighter wire shape.
  useEffect(() => {
    let timer: number | null = null;
    const bump = () => {
      if (timer != null) return;
      timer = window.setTimeout(() => {
        timer = null;
        refreshEvents();
      }, 800);
    };
    const unsub = subscribeEventsStream({}, bump, () => {});
    // Fallback poll in case the stream drops silently.
    const poll = window.setInterval(refreshEvents, 30_000);
    return () => {
      unsub();
      if (timer != null) window.clearTimeout(timer);
      window.clearInterval(poll);
    };
  }, [refreshEvents]);

  const unreadCount = useMemo(
    () => events.filter((e) => !e.acknowledged_at).length,
    [events],
  );

  const acknowledge = useCallback(async (id: string) => {
    await ackEvent(id);
    setEvents((es) => es.map((e) => (e.id === id ? { ...e, acknowledged_at: new Date().toISOString() } : e)));
  }, []);

  const acknowledgeAll = useCallback(async (cameraID?: string) => {
    const r = await ackAllEvents(cameraID);
    refreshEvents();
    return r.acknowledged;
  }, [refreshEvents]);

  /* ----- per-user prefs ----- */
  const [prefs, setPrefs] = useState<MyPrefs>({});
  useEffect(() => {
    void getMyPrefs().then(setPrefs).catch(() => {});
  }, []);
  const savePrefs = useCallback(async (patch: MyPrefs) => {
    setPrefs((p) => ({ ...p, ...patch }));
    try {
      await putMyPrefs(patch);
    } catch {
      /* non-fatal; next load re-syncs */
    }
  }, []);

  const value = useMemo<ConsoleState>(
    () => ({
      now, cameras, camerasLoaded, getCam, refreshCameras,
      events, eventsLoaded, unreadCount, refreshEvents, acknowledge, acknowledgeAll,
      policies, groups, prefs, savePrefs,
    }),
    [now, cameras, camerasLoaded, getCam, refreshCameras, events, eventsLoaded,
     unreadCount, refreshEvents, acknowledge, acknowledgeAll, policies, groups, prefs, savePrefs],
  );

  return <ConsoleContext.Provider value={value}>{children}</ConsoleContext.Provider>;
}
