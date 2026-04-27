// Cameras route — list, ONVIF discover, manual-add wizard, and the
// per-camera config drawer (5 tabs). Faithful port of the design's
// cameras.jsx. Kept in a single file matching the design's structure;
// splitting would add module ceremony without aiding comprehension —
// the four sub-flows reference each other through shared types and
// the configCam state on the parent.
//
// Sub-flows in order:
//   - DiscoverPanel: 3-phase ONVIF scan with bulk creds + inline preview
//   - ManualAddWizard: 4-step form (identify / authenticate / stream / finalize)
//   - CameraList: clickable rows that open the config drawer
//   - CameraConfigDrawer: right-side overlay with 5 tabs

import { useEffect, useRef, useState } from 'react';
import {
  Btn,
  Brackets,
  Card,
  Input,
  KV,
  SectionHeader,
  Segmented,
  SliderField,
  StatusBadge,
  Toggle,
} from '../components/primitives';
import { Icon } from '../components/Icon';
import type { IconName } from '../components/Icon';
import { PageHeader } from '../components/PageHeader';
import { fetchCameras, deleteCamera, createCamera } from '../lib/api';
import type { Camera as ApiCamera, CameraSourceType } from '../lib/api';
import { useFetch } from '../lib/hooks';
import type { AppState, ToastInput, UICamera } from '../lib/types';

interface CamerasProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  addToast: (t: ToastInput) => void;
}

type Mode = 'list' | 'scan' | 'manual';

interface DiscoverHit {
  id: string;
  ip: string;
  mac: string;
  vendor: string;
  model: string;
  resolution: string;
  fps: number;
  onvif: boolean;
  rtsp: string;
  identified: boolean;
  status: 'reachable' | 'identifying' | 'ok';
}

const VENDOR_POOL: Omit<DiscoverHit, 'identified' | 'status'>[] = [
  { id: 'CAM-07', ip: '10.0.1.47', mac: 'B8:27:EB:2A:81:77', vendor: 'Hikvision', model: 'DS-2CD2143G2-IS', resolution: '2688×1520', fps: 25, onvif: true, rtsp: '/Streaming/Channels/101' },
  { id: 'CAM-08', ip: '10.0.1.48', mac: 'B8:27:EB:2A:81:88', vendor: 'Axis', model: 'M3215-LVE', resolution: '1920×1080', fps: 30, onvif: true, rtsp: '/axis-media/media.amp' },
  { id: 'CAM-09', ip: '10.0.1.49', mac: 'B8:27:EB:2A:81:99', vendor: 'Dahua', model: 'IPC-HFW3841T-ZAS', resolution: '3840×2160', fps: 15, onvif: true, rtsp: '/cam/realmonitor?channel=1&subtype=0' },
  { id: 'CAM-10', ip: '10.0.1.50', mac: 'B8:27:EB:2A:81:AA', vendor: 'Reolink', model: 'RLC-823A 16x', resolution: '3840×2160', fps: 25, onvif: true, rtsp: '/h264Preview_01_main' },
  { id: 'CAM-11', ip: '10.0.1.51', mac: 'B8:27:EB:2A:81:BB', vendor: 'Bosch', model: 'DINION 7100i IR', resolution: '3840×2160', fps: 30, onvif: true, rtsp: '/rtsp_tunnel?h26x=4&line=1' },
  { id: 'CAM-12', ip: '10.0.1.52', mac: 'B8:27:EB:2A:81:CC', vendor: 'Unknown', model: 'Generic ONVIF', resolution: '1920×1080', fps: 20, onvif: false, rtsp: '/live' },
];

interface VendorDefaults {
  user: string;
  port: string;
  path: string;
}

const VENDOR_DEFAULTS: Record<string, VendorDefaults> = {
  Hikvision: { user: 'admin', port: '554', path: '/Streaming/Channels/101' },
  Axis: { user: 'root', port: '554', path: '/axis-media/media.amp' },
  Dahua: { user: 'admin', port: '554', path: '/cam/realmonitor?channel=1&subtype=0' },
  Reolink: { user: 'admin', port: '554', path: '/h264Preview_01_main' },
  Bosch: { user: 'service', port: '554', path: '/rtsp_tunnel?h26x=4&line=1' },
  Generic: { user: 'admin', port: '554', path: '/live' },
};

// Translate canonical Camera (from /v1/cameras) into the UICamera
// shape the list/drawer components expect. Runtime online → status;
// fields the SPA cares about beyond canonical (resolution, fps,
// codec) are derived stubs until /v1/streams wiring lands per the
// stub list in docs/web-ui.md.
function toUICamera(c: ApiCamera): UICamera {
  const status: UICamera['status'] = c.runtime
    ? c.runtime.online
      ? 'online'
      : 'offline'
    : 'offline';
  return {
    id: c.id,
    name: c.name,
    ip: c.source_url,
    status,
    resolution: '—', // STUB until /v1/streams wires up
    fps: 0,
    codec: '—',
    vendor: undefined,
    user: c.credentials_ref ?? undefined,
  };
}

export function Cameras({ state, setState, addToast }: CamerasProps) {
  const [mode, setMode] = useState<Mode>('list');
  const [configCam, setConfigCam] = useState<UICamera | null>(null);

  // Real /v1/cameras list. Refetches on add/delete via the helper.
  const list = useFetch(() => fetchCameras(0, 100), []);

  // Mirror the canonical list into AppState.cameras so other routes
  // (Overview's mini grid, the wizard) see the same data. STUB:
  // the design's UICamera carries fields we don't have on canonical
  // (resolution/fps/codec); those stay blank until /v1/streams wires.
  useEffect(() => {
    if (list.status === 'ready') {
      setState((s) => ({
        ...s,
        cameras: list.data.items.map(toUICamera),
      }));
    }
  }, [list.status, list.status === 'ready' ? list.data : null, setState]);

  async function bulkAdd(cams: UICamera[]) {
    // Discover flow surfaces an array of UICameras (mock-shape from
    // ONVIF probe). Real recorder add takes a small canonical body
    // per camera. For each, POST /v1/cameras then refetch.
    let added = 0;
    for (const cam of cams) {
      try {
        await createCamera({
          name: cam.name,
          source_type: 'rtsp',
          source_url: `rtsp://${cam.ip}:${cam.port ?? '554'}${cam.path ?? ''}`,
        });
        added++;
      } catch (e) {
        addToast({
          kind: 'danger',
          title: 'ADD FAILED',
          body: `${cam.name}: ${(e as Error).message}`,
          icon: 'x',
        });
      }
    }
    if (added > 0) {
      addToast({
        kind: 'success',
        title: 'ADDED',
        body: `${added} camera${added > 1 ? 's' : ''} added`,
        icon: 'check-circle',
      });
    }
    list.refetch();
    setMode('list');
  }

  async function singleAdd(cam: UICamera) {
    try {
      await createCamera({
        name: cam.name,
        source_type: (cam as { source_type?: CameraSourceType }).source_type ?? 'rtsp',
        source_url: `rtsp://${cam.ip}:${cam.port ?? '554'}${cam.path ?? ''}`,
      });
      addToast({ kind: 'success', title: 'ADDED', body: `${cam.name} connected`, icon: 'check-circle' });
      list.refetch();
    } catch (e) {
      addToast({
        kind: 'danger',
        title: 'ADD FAILED',
        body: (e as Error).message,
        icon: 'x',
      });
    }
    setMode('list');
  }

  // Saving the config drawer's edits is currently STUB — the drawer
  // collects motion zones, recording mode, ONVIF events, etc. that
  // don't have a 1:1 /v1/cameras PATCH path yet (RecordingPolicy is
  // its own resource; motion is recorder-internal; ONVIF events are
  // recorder-internal). Most-frequently-edited fields (name, source
  // URL) wire up to PATCH /v1/cameras/{id} in a follow-up. For now
  // the save just closes the drawer and toasts "saved (locally)".
  function saveCamera(updated: UICamera) {
    addToast({
      kind: 'info',
      title: 'SAVED LOCALLY',
      body: `${updated.name} — full PATCH wiring pending`,
      icon: 'check-circle',
    });
    setConfigCam(null);
  }

  async function removeCamera(cam: UICamera) {
    try {
      await deleteCamera(cam.id);
      addToast({
        kind: 'warning',
        title: 'REMOVED',
        body: `${cam.name} unlinked`,
        icon: 'trash-2',
      });
      list.refetch();
    } catch (e) {
      addToast({
        kind: 'danger',
        title: 'DELETE FAILED',
        body: (e as Error).message,
        icon: 'x',
      });
    }
    setConfigCam(null);
  }

  // Cameras the routes/components see. Prefer the live list
  // (canonical) once it arrives; fall back to the mock seed during
  // initial render so the UI doesn't flash an empty grid.
  const cameras = list.status === 'ready' ? list.data.items.map(toUICamera) : state.cameras;

  return (
    <div
      style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        minWidth: 0,
        background: 'var(--bg-primary)',
        overflow: 'auto',
      }}
    >
      <PageHeader
        breadcrumb="RECORDING SERVER / CAMERAS"
        title="Cameras"
        sub={
          list.status === 'error'
            ? `Recorder unreachable — ${list.error.message}`
            : list.status === 'loading'
              ? 'Loading cameras…'
              : `${cameras.filter((c) => c.status === 'online').length} online · ${cameras.filter((c) => c.status === 'degraded').length} degraded · ${cameras.filter((c) => c.status === 'offline').length} offline`
        }
        right={
          <>
            <Btn
              kind="secondary"
              icon="radar"
              onClick={() => setMode(mode === 'scan' ? 'list' : 'scan')}
            >
              Discover
            </Btn>
            <Btn kind="primary" icon="plus" onClick={() => setMode(mode === 'manual' ? 'list' : 'manual')}>
              Add Camera
            </Btn>
          </>
        }
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        {mode === 'scan' && <DiscoverPanel onClose={() => setMode('list')} onAdd={bulkAdd} />}
        {mode === 'manual' && (
          <ManualAddWizard onCancel={() => setMode('list')} onSubmit={singleAdd} addToast={addToast} />
        )}
        <CameraList cameras={cameras} onConfig={setConfigCam} />
      </div>
      {configCam && (
        <CameraConfigDrawer
          camera={configCam}
          onClose={() => setConfigCam(null)}
          onSave={saveCamera}
          onRemove={removeCamera}
        />
      )}
    </div>
  );
}

/* ================================================================
   DISCOVER FLOW
================================================================ */

interface LogLine {
  t: string;
  level: 'ok' | 'info' | 'err';
  msg: string;
}

interface DiscoverProps {
  onClose: () => void;
  onAdd: (cams: UICamera[]) => void;
}

type DiscoverStage = 'probe' | 'identify' | 'done';

function DiscoverPanel({ onClose, onAdd }: DiscoverProps) {
  const [stage, setStage] = useState<DiscoverStage>('probe');
  const [progress, setProgress] = useState(0);
  const [subnet, setSubnet] = useState('10.0.1.0/24');
  const [found, setFound] = useState<DiscoverHit[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [creds, setCreds] = useState({ user: 'admin', pass: '••••••••' });
  const [credsExpanded, setCredsExpanded] = useState(false);
  const [previewing, setPreviewing] = useState<string | null>(null);
  const [logLines, setLogLines] = useState<LogLine[]>([]);
  const logRef = useRef<HTMLDivElement>(null);

  function pushLog(level: LogLine['level'], msg: string) {
    setLogLines((prev) => [...prev.slice(-30), { t: new Date().toTimeString().slice(0, 8), level, msg }]);
  }

  function rescan() {
    setStage('probe');
    setProgress(0);
    setFound([]);
    setSelected(new Set());
    setLogLines([]);
    pushLog('info', `WS-Discovery probe → ${subnet}`);
  }

  // Probe phase: progress ticker + streaming WS-Discovery hits.
  useEffect(() => {
    if (stage !== 'probe') return;
    let p = 0;
    const tick = window.setInterval(() => {
      p += 4 + Math.random() * 6;
      if (p > 100) p = 100;
      setProgress(Math.floor(p));
      if (p >= 100) {
        clearInterval(tick);
        setStage('identify');
        pushLog('ok', 'Probe complete · 254 hosts scanned');
      }
    }, 80);

    const timers = VENDOR_POOL.map((peer, i) =>
      window.setTimeout(() => {
        setFound((prev) => [...prev, { ...peer, identified: false, status: 'reachable' }]);
        pushLog('info', `Reply from ${peer.ip} · ${peer.mac}`);
      }, 400 + i * 380),
    );
    return () => {
      clearInterval(tick);
      timers.forEach(clearTimeout);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stage === 'probe' ? 'go' : 'stop']);

  // Identify phase: walk found list, mark identified.
  useEffect(() => {
    if (stage !== 'identify') return;
    found.forEach((cam, i) => {
      window.setTimeout(() => {
        setFound((prev) => prev.map((c) => (c.id === cam.id ? { ...c, identified: true } : c)));
        pushLog('ok', `${cam.ip} → ${cam.vendor} ${cam.model} · ${cam.resolution}`);
        if (i === found.length - 1) {
          setStage('done');
          pushLog('ok', 'Identification complete');
        }
      }, 250 + i * 200);
    });
    if (found.length === 0) setStage('done');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stage === 'identify' ? 'go' : 'stop']);

  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [logLines]);

  function toggle(id: string) {
    setSelected((prev) => {
      const n = new Set(prev);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  }

  function add() {
    const cams: UICamera[] = found
      .filter((f) => selected.has(f.id) && f.identified)
      .map((f) => ({
        id: f.id,
        name: `${f.vendor} ${f.id.slice(-2)}`,
        ip: f.ip,
        status: 'online',
        resolution: f.resolution,
        fps: f.fps,
        codec: 'H.264',
        vendor: f.vendor,
        model: f.model,
        mac: f.mac,
        port: '554',
        path: f.rtsp,
        user: creds.user,
        onvif: f.onvif,
      }));
    onAdd(cams);
  }

  const stageColor = stage === 'probe' ? '#F97316' : stage === 'identify' ? '#EAB308' : '#22C55E';
  const stageLabel = stage === 'probe' ? 'WS-DISCOVERY' : stage === 'identify' ? 'IDENTIFYING' : 'COMPLETE';

  return (
    <Card style={{ padding: 0 }}>
      {/* Header */}
      <div
        style={{
          padding: '14px 16px',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 12,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <SectionHeader style={{ margin: 0 }}>ONVIF DISCOVERY</SectionHeader>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '4px 10px',
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
            }}
          >
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                letterSpacing: 1,
                textTransform: 'uppercase',
              }}
            >
              SUBNET
            </span>
            <input
              value={subnet}
              onChange={(e) => setSubnet(e.target.value)}
              disabled={stage === 'probe' || stage === 'identify'}
              style={{
                background: 'transparent',
                border: 'none',
                outline: 'none',
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: '#F97316',
                width: 110,
              }}
            />
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              letterSpacing: 1,
              color: stageColor,
              textTransform: 'uppercase',
            }}
          >
            <span
              className={stage !== 'done' ? 'pulse-dot' : ''}
              style={{
                width: 6,
                height: 6,
                background: stageColor,
                borderRadius: '50%',
                boxShadow: `0 0 6px ${stageColor}`,
              }}
            />{' '}
            {stageLabel}
          </span>
          <Btn kind="ghost" size="sm" onClick={onClose}>
            Close
          </Btn>
        </div>
      </div>

      {/* Stage chips + progress */}
      <div
        style={{
          padding: '10px 16px',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          alignItems: 'center',
          gap: 14,
        }}
      >
        <div style={{ display: 'flex', gap: 4, flex: 1 }}>
          {(['Probe', 'Identify', 'Ready'] as const).map((p, i) => {
            const idx = stage === 'probe' ? 0 : stage === 'identify' ? 1 : 2;
            const active = i <= idx;
            return (
              <div key={p} style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 8 }}>
                <div
                  style={{
                    width: 18,
                    height: 18,
                    borderRadius: '50%',
                    border: `1px solid ${active ? '#F97316' : 'var(--border)'}`,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    background: active ? 'rgba(249,115,22,0.15)' : 'transparent',
                  }}
                >
                  {i < idx ? (
                    <Icon name="check" style={{ width: 10, height: 10, color: '#F97316' }} />
                  ) : (
                    <span
                      style={{
                        fontFamily: 'var(--font-mono)',
                        fontSize: 9,
                        color: active ? '#F97316' : 'var(--text-muted)',
                      }}
                    >
                      {i + 1}
                    </span>
                  )}
                </div>
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: active ? 'var(--text-primary)' : 'var(--text-muted)',
                    letterSpacing: 1,
                    textTransform: 'uppercase',
                  }}
                >
                  {p}
                </span>
                {i < 2 && (
                  <div
                    style={{
                      flex: 1,
                      height: 1,
                      background: i < idx ? '#F97316' : 'var(--border)',
                      opacity: i < idx ? 0.5 : 1,
                    }}
                  />
                )}
              </div>
            );
          })}
        </div>
        <div
          style={{
            width: 200,
            height: 4,
            background: 'var(--bg-tertiary)',
            borderRadius: 2,
            overflow: 'hidden',
            position: 'relative',
          }}
        >
          <div
            style={{
              position: 'absolute',
              inset: 0,
              width: `${progress}%`,
              background: stageColor,
              transition: 'width 0.1s linear',
              boxShadow: `0 0 8px ${stageColor}`,
            }}
          />
        </div>
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            color: 'var(--text-secondary)',
            minWidth: 36,
            textAlign: 'right',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {progress}%
        </span>
      </div>

      {/* Two-pane: results + log */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 320px', minHeight: 280 }}>
        <div style={{ borderRight: '1px solid var(--border)' }}>
          {/* Bulk creds row */}
          <div
            style={{
              padding: '10px 16px',
              borderBottom: '1px solid var(--border)',
              background: 'var(--bg-tertiary)',
              display: 'flex',
              alignItems: 'center',
              gap: 10,
            }}
          >
            <Icon name="link" style={{ width: 12, height: 12, color: 'var(--text-muted)' }} />
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                letterSpacing: 1,
                textTransform: 'uppercase',
              }}
            >
              BULK CREDENTIALS
            </span>
            <button
              onClick={() => setCredsExpanded(!credsExpanded)}
              style={{
                background: 'transparent',
                border: '1px solid var(--border)',
                borderRadius: 3,
                padding: '2px 8px',
                color: 'var(--text-secondary)',
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                cursor: 'pointer',
                letterSpacing: 1,
              }}
            >
              {credsExpanded ? 'COLLAPSE' : 'EXPAND'}{' '}
              <Icon
                name={credsExpanded ? 'chevron-down' : 'chevron-right'}
                style={{ width: 8, height: 8, marginLeft: 4 }}
              />
            </button>
            <span style={{ flex: 1 }} />
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
              Applied to all selected on add
            </span>
          </div>
          {credsExpanded && (
            <div
              style={{
                padding: '12px 16px',
                borderBottom: '1px solid var(--border)',
                display: 'grid',
                gridTemplateColumns: '1fr 1fr',
                gap: 10,
              }}
            >
              <Input label="USERNAME" value={creds.user} onChange={(v) => setCreds({ ...creds, user: v })} mono />
              <Input
                label="PASSWORD"
                value={creds.pass}
                onChange={(v) => setCreds({ ...creds, pass: v })}
                type="password"
              />
            </div>
          )}

          {/* Results list */}
          <div style={{ maxHeight: 360, overflowY: 'auto' }}>
            {found.length === 0 && stage === 'probe' && (
              <div style={{ padding: 40, textAlign: 'center' }}>
                <span
                  className="pulse-dot"
                  style={{
                    display: 'inline-block',
                    width: 8,
                    height: 8,
                    background: '#F97316',
                    borderRadius: '50%',
                    boxShadow: '0 0 8px #F97316',
                    marginBottom: 10,
                  }}
                />
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    letterSpacing: 1,
                    color: 'var(--text-muted)',
                    textTransform: 'uppercase',
                  }}
                >
                  Probing 254 hosts on {subnet}…
                </div>
              </div>
            )}
            {found.map((f, i) => {
              const isSelected = selected.has(f.id);
              return (
                <div
                  key={f.id}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '24px 80px 1fr 1fr auto auto',
                    gap: 12,
                    padding: '10px 16px',
                    alignItems: 'center',
                    borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                    background: isSelected ? 'rgba(249,115,22,0.07)' : 'transparent',
                    cursor: f.identified ? 'pointer' : 'default',
                    opacity: f.identified ? 1 : 0.65,
                  }}
                  onClick={() => f.identified && toggle(f.id)}
                >
                  <input
                    type="checkbox"
                    checked={isSelected}
                    disabled={!f.identified}
                    onChange={() => toggle(f.id)}
                    style={{ accentColor: '#F97316' }}
                    onClick={(e) => e.stopPropagation()}
                  />
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: '#F97316' }}>{f.id}</span>
                  <div>
                    {f.identified ? (
                      <>
                        <div
                          style={{
                            fontFamily: 'var(--font-sans)',
                            fontSize: 12,
                            color: 'var(--text-primary)',
                            fontWeight: 500,
                          }}
                        >
                          {f.vendor}{' '}
                          <span style={{ color: 'var(--text-secondary)', fontWeight: 400 }}>{f.model}</span>
                        </div>
                        <div
                          style={{
                            fontFamily: 'var(--font-mono)',
                            fontSize: 10,
                            color: 'var(--text-muted)',
                            marginTop: 2,
                          }}
                        >
                          {f.mac} · {f.onvif ? 'ONVIF Profile S' : 'Generic RTSP'}
                        </div>
                      </>
                    ) : (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <span
                          className="pulse-dot"
                          style={{
                            width: 6,
                            height: 6,
                            background: '#EAB308',
                            borderRadius: '50%',
                          }}
                        />
                        <span
                          style={{
                            fontFamily: 'var(--font-mono)',
                            fontSize: 10,
                            color: 'var(--text-muted)',
                            letterSpacing: 1,
                            textTransform: 'uppercase',
                          }}
                        >
                          Identifying…
                        </span>
                      </div>
                    )}
                  </div>
                  <div>
                    <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
                      {f.ip}
                    </div>
                    {f.identified && (
                      <div
                        style={{
                          fontFamily: 'var(--font-mono)',
                          fontSize: 10,
                          color: 'var(--text-muted)',
                          marginTop: 2,
                        }}
                      >
                        {f.resolution} · {f.fps}FPS
                      </div>
                    )}
                  </div>
                  {f.identified ? (
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setPreviewing(previewing === f.id ? null : f.id);
                      }}
                      style={{
                        background: previewing === f.id ? 'rgba(249,115,22,0.13)' : 'transparent',
                        border: '1px solid var(--border)',
                        borderRadius: 3,
                        padding: '4px 10px',
                        cursor: 'pointer',
                        fontFamily: 'var(--font-mono)',
                        fontSize: 10,
                        color: previewing === f.id ? '#F97316' : 'var(--text-secondary)',
                        letterSpacing: 1,
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: 6,
                      }}
                    >
                      <Icon name="video" style={{ width: 10, height: 10 }} /> PREVIEW
                    </button>
                  ) : (
                    <span />
                  )}
                  {f.identified && <StatusBadge kind="online" label="OK" size="sm" />}
                </div>
              );
            })}

            {/* Inline preview */}
            {previewing &&
              (() => {
                const cam = found.find((f) => f.id === previewing);
                if (!cam) return null;
                return (
                  <div
                    style={{
                      padding: 16,
                      background: 'var(--bg-tertiary)',
                      borderTop: '1px solid var(--border)',
                      borderBottom: '1px solid var(--border)',
                      display: 'grid',
                      gridTemplateColumns: '240px 1fr',
                      gap: 16,
                    }}
                  >
                    <div
                      style={{
                        position: 'relative',
                        aspectRatio: '16/9',
                        background: 'linear-gradient(135deg, #1a1410, #0d0c0a)',
                        overflow: 'hidden',
                      }}
                    >
                      <Brackets />
                      <div
                        style={{
                          position: 'absolute',
                          inset: 0,
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                        }}
                      >
                        <Icon name="video" style={{ width: 28, height: 28, color: 'rgba(249,115,22,0.4)' }} />
                      </div>
                      <div
                        style={{
                          position: 'absolute',
                          top: 6,
                          left: 8,
                          fontFamily: 'var(--font-mono)',
                          fontSize: 9,
                          color: '#F97316',
                          letterSpacing: 1,
                        }}
                      >
                        ● LIVE
                      </div>
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
                        {cam.resolution} · H.264 · {cam.fps}fps
                      </div>
                      <div
                        className="scanline"
                        style={{
                          position: 'absolute',
                          top: 0,
                          bottom: 0,
                          width: 2,
                          background:
                            'linear-gradient(to bottom, transparent, rgba(249,115,22,0.6), transparent)',
                        }}
                      />
                    </div>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                      <span
                        style={{
                          fontFamily: 'var(--font-mono)',
                          fontSize: 10,
                          color: 'var(--text-muted)',
                          letterSpacing: 1,
                          textTransform: 'uppercase',
                        }}
                      >
                        STREAM ENDPOINT
                      </span>
                      <div
                        style={{
                          padding: 8,
                          background: 'var(--bg-primary)',
                          border: '1px solid var(--border)',
                          borderRadius: 3,
                          fontFamily: 'var(--font-mono)',
                          fontSize: 11,
                          color: '#F97316',
                          wordBreak: 'break-all',
                        }}
                      >
                        rtsp://{creds.user}:••••@{cam.ip}:554{cam.rtsp}
                      </div>
                      <div
                        style={{
                          display: 'grid',
                          gridTemplateColumns: 'repeat(2,1fr)',
                          gap: 8,
                          marginTop: 4,
                        }}
                      >
                        <KV k="VENDOR" v={cam.vendor} />
                        <KV k="MODEL" v={cam.model} />
                        <KV k="ONVIF" v={cam.onvif ? 'PROFILE S' : 'NO'} />
                        <KV k="MAC" v={cam.mac} />
                      </div>
                    </div>
                  </div>
                );
              })()}
          </div>
        </div>

        {/* Log panel */}
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          <div
            style={{
              padding: '8px 12px',
              borderBottom: '1px solid var(--border)',
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              letterSpacing: 1,
              textTransform: 'uppercase',
              display: 'flex',
              alignItems: 'center',
              gap: 6,
            }}
          >
            <Icon name="terminal" style={{ width: 10, height: 10 }} /> DISCOVERY LOG
          </div>
          <div
            ref={logRef}
            style={{
              flex: 1,
              padding: 8,
              overflowY: 'auto',
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-secondary)',
              maxHeight: 360,
            }}
          >
            {logLines.map((l, i) => (
              <div key={i} style={{ padding: '2px 4px', display: 'flex', gap: 6 }}>
                <span style={{ color: 'var(--text-muted)' }}>{l.t}</span>
                <span
                  style={{
                    color: l.level === 'ok' ? '#22C55E' : l.level === 'err' ? '#EF4444' : '#F97316',
                    textTransform: 'uppercase',
                    minWidth: 32,
                  }}
                >
                  {l.level}
                </span>
                <span style={{ flex: 1 }}>{l.msg}</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Footer */}
      <div
        style={{
          padding: '12px 16px',
          borderTop: '1px solid var(--border)',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <Btn
            kind="tactical"
            size="sm"
            icon="refresh-cw"
            onClick={rescan}
            disabled={stage === 'probe' || stage === 'identify'}
          >
            Rescan
          </Btn>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              letterSpacing: 1,
              textTransform: 'uppercase',
            }}
          >
            {selected.size} of {found.filter((f) => f.identified).length} selected
          </span>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <Btn
            kind="secondary"
            onClick={() =>
              setSelected(new Set(found.filter((f) => f.identified).map((f) => f.id)))
            }
          >
            Select all
          </Btn>
          <Btn kind="primary" disabled={selected.size === 0} onClick={add}>
            Add {selected.size > 0 ? `${selected.size} ` : ''}Camera
            {selected.size !== 1 ? 's' : ''}
          </Btn>
        </div>
      </div>
    </Card>
  );
}

/* ================================================================
   MANUAL ADD WIZARD
================================================================ */

interface ManualAddProps {
  onCancel: () => void;
  onSubmit: (cam: UICamera) => void;
  addToast: (t: ToastInput) => void;
}

interface ManualForm {
  name: string;
  vendor: string;
  ip: string;
  port: string;
  user: string;
  pass: string;
  path: string;
  protocol: string;
  transport: string;
  onvif: boolean;
}

type ManualErrors = Partial<Record<keyof ManualForm, string>>;

interface ProbeResult {
  ok: boolean;
  vendor?: string;
  model?: string;
  codec?: string;
  res?: string;
  fps?: number;
  onvif?: boolean;
  msg?: string;
}

const STEPS = ['IDENTIFY', 'AUTHENTICATE', 'STREAM', 'FINALIZE'] as const;

function ManualAddWizard({ onCancel, onSubmit, addToast }: ManualAddProps) {
  const [step, setStep] = useState(0);
  const [f, setF] = useState<ManualForm>({
    name: '',
    vendor: 'Hikvision',
    ip: '',
    port: '554',
    user: 'admin',
    pass: '',
    path: '/Streaming/Channels/101',
    protocol: 'rtsp',
    transport: 'TCP',
    onvif: true,
  });
  const [errs, setErrs] = useState<ManualErrors>({});
  const [probe, setProbe] = useState<'busy' | ProbeResult | null>(null);
  const [stream, setStream] = useState<'busy' | 'ok' | 'fail' | null>(null);

  function setVendor(v: string) {
    const d = VENDOR_DEFAULTS[v] || VENDOR_DEFAULTS.Generic;
    setF((s) => ({ ...s, vendor: v, user: d.user, port: d.port, path: d.path }));
  }

  function validateStep0() {
    const e: ManualErrors = {};
    if (!f.name.trim()) e.name = 'Required';
    if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(f.ip)) e.ip = 'IPv4 expected';
    if (!/^\d+$/.test(f.port) || +f.port < 1 || +f.port > 65535) e.port = 'Invalid port';
    setErrs(e);
    return Object.keys(e).length === 0;
  }
  function validateStep1() {
    const e: ManualErrors = {};
    if (!f.user.trim()) e.user = 'Required';
    if (!f.pass) e.pass = 'Required';
    setErrs(e);
    return Object.keys(e).length === 0;
  }

  function probeDevice() {
    if (!validateStep0()) return;
    setProbe('busy');
    window.setTimeout(() => {
      const ok = Math.random() > 0.15;
      if (ok) {
        setProbe({
          ok: true,
          vendor: f.vendor,
          model: VENDOR_DEFAULTS[f.vendor]?.path ? `${f.vendor} device` : 'Generic ONVIF',
          codec: 'H.264',
          res: '1920×1080',
          fps: 30,
          onvif: f.vendor !== 'Generic',
        });
      } else {
        setProbe({ ok: false, msg: 'Device unreachable — ICMP timeout' });
      }
    }, 1400);
  }

  function testStream() {
    if (!validateStep1()) return;
    setStream('busy');
    window.setTimeout(() => setStream(Math.random() > 0.2 ? 'ok' : 'fail'), 1700);
  }

  function finalize() {
    const id = 'CAM-' + String(Math.floor(Math.random() * 89) + 11);
    onSubmit({
      id,
      name: f.name,
      ip: f.ip,
      status: 'online',
      resolution: '1920×1080',
      fps: 30,
      codec: 'H.264',
      vendor: f.vendor,
      port: f.port,
      path: f.path,
      user: f.user,
      onvif: f.onvif,
      transport: f.transport,
    });
  }

  function next() {
    if (step === 0) {
      if (!validateStep0()) return;
      setStep(1);
    } else if (step === 1) {
      if (!validateStep1()) return;
      setStep(2);
    } else if (step === 2) {
      if (stream !== 'ok') {
        addToast({
          kind: 'warning',
          title: 'TEST FIRST',
          body: 'Verify stream before continuing',
          icon: 'info',
        });
        return;
      }
      setStep(3);
    }
  }

  return (
    <Card style={{ padding: 0 }}>
      <div
        style={{
          padding: '14px 16px',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <SectionHeader style={{ margin: 0 }}>ADD CAMERA · MANUAL</SectionHeader>
        <Btn kind="ghost" size="sm" onClick={onCancel}>
          Cancel
        </Btn>
      </div>

      {/* Stepper */}
      <div
        style={{
          padding: '14px 16px',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          gap: 6,
          background: 'var(--bg-tertiary)',
        }}
      >
        {STEPS.map((s, i) => {
          const done = i < step;
          const current = i === step;
          return (
            <div key={s} style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 8 }}>
              <div
                style={{
                  width: 22,
                  height: 22,
                  borderRadius: '50%',
                  border: `1px solid ${done || current ? '#F97316' : 'var(--border)'}`,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  background: current
                    ? '#F97316'
                    : done
                      ? 'rgba(249,115,22,0.13)'
                      : 'transparent',
                  boxShadow: current ? '0 0 8px rgba(249,115,22,0.5)' : 'none',
                }}
              >
                {done ? (
                  <Icon name="check" style={{ width: 10, height: 10, color: '#F97316' }} />
                ) : (
                  <span
                    style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 10,
                      color: current ? '#0d0c0a' : 'var(--text-muted)',
                      fontWeight: 700,
                    }}
                  >
                    {i + 1}
                  </span>
                )}
              </div>
              <span
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 1,
                  color: done || current ? 'var(--text-primary)' : 'var(--text-muted)',
                }}
              >
                {s}
              </span>
              {i < STEPS.length - 1 && (
                <div
                  style={{
                    flex: 1,
                    height: 1,
                    background: done ? '#F97316' : 'var(--border)',
                  }}
                />
              )}
            </div>
          );
        })}
      </div>

      <div style={{ padding: 18, minHeight: 280 }}>
        {step === 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <Input
              label="CAMERA NAME"
              value={f.name}
              onChange={(v) => setF({ ...f, name: v })}
              placeholder="Loading Bay South"
              error={errs.name}
              autoFocus
            />
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                  letterSpacing: 1,
                  textTransform: 'uppercase',
                  marginBottom: 6,
                }}
              >
                VENDOR
              </div>
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                {Object.keys(VENDOR_DEFAULTS).map((v) => (
                  <button
                    key={v}
                    onClick={() => setVendor(v)}
                    style={{
                      padding: '6px 12px',
                      borderRadius: 3,
                      cursor: 'pointer',
                      fontFamily: 'var(--font-mono)',
                      fontSize: 10,
                      letterSpacing: 1,
                      textTransform: 'uppercase',
                      background: f.vendor === v ? 'rgba(249,115,22,0.13)' : 'transparent',
                      border: `1px solid ${f.vendor === v ? '#F97316' : 'var(--border)'}`,
                      color: f.vendor === v ? '#F97316' : 'var(--text-secondary)',
                    }}
                  >
                    {v}
                  </button>
                ))}
              </div>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
              <Input
                label="IP ADDRESS"
                value={f.ip}
                onChange={(v) => setF({ ...f, ip: v })}
                placeholder="10.0.1.50"
                mono
                error={errs.ip}
              />
              <Input
                label="PORT"
                value={f.port}
                onChange={(v) => setF({ ...f, port: v })}
                mono
                error={errs.port}
              />
            </div>
            <div style={{ marginTop: 4, display: 'flex', alignItems: 'center', gap: 12 }}>
              <Btn
                kind="tactical"
                icon="radar"
                onClick={probeDevice}
                disabled={probe === 'busy'}
              >
                {probe === 'busy' ? 'Probing…' : 'Probe Device'}
              </Btn>
              {probe && probe !== 'busy' && probe.ok && (
                <StatusBadge
                  kind="online"
                  label={`${probe.vendor} · ${probe.codec} · ${probe.res}`}
                  size="sm"
                />
              )}
              {probe && probe !== 'busy' && !probe.ok && probe.msg && (
                <StatusBadge kind="error" label={probe.msg} size="sm" />
              )}
            </div>
          </div>
        )}

        {step === 1 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div
              style={{
                padding: 12,
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border)',
                borderRadius: 4,
                display: 'flex',
                gap: 12,
                alignItems: 'center',
              }}
            >
              <Icon name="server" style={{ width: 16, height: 16, color: '#F97316' }} />
              <div style={{ flex: 1 }}>
                <div
                  style={{
                    fontFamily: 'var(--font-sans)',
                    fontSize: 13,
                    color: 'var(--text-primary)',
                    fontWeight: 500,
                  }}
                >
                  {f.name} · {f.vendor}
                </div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    marginTop: 2,
                  }}
                >
                  {f.ip}:{f.port}
                </div>
              </div>
              <StatusBadge kind="online" label="REACHABLE" size="sm" />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <Input label="USERNAME" value={f.user} onChange={(v) => setF({ ...f, user: v })} mono error={errs.user} />
              <Input
                label="PASSWORD"
                value={f.pass}
                onChange={(v) => setF({ ...f, pass: v })}
                type="password"
                error={errs.pass}
              />
            </div>
            <Toggle
              on={f.onvif}
              onChange={(v) => setF({ ...f, onvif: v })}
              label="USE ONVIF FOR EVENTS / PTZ / METADATA"
            />
            <div
              style={{
                padding: 10,
                background: 'rgba(249,115,22,0.05)',
                border: '1px solid rgba(249,115,22,0.27)',
                borderRadius: 3,
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-secondary)',
                display: 'flex',
                gap: 8,
                alignItems: 'flex-start',
              }}
            >
              <Icon name="info" style={{ width: 12, height: 12, color: '#F97316', marginTop: 2 }} />
              <span>
                Credentials are stored encrypted on the recorder. They are never transmitted to the management server.
              </span>
            </div>
          </div>
        )}

        {step === 2 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    letterSpacing: 1,
                    textTransform: 'uppercase',
                    marginBottom: 6,
                  }}
                >
                  PROTOCOL
                </div>
                <Segmented
                  options={['RTSP', 'RTSPS', 'HTTP-MJPEG'] as const}
                  value={f.protocol.toUpperCase() as 'RTSP' | 'RTSPS' | 'HTTP-MJPEG'}
                  onChange={(v) => setF({ ...f, protocol: v.toLowerCase() })}
                  size="sm"
                />
              </div>
              <div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    letterSpacing: 1,
                    textTransform: 'uppercase',
                    marginBottom: 6,
                  }}
                >
                  TRANSPORT
                </div>
                <Segmented
                  options={['TCP', 'UDP', 'AUTO'] as const}
                  value={f.transport as 'TCP' | 'UDP' | 'AUTO'}
                  onChange={(v) => setF({ ...f, transport: v })}
                  size="sm"
                />
              </div>
            </div>
            <Input label="STREAM PATH" value={f.path} onChange={(v) => setF({ ...f, path: v })} mono />
            <div
              style={{
                padding: 10,
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border)',
                borderRadius: 4,
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-secondary)',
                display: 'flex',
                gap: 6,
                alignItems: 'center',
              }}
            >
              <span style={{ color: 'var(--text-muted)' }}>URL</span>
              <span style={{ color: '#F97316' }}>
                {f.protocol}://{f.user || 'user'}:{'•'.repeat(Math.min(f.pass.length, 8)) || '•••'}@
                {f.ip || '0.0.0.0'}:{f.port}
                {f.path}
              </span>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <Btn kind="tactical" icon="plug" onClick={testStream} disabled={stream === 'busy'}>
                {stream === 'busy' ? 'Connecting…' : 'Test Stream'}
              </Btn>
              {stream === 'ok' && <StatusBadge kind="online" label="STREAM OK · 30FPS · H.264" size="sm" />}
              {stream === 'fail' && <StatusBadge kind="error" label="STREAM FAIL · CHECK PATH" size="sm" />}
            </div>
            {stream === 'ok' && (
              <div
                style={{
                  position: 'relative',
                  aspectRatio: '16/9',
                  maxWidth: 320,
                  background: 'linear-gradient(135deg, #1a1410, #0d0c0a)',
                  overflow: 'hidden',
                }}
              >
                <Brackets />
                <div
                  style={{
                    position: 'absolute',
                    inset: 0,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                >
                  <Icon name="video" style={{ width: 32, height: 32, color: 'rgba(249,115,22,0.4)' }} />
                </div>
                <div
                  style={{
                    position: 'absolute',
                    top: 6,
                    left: 8,
                    fontFamily: 'var(--font-mono)',
                    fontSize: 9,
                    color: '#F97316',
                    letterSpacing: 1,
                  }}
                >
                  ● LIVE
                </div>
                <div
                  className="scanline"
                  style={{
                    position: 'absolute',
                    top: 0,
                    bottom: 0,
                    width: 2,
                    background:
                      'linear-gradient(to bottom, transparent, rgba(249,115,22,0.6), transparent)',
                  }}
                />
              </div>
            )}
          </div>
        )}

        {step === 3 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div
              style={{
                padding: 16,
                background: 'rgba(34,197,94,0.07)',
                border: '1px solid rgba(34,197,94,0.27)',
                borderRadius: 4,
                display: 'flex',
                gap: 12,
                alignItems: 'center',
              }}
            >
              <Icon name="check-circle" style={{ width: 20, height: 20, color: '#22C55E' }} />
              <div style={{ flex: 1 }}>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    letterSpacing: 1,
                    color: '#22C55E',
                    textTransform: 'uppercase',
                  }}
                >
                  READY TO ADD
                </div>
                <div
                  style={{
                    fontFamily: 'var(--font-sans)',
                    fontSize: 13,
                    color: 'var(--text-primary)',
                    marginTop: 4,
                  }}
                >
                  {f.name} · {f.vendor}
                </div>
              </div>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10 }}>
              <KV k="ENDPOINT" v={`${f.ip}:${f.port}`} />
              <KV k="PROTOCOL" v={`${f.protocol.toUpperCase()} · ${f.transport}`} />
              <KV k="STREAM" v="H.264 · 1920×1080 · 30fps" />
              <KV k="ONVIF" v={f.onvif ? 'ENABLED' : 'DISABLED'} />
              <KV k="USER" v={f.user} />
              <KV k="PATH" v={f.path} />
            </div>
            <div
              style={{
                padding: 10,
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border)',
                borderRadius: 4,
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-secondary)',
                display: 'flex',
                gap: 8,
                alignItems: 'flex-start',
              }}
            >
              <Icon name="info" style={{ width: 12, height: 12, color: 'var(--text-muted)', marginTop: 2 }} />
              <span>
                Recording rules will be inherited from the management server. Configure motion zones and substream
                after add.
              </span>
            </div>
          </div>
        )}
      </div>

      <div
        style={{
          padding: '12px 16px',
          borderTop: '1px solid var(--border)',
          display: 'flex',
          justifyContent: 'space-between',
        }}
      >
        <Btn kind="ghost" disabled={step === 0} onClick={() => setStep(step - 1)} icon="chevron-left">
          Back
        </Btn>
        {step < 3 && (
          <Btn kind="primary" onClick={next} icon="chevron-right">
            Continue
          </Btn>
        )}
        {step === 3 && (
          <Btn kind="primary" icon="check" onClick={finalize}>
            Add Camera
          </Btn>
        )}
      </div>
    </Card>
  );
}

/* ================================================================
   CAMERA LIST
================================================================ */

function CameraList({ cameras, onConfig }: { cameras: UICamera[]; onConfig: (c: UICamera) => void }) {
  return (
    <Card style={{ padding: 0 }}>
      <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
        <SectionHeader style={{ margin: 0 }}>CONFIGURED CAMERAS · {cameras.length}</SectionHeader>
      </div>
      {cameras.map((c, i) => (
        <div
          key={c.id}
          onClick={() => onConfig(c)}
          style={{
            display: 'grid',
            gridTemplateColumns: '96px 1fr 1fr auto auto',
            gap: 14,
            padding: 12,
            alignItems: 'center',
            cursor: 'pointer',
            borderTop: i === 0 ? 'none' : '1px solid var(--border)',
            opacity: c.status === 'offline' ? 0.65 : 1,
            transition: 'background 0.1s',
          }}
          onMouseEnter={(e) => (e.currentTarget.style.background = 'rgba(249,115,22,0.04)')}
          onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
        >
          <div
            style={{
              width: 96,
              height: 56,
              position: 'relative',
              background: 'linear-gradient(135deg, #1a1410, #0d0c0a)',
            }}
          >
            <Brackets />
            <div
              style={{
                position: 'absolute',
                inset: 0,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <Icon
                name={c.status === 'offline' ? 'wifi-off' : 'video'}
                style={{
                  width: 16,
                  height: 16,
                  color: c.status === 'offline' ? '#EF4444' : 'rgba(249,115,22,0.5)',
                }}
              />
            </div>
            {c.status !== 'offline' && (
              <div
                className="scanline"
                style={{
                  position: 'absolute',
                  top: 0,
                  bottom: 0,
                  width: 2,
                  background:
                    'linear-gradient(to bottom, transparent, rgba(249,115,22,0.5), transparent)',
                }}
              />
            )}
          </div>
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <span
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 14,
                  fontWeight: 600,
                  color: 'var(--text-primary)',
                }}
              >
                {c.name}
              </span>
              <StatusBadge kind={c.status} size="sm" />
            </div>
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                marginTop: 4,
                letterSpacing: 0.5,
              }}
            >
              {c.id} · {c.ip} · {c.codec || 'H.264'}
              {c.vendor ? ' · ' + c.vendor : ''}
            </div>
          </div>
          <div
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              color: 'var(--text-secondary)',
              letterSpacing: 0.5,
            }}
          >
            {c.resolution} · {c.fps} FPS
          </div>
          <Btn
            kind="tactical"
            size="sm"
            onClick={(e) => {
              e.stopPropagation();
              onConfig(c);
            }}
          >
            CONFIG
          </Btn>
          <Icon name="chevron-right" style={{ width: 14, height: 14, color: 'var(--text-muted)' }} />
        </div>
      ))}
    </Card>
  );
}

/* ================================================================
   CAMERA CONFIG DRAWER
================================================================ */

interface CameraConfig extends UICamera {
  pass: string;
  recordingMode: string;
  quality: string;
  retention: number;
  prebuffer: number;
  postbuffer: number;
  substreamEnabled: boolean;
  substreamRes: string;
  substreamFps: number;
  motionEnabled: boolean;
  motionSensitivity: number;
  motionZones: { id: number; name: string; x: number; y: number; w: number; h: number; enabled: boolean }[];
  events: {
    motion: boolean;
    tampering: boolean;
    lineCrossing: boolean;
    audioDetection: boolean;
    objectDetection: boolean;
  };
  timesync: string;
  osdOverlay: boolean;
  audioRecord: boolean;
}

type DrawerTab = 'stream' | 'recording' | 'motion' | 'events' | 'advanced';

interface DrawerProps {
  camera: UICamera;
  onClose: () => void;
  onSave: (c: UICamera) => void;
  onRemove: (c: UICamera) => void;
}

function CameraConfigDrawer({ camera, onClose, onSave, onRemove }: DrawerProps) {
  const [tab, setTab] = useState<DrawerTab>('stream');
  const [c, setC] = useState<CameraConfig>({
    ...camera,
    port: camera.port || '554',
    path: camera.path || '/Streaming/Channels/101',
    user: camera.user || 'admin',
    pass: '••••••••',
    transport: camera.transport || 'TCP',
    vendor: camera.vendor || 'Hikvision',
    onvif: camera.onvif ?? true,
    recordingMode: 'CONTINUOUS+EVENT',
    quality: 'HIGH',
    retention: 14,
    prebuffer: 5,
    postbuffer: 10,
    substreamEnabled: true,
    substreamRes: '640×360',
    substreamFps: 10,
    motionEnabled: true,
    motionSensitivity: 65,
    motionZones: [
      { id: 1, name: 'Front Path', x: 12, y: 22, w: 38, h: 44, enabled: true },
      { id: 2, name: 'Vehicle Lane', x: 56, y: 38, w: 36, h: 50, enabled: true },
    ],
    events: {
      motion: true,
      tampering: true,
      lineCrossing: false,
      audioDetection: false,
      objectDetection: true,
    },
    timesync: 'NTP',
    osdOverlay: true,
    audioRecord: false,
  });

  function patch(p: Partial<CameraConfig>) {
    setC((s) => ({ ...s, ...p }));
  }

  const TABS: { k: DrawerTab; l: string; i: IconName }[] = [
    { k: 'stream', l: 'STREAM', i: 'video' },
    { k: 'recording', l: 'RECORDING', i: 'database' },
    { k: 'motion', l: 'MOTION', i: 'activity' },
    { k: 'events', l: 'ONVIF EVENTS', i: 'zap' },
    { k: 'advanced', l: 'ADVANCED', i: 'settings' },
  ];

  return (
    <>
      <div
        onClick={onClose}
        style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(0,0,0,0.55)',
          backdropFilter: 'blur(2px)',
          zIndex: 90,
        }}
      />
      <div
        style={{
          position: 'fixed',
          top: 0,
          right: 0,
          bottom: 0,
          width: 720,
          maxWidth: '92vw',
          background: 'var(--bg-secondary)',
          borderLeft: '1px solid var(--border)',
          boxShadow: '-12px 0 32px rgba(0,0,0,0.5)',
          zIndex: 91,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        {/* Header */}
        <div
          style={{
            padding: '16px 20px',
            borderBottom: '1px solid var(--border)',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'flex-start',
          }}
        >
          <div>
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                letterSpacing: 1,
                textTransform: 'uppercase',
              }}
            >
              CAMERA · {c.id}
            </div>
            <input
              value={c.name}
              onChange={(e) => patch({ name: e.target.value })}
              style={{
                background: 'transparent',
                border: 'none',
                outline: 'none',
                fontFamily: 'var(--font-sans)',
                fontSize: 22,
                fontWeight: 600,
                color: 'var(--text-primary)',
                padding: '4px 0',
                marginTop: 2,
                width: '100%',
              }}
            />
            <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 6 }}>
              <StatusBadge kind={c.status} size="sm" />
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                {c.ip}:{c.port} · {c.vendor}
              </span>
            </div>
          </div>
          <button
            onClick={onClose}
            style={{
              background: 'transparent',
              border: '1px solid var(--border)',
              borderRadius: 4,
              width: 32,
              height: 32,
              cursor: 'pointer',
              color: 'var(--text-secondary)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon name="x" style={{ width: 14, height: 14 }} />
          </button>
        </div>

        {/* Tabs */}
        <div
          style={{
            padding: '0 20px',
            borderBottom: '1px solid var(--border)',
            background: 'var(--bg-tertiary)',
            display: 'flex',
            gap: 0,
          }}
        >
          {TABS.map((t) => (
            <button
              key={t.k}
              onClick={() => setTab(t.k)}
              style={{
                padding: '10px 14px',
                background: 'transparent',
                border: 'none',
                cursor: 'pointer',
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                letterSpacing: 1,
                color: tab === t.k ? '#F97316' : 'var(--text-muted)',
                borderBottom: `2px solid ${tab === t.k ? '#F97316' : 'transparent'}`,
                display: 'inline-flex',
                alignItems: 'center',
                gap: 6,
                marginBottom: -1,
              }}
            >
              <Icon name={t.i} style={{ width: 11, height: 11 }} /> {t.l}
            </button>
          ))}
        </div>

        {/* Tab body */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>
          {tab === 'stream' && <StreamTab c={c} patch={patch} />}
          {tab === 'recording' && <RecordingTab c={c} patch={patch} />}
          {tab === 'motion' && <MotionTab c={c} patch={patch} />}
          {tab === 'events' && <EventsTab c={c} patch={patch} />}
          {tab === 'advanced' && <AdvancedTab c={c} patch={patch} />}
        </div>

        {/* Footer */}
        <div
          style={{
            padding: '14px 20px',
            borderTop: '1px solid var(--border)',
            display: 'flex',
            justifyContent: 'space-between',
          }}
        >
          <Btn kind="danger" icon="trash-2" onClick={() => onRemove(c)}>
            Remove Camera
          </Btn>
          <div style={{ display: 'flex', gap: 8 }}>
            <Btn kind="ghost" onClick={onClose}>
              Cancel
            </Btn>
            <Btn kind="primary" icon="check" onClick={() => onSave(c)}>
              Save Changes
            </Btn>
          </div>
        </div>
      </div>
    </>
  );
}

/* ---- Drawer tabs ---- */

interface TabProps {
  c: CameraConfig;
  patch: (p: Partial<CameraConfig>) => void;
}

function StreamTab({ c, patch }: TabProps) {
  const [testing, setTesting] = useState<'busy' | 'ok' | 'fail' | null>(null);
  function test() {
    setTesting('busy');
    window.setTimeout(() => setTesting(Math.random() > 0.15 ? 'ok' : 'fail'), 1500);
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      {/* Live preview */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <div
          style={{
            position: 'relative',
            aspectRatio: '16/9',
            background: 'linear-gradient(135deg, #1a1410, #0d0c0a)',
            overflow: 'hidden',
          }}
        >
          <Brackets />
          <div
            style={{
              position: 'absolute',
              inset: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon name="video" style={{ width: 32, height: 32, color: 'rgba(249,115,22,0.4)' }} />
          </div>
          <div
            style={{
              position: 'absolute',
              top: 8,
              left: 8,
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              color: '#F97316',
              letterSpacing: 1,
            }}
          >
            ● LIVE · MAIN
          </div>
          <div
            style={{
              position: 'absolute',
              bottom: 8,
              left: 8,
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              color: 'rgba(255,255,255,0.7)',
            }}
          >
            {c.resolution} · H.264 · {c.fps}fps
          </div>
          <div
            className="scanline"
            style={{
              position: 'absolute',
              top: 0,
              bottom: 0,
              width: 2,
              background:
                'linear-gradient(to bottom, transparent, rgba(249,115,22,0.6), transparent)',
            }}
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <SectionHeader>STREAM HEALTH</SectionHeader>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2,1fr)', gap: 8 }}>
            <KV k="UPTIME" v="14d 8h 22m" />
            <KV k="LATENCY" v="84 ms" />
            <KV k="PACKET LOSS" v="0.02%" />
            <KV k="JITTER" v="2.1 ms" />
            <KV k="GOP" v="2 s · I-FRAME" />
            <KV k="BITRATE" v="4.2 Mb/s" />
          </div>
        </div>
      </div>

      {/* Endpoint */}
      <div>
        <SectionHeader>ENDPOINT</SectionHeader>
        <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10, marginTop: 10 }}>
          <Input label="IP ADDRESS" value={c.ip} onChange={(v) => patch({ ip: v })} mono />
          <Input label="PORT" value={c.port!} onChange={(v) => patch({ port: v })} mono />
        </div>
        <div style={{ marginTop: 10 }}>
          <Input label="STREAM PATH" value={c.path!} onChange={(v) => patch({ path: v })} mono />
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 10 }}>
          <Input label="USERNAME" value={c.user!} onChange={(v) => patch({ user: v })} mono />
          <Input label="PASSWORD" value={c.pass} onChange={(v) => patch({ pass: v })} type="password" />
        </div>
        <div style={{ marginTop: 10 }}>
          <div
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              letterSpacing: 1,
              textTransform: 'uppercase',
              marginBottom: 6,
            }}
          >
            TRANSPORT
          </div>
          <Segmented
            options={['TCP', 'UDP', 'AUTO'] as const}
            value={c.transport as 'TCP' | 'UDP' | 'AUTO'}
            onChange={(v) => patch({ transport: v })}
            size="sm"
          />
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14 }}>
          <Btn kind="tactical" icon="plug" onClick={test} disabled={testing === 'busy'}>
            {testing === 'busy' ? 'Testing…' : 'Test Stream'}
          </Btn>
          {testing === 'ok' && <StatusBadge kind="online" label="STREAM OK" size="sm" />}
          {testing === 'fail' && <StatusBadge kind="error" label="CONNECT FAIL" size="sm" />}
        </div>
      </div>

      {/* Substream */}
      <div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <SectionHeader>SUBSTREAM (LOW-RES)</SectionHeader>
          <Toggle on={c.substreamEnabled} onChange={(v) => patch({ substreamEnabled: v })} />
        </div>
        {c.substreamEnabled && (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 10 }}>
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                  letterSpacing: 1,
                  textTransform: 'uppercase',
                  marginBottom: 6,
                }}
              >
                RESOLUTION
              </div>
              <Segmented
                options={['320×180', '640×360', '1280×720'] as const}
                value={c.substreamRes as '320×180' | '640×360' | '1280×720'}
                onChange={(v) => patch({ substreamRes: v })}
                size="sm"
              />
            </div>
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                  letterSpacing: 1,
                  textTransform: 'uppercase',
                  marginBottom: 6,
                }}
              >
                FRAMERATE
              </div>
              <Segmented
                options={['5', '10', '15', '30'] as const}
                value={String(c.substreamFps) as '5' | '10' | '15' | '30'}
                onChange={(v) => patch({ substreamFps: +v })}
                size="sm"
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function RecordingTab({ c, patch }: TabProps) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div>
        <SectionHeader>MODE</SectionHeader>
        <div style={{ marginTop: 10 }}>
          <Segmented
            options={['CONTINUOUS', 'EVENT', 'CONTINUOUS+EVENT', 'SCHEDULE'] as const}
            value={c.recordingMode as 'CONTINUOUS' | 'EVENT' | 'CONTINUOUS+EVENT' | 'SCHEDULE'}
            onChange={(v) => patch({ recordingMode: v })}
            size="sm"
          />
        </div>
      </div>
      <div>
        <SectionHeader>QUALITY</SectionHeader>
        <div style={{ marginTop: 10 }}>
          <Segmented
            options={['LOW', 'MEDIUM', 'HIGH', 'BEST'] as const}
            value={c.quality as 'LOW' | 'MEDIUM' | 'HIGH' | 'BEST'}
            onChange={(v) => patch({ quality: v })}
            size="sm"
          />
        </div>
      </div>
      <div>
        <SectionHeader>BUFFER WINDOW</SectionHeader>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 10 }}>
          <SliderField
            label={`PRE-BUFFER · ${c.prebuffer}s`}
            value={c.prebuffer}
            min={0}
            max={30}
            onChange={(v) => patch({ prebuffer: v })}
          />
          <SliderField
            label={`POST-BUFFER · ${c.postbuffer}s`}
            value={c.postbuffer}
            min={0}
            max={60}
            onChange={(v) => patch({ postbuffer: v })}
          />
        </div>
      </div>
      <div>
        <SectionHeader>RETENTION OVERRIDE</SectionHeader>
        <div style={{ marginTop: 10 }}>
          <SliderField
            label={`${c.retention} DAYS · default inherits 14d from MS`}
            value={c.retention}
            min={1}
            max={90}
            onChange={(v) => patch({ retention: v })}
          />
        </div>
      </div>
      <div
        style={{
          padding: 12,
          background: 'var(--bg-tertiary)',
          border: '1px solid var(--border)',
          borderRadius: 4,
          display: 'grid',
          gridTemplateColumns: 'repeat(3,1fr)',
          gap: 10,
        }}
      >
        <KV k="EST. DAILY" v="3.4 GB" />
        <KV k="EST. WEEKLY" v="23.8 GB" />
        <KV k="DISK IMPACT" v="+1.7%" />
      </div>
    </div>
  );
}

function MotionTab({ c, patch }: TabProps) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <SectionHeader>MOTION DETECTION</SectionHeader>
          <div
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              marginTop: 4,
              letterSpacing: 0.5,
            }}
          >
            Server-side analysis on substream
          </div>
        </div>
        <Toggle on={c.motionEnabled} onChange={(v) => patch({ motionEnabled: v })} />
      </div>

      {c.motionEnabled && (
        <>
          <div>
            <SectionHeader>SENSITIVITY</SectionHeader>
            <div style={{ marginTop: 10 }}>
              <SliderField
                label={`${c.motionSensitivity}% · ${c.motionSensitivity > 75 ? 'AGGRESSIVE' : c.motionSensitivity > 40 ? 'BALANCED' : 'LOW'}`}
                value={c.motionSensitivity}
                min={0}
                max={100}
                onChange={(v) => patch({ motionSensitivity: v })}
              />
            </div>
          </div>

          <div>
            <SectionHeader>DETECTION ZONES · {c.motionZones.length}</SectionHeader>
            <div
              style={{
                marginTop: 10,
                position: 'relative',
                aspectRatio: '16/9',
                background: 'linear-gradient(135deg, #1a1410, #0d0c0a)',
                overflow: 'hidden',
              }}
            >
              <Brackets />
              <div
                style={{
                  position: 'absolute',
                  inset: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <Icon name="video" style={{ width: 32, height: 32, color: 'rgba(249,115,22,0.25)' }} />
              </div>
              <svg viewBox="0 0 100 56" style={{ position: 'absolute', inset: 0, width: '100%', height: '100%' }}>
                <defs>
                  <pattern id="zonegrid" width="5" height="5" patternUnits="userSpaceOnUse">
                    <path
                      d="M 5 0 L 0 0 0 5"
                      fill="none"
                      stroke="rgba(249,115,22,0.08)"
                      strokeWidth="0.2"
                    />
                  </pattern>
                </defs>
                <rect width="100" height="56" fill="url(#zonegrid)" />
                {c.motionZones.map(
                  (z) =>
                    z.enabled && (
                      <g key={z.id}>
                        <rect
                          x={z.x}
                          y={z.y * 0.56}
                          width={z.w}
                          height={z.h * 0.56}
                          fill="rgba(249,115,22,0.18)"
                          stroke="#F97316"
                          strokeWidth="0.4"
                          strokeDasharray="0.6 0.6"
                        />
                        <text
                          x={z.x + 1}
                          y={z.y * 0.56 + 2.5}
                          fill="#F97316"
                          fontSize="2"
                          fontFamily="var(--font-mono)"
                          letterSpacing="0.1"
                        >
                          {z.name.toUpperCase()}
                        </text>
                      </g>
                    ),
                )}
              </svg>
              <div
                style={{
                  position: 'absolute',
                  bottom: 6,
                  right: 8,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 9,
                  color: 'rgba(255,255,255,0.6)',
                }}
              >
                CLICK + DRAG TO DRAW · NOT IN PROTOTYPE
              </div>
            </div>
            <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 4 }}>
              {c.motionZones.map((z) => (
                <div
                  key={z.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 10,
                    padding: 8,
                    background: 'var(--bg-tertiary)',
                    border: '1px solid var(--border)',
                    borderRadius: 3,
                  }}
                >
                  <span
                    style={{
                      width: 10,
                      height: 10,
                      background: z.enabled ? '#F97316' : 'var(--text-muted)',
                      borderRadius: 1,
                    }}
                  />
                  <span
                    style={{
                      flex: 1,
                      fontFamily: 'var(--font-sans)',
                      fontSize: 12,
                      color: 'var(--text-primary)',
                    }}
                  >
                    {z.name}
                  </span>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                    {z.w}×{z.h}%
                  </span>
                  <Toggle
                    on={z.enabled}
                    onChange={(v) =>
                      patch({
                        motionZones: c.motionZones.map((x) =>
                          x.id === z.id ? { ...x, enabled: v } : x,
                        ),
                      })
                    }
                  />
                </div>
              ))}
              <Btn
                kind="ghost"
                icon="plus"
                size="sm"
                onClick={() =>
                  patch({
                    motionZones: [
                      ...c.motionZones,
                      {
                        id: Date.now(),
                        name: `Zone ${c.motionZones.length + 1}`,
                        x: 30,
                        y: 30,
                        w: 30,
                        h: 30,
                        enabled: true,
                      },
                    ],
                  })
                }
              >
                Add zone
              </Btn>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function EventsTab({ c, patch }: TabProps) {
  type EventKey = keyof CameraConfig['events'];
  const events: { k: EventKey; l: string; d: string }[] = [
    { k: 'motion', l: 'Motion Detection', d: 'Standard pixel-difference motion' },
    { k: 'tampering', l: 'Tampering', d: 'Lens cover, defocus, scene change' },
    { k: 'lineCrossing', l: 'Line Crossing', d: 'Virtual tripwire (ONVIF rule)' },
    { k: 'audioDetection', l: 'Audio Detection', d: 'Loud noise / glass break' },
    { k: 'objectDetection', l: 'Object Detection', d: 'On-camera AI: person, vehicle' },
  ];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div
        style={{
          padding: 12,
          background: 'rgba(249,115,22,0.05)',
          border: '1px solid rgba(249,115,22,0.27)',
          borderRadius: 4,
          display: 'flex',
          gap: 10,
          alignItems: 'flex-start',
        }}
      >
        <Icon name="info" style={{ width: 14, height: 14, color: '#F97316', marginTop: 2 }} />
        <div
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            color: 'var(--text-secondary)',
            lineHeight: 1.6,
          }}
        >
          ONVIF events arrive via PullPoint subscription. Events flagged here are forwarded to the management
          server and trigger event-mode recording.
        </div>
      </div>
      {events.map((e) => (
        <div
          key={e.k}
          style={{
            padding: 12,
            background: 'var(--bg-tertiary)',
            border: '1px solid var(--border)',
            borderRadius: 4,
            display: 'flex',
            alignItems: 'center',
            gap: 12,
          }}
        >
          <Icon name="zap" style={{ width: 14, height: 14, color: c.events[e.k] ? '#F97316' : 'var(--text-muted)' }} />
          <div style={{ flex: 1 }}>
            <div
              style={{
                fontFamily: 'var(--font-sans)',
                fontSize: 13,
                color: 'var(--text-primary)',
                fontWeight: 500,
              }}
            >
              {e.l}
            </div>
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                marginTop: 2,
              }}
            >
              {e.d}
            </div>
          </div>
          <Toggle on={c.events[e.k]} onChange={(v) => patch({ events: { ...c.events, [e.k]: v } })} />
        </div>
      ))}
    </div>
  );
}

function AdvancedTab({ c, patch }: TabProps) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div>
        <SectionHeader>TIME SYNC</SectionHeader>
        <div style={{ marginTop: 10 }}>
          <Segmented
            options={['NTP', 'CAMERA', 'RECORDER'] as const}
            value={c.timesync as 'NTP' | 'CAMERA' | 'RECORDER'}
            onChange={(v) => patch({ timesync: v })}
            size="sm"
          />
          <div
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              marginTop: 6,
            }}
          >
            Source for camera clock — NTP recommended for forensic accuracy.
          </div>
        </div>
      </div>
      <div
        style={{
          padding: 12,
          background: 'var(--bg-tertiary)',
          border: '1px solid var(--border)',
          borderRadius: 4,
          display: 'flex',
          flexDirection: 'column',
          gap: 10,
        }}
      >
        <Toggle
          on={c.osdOverlay}
          onChange={(v) => patch({ osdOverlay: v })}
          label="ON-SCREEN DISPLAY (TIMESTAMP, NAME)"
        />
        <Toggle on={c.audioRecord} onChange={(v) => patch({ audioRecord: v })} label="RECORD AUDIO TRACK" />
        <Toggle on={c.onvif!} onChange={(v) => patch({ onvif: v })} label="ONVIF PROFILE S" />
      </div>
      <div>
        <SectionHeader>DEVICE INFO (READ-ONLY)</SectionHeader>
        <div style={{ marginTop: 10, display: 'grid', gridTemplateColumns: 'repeat(2,1fr)', gap: 10 }}>
          <KV k="VENDOR" v={c.vendor!} />
          <KV k="MAC ADDRESS" v={c.mac || '00:00:00:00:00:00'} />
          <KV k="FIRMWARE" v="V5.7.2 BUILD 230914" />
          <KV k="SERIAL" v={c.id + '-' + (c.mac || 'XXXXXX').slice(-6)} />
        </div>
      </div>
      <div>
        <SectionHeader>DANGER ZONE</SectionHeader>
        <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
          <Btn kind="secondary" icon="rotate-ccw">
            Reboot Camera
          </Btn>
          <Btn kind="secondary">Reset to Camera Defaults</Btn>
        </div>
      </div>
    </div>
  );
}

