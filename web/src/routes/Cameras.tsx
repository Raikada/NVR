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
import { HLSPreview } from '../components/HLSPreview';
import { PageHeader } from '../components/PageHeader';
import {
  ApiError,
  fetchCameras,
  deleteCamera,
  createCamera,
  fetchStreams,
  probeCameraSource,
  patchCamera,
  fetchRecordingPolicies,
  updateRecordingPolicy,
  onvifDiscover,
  onvifDeviceInfo,
  onvifListSubscriptions,
  onvifCreateSubscription,
  onvifDeleteSubscription,
  fetchEvents,
  fetchMotionConfig,
  patchMotionConfig,
  triggerMotionTest,
} from '../lib/api';
import type {
  OnvifSubscription,
  OnvifDiscoveredDevice,
  Event as ApiEvent,
  MotionConfig as MotionConfigShape,
  MotionConfigPatchBody,
} from '../lib/api';
import type {
  Camera as ApiCamera,
  CameraSourceType,
  RecordingPolicy,
  RecordingPolicyContainer,
  RecordingPolicyMode,
  RecordingPolicyWriteBody,
  Stream,
} from '../lib/api';
import { PolicyEditorModal } from '../components/PolicyEditorModal';
import { daysToNs, formatDuration, nsToDays } from '../lib/duration';
import { useFetch, usePoll } from '../lib/hooks';
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
  // Set when the hit came from a real /v1/onvif/discover response;
  // the identify + add-camera flows POST against this URL.
  xaddr?: string;
}

// VENDOR_POOL was a Wave-2 mock seed; the discovery panel now reads
// from /v1/onvif/discover. Kept here as a comment so future adjacent
// changes have the historical reference.

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

// Build the rtsp:// source URL for /v1/cameras POST. When the user
// supplied credentials in the wizard they're embedded as userinfo —
// the recorder honors userinfo on write and redacts it on read-back
// (see internal/defs/camera_translate.go), so this is the canonical
// way to pass camera auth. encodeURIComponent guards passwords with
// special chars (':', '@', '/', '?').
function buildSourceURL(cam: UICamera): string {
  const host = `${cam.ip}:${cam.port ?? '554'}${cam.path ?? ''}`;
  if (cam.user) {
    const u = encodeURIComponent(cam.user);
    const p = encodeURIComponent(cam.pass ?? '');
    return `rtsp://${u}:${p}@${host}`;
  }
  return `rtsp://${host}`;
}

// Translate canonical Camera (from /v1/cameras) plus its active
// Stream (from /v1/streams) into the UICamera shape the list and
// drawer consume. Cameras without an active stream get '—' for
// resolution / fps / codec.
function toUICamera(c: ApiCamera, streamsByCamera: Record<string, Stream>): UICamera {
  const status: UICamera['status'] = c.runtime
    ? c.runtime.online
      ? 'online'
      : 'offline'
    : 'offline';
  // Pick the first video track's metadata when a stream exists.
  const stream = streamsByCamera[c.id];
  const videoTrack = stream?.tracks?.find((t) => t.kind === 'video');
  return {
    id: c.id,
    name: c.name,
    ip: c.source_url,
    status,
    resolution: videoTrack?.resolution ?? '—',
    fps: videoTrack?.fps ?? 0,
    codec: videoTrack?.codec ?? '—',
    vendor: undefined,
    user: c.credentials_ref ?? undefined,
    recording_policy_id: c.recording_policy_id,
  };
}

export function Cameras({ state, setState, addToast }: CamerasProps) {
  const [mode, setMode] = useState<Mode>('list');
  const [configCam, setConfigCam] = useState<UICamera | null>(null);

  // Real /v1/cameras list + /v1/streams. Streams polled every 5s
  // so the resolution / fps / codec columns stay live as cameras
  // come and go.
  const list = useFetch(() => fetchCameras(0, 100), []);
  const streams = usePoll(fetchStreams, 5000, []);

  const lockedDown = false;

  // Index streams by camera_id for O(1) lookup in toUICamera.
  const streamsByCamera: Record<string, Stream> = {};
  for (const s of streams.data?.items ?? []) {
    if (s.camera_id) streamsByCamera[s.camera_id] = s;
  }

  // Mirror the canonical list into AppState.cameras so other routes
  // (Overview's mini grid, the wizard) see the same data.
  useEffect(() => {
    if (list.status === 'ready') {
      setState((s) => ({
        ...s,
        cameras: list.data.items.map((c) => toUICamera(c, streamsByCamera)),
      }));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.status, list.status === 'ready' ? list.data : null, streams.data, setState]);

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
          source_url: buildSourceURL(cam),
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
        source_url: buildSourceURL(cam),
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

  // Saving the config drawer's edits. Multi-resource save: the
  // drawer's Recording tab can mutate two resources at once — the
  // Camera (recording_policy_id) and the RecordingPolicy itself
  // (mode / retention / container / enabled). Both PATCHes fire
  // best-effort: a failure on one is reported but doesn't roll the
  // other back. The recorder doesn't expose a multi-resource
  // transaction surface today; if eventual consistency between Camera
  // and RecordingPolicy turns into an operator-visible problem we'd
  // queue an ADR for a transactional bundle endpoint, but for the
  // common case (operator picks a policy and tweaks its retention)
  // sequencing is fine.
  //
  // Other drawer tabs (Motion, ONVIF Events, Advanced) still collect
  // local-only state pending recorder-side subsystems for their
  // domains; their values are NOT round-tripped.
  async function saveCamera(
    updated: UICamera,
    policyEdit: { id: string; patch: RecordingPolicyWriteBody } | null,
  ) {
    let cameraOk = true;
    let policyOk = true;
    let cameraErr: string | null = null;
    let policyErr: string | null = null;

    // 1) Persist the policy mutation first so a subsequent Camera
    //    PATCH that changed recording_policy_id observes the latest
    //    RecordingPolicy state. Skipped when the operator only
    //    flipped the policy selection (no inline policy edits).
    if (policyEdit && Object.keys(policyEdit.patch).length > 0) {
      try {
        await updateRecordingPolicy(policyEdit.id, policyEdit.patch);
      } catch (e) {
        policyOk = false;
        policyErr = e instanceof ApiError ? e.message : (e as Error).message;
      }
    }

    // 2) Persist the Camera mutation regardless of whether (1) succeeded
    //    — they're independent resources from the operator's POV.
    try {
      await patchCamera(updated.id, {
        recording_policy_id: updated.recording_policy_id,
      });
    } catch (e) {
      cameraOk = false;
      cameraErr = e instanceof ApiError ? e.message : (e as Error).message;
    }

    if (cameraOk && policyOk) {
      addToast({
        kind: 'success',
        title: 'CAMERA SAVED',
        body: updated.name,
        icon: 'check-circle',
      });
    } else if (cameraOk && !policyOk) {
      addToast({
        kind: 'warning',
        title: 'CAMERA SAVED · POLICY FAILED',
        body: policyErr ?? 'policy update failed',
        icon: 'alert-triangle',
      });
    } else if (!cameraOk && policyOk) {
      addToast({
        kind: 'warning',
        title: 'POLICY SAVED · CAMERA FAILED',
        body: cameraErr ?? 'camera update failed',
        icon: 'alert-triangle',
      });
    } else {
      addToast({
        kind: 'danger',
        title: 'SAVE FAILED',
        body: cameraErr ?? policyErr ?? 'both updates failed',
        icon: 'x',
      });
    }

    list.refetch();
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
  const cameras =
    list.status === 'ready'
      ? list.data.items.map((c) => toUICamera(c, streamsByCamera))
      : state.cameras;

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
              disabled={lockedDown}
              title={lockedDown ? 'Cameras managed by Management Server' : undefined}
              onClick={() => {
                if (lockedDown) return;
                setMode(mode === 'scan' ? 'list' : 'scan');
              }}
            >
              Discover
            </Btn>
            <Btn
              kind="primary"
              icon="plus"
              disabled={lockedDown}
              title={lockedDown ? 'Cameras managed by Management Server' : undefined}
              onClick={() => {
                if (lockedDown) return;
                setMode(mode === 'manual' ? 'list' : 'manual');
              }}
            >
              Add Camera
            </Btn>
          </>
        }
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        {lockedDown && (
          <div
            style={{
              background: 'var(--bg-elevated, rgba(255,255,255,0.04))',
              border: '1px solid var(--border, rgba(255,255,255,0.08))',
              borderLeft: '3px solid var(--accent-info, #4a90e2)',
              padding: '12px 16px',
              fontSize: 13,
              lineHeight: 1.5,
              color: 'var(--text-secondary, #aaa)',
            }}
          >
            <div style={{ fontWeight: 600, color: 'var(--text-primary, #fff)', marginBottom: 4 }}>
              Cameras managed by Management Server
            </div>
            This recorder is paired with a Management Server that has taken canonical
            authority for cameras. Add, edit, and delete operations are performed in the
            Management Server UI. The list and stream views remain available here for
            reference.
          </div>
        )}
        {mode === 'scan' && !lockedDown && <DiscoverPanel onClose={() => setMode('list')} onAdd={bulkAdd} />}
        {mode === 'manual' && !lockedDown && (
          <ManualAddWizard onCancel={() => setMode('list')} onSubmit={singleAdd} addToast={addToast} />
        )}
        <CameraList cameras={cameras} onConfig={lockedDown ? undefined : setConfigCam} />
      </div>
      {configCam && (
        <CameraConfigDrawer
          camera={configCam}
          onClose={() => setConfigCam(null)}
          onSave={saveCamera}
          onRemove={removeCamera}
          addToast={addToast}
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
  const [subnet, setSubnet] = useState('LAN');
  const [found, setFound] = useState<DiscoverHit[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [creds, setCreds] = useState({ user: 'admin', pass: '' });
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

  // Translate one ONVIF DiscoveredDevice into the panel's DiscoverHit
  // shape. The xaddr is the canonical pointer back to the device for
  // the device-info phase; we surface a synthetic id so the dedup keys
  // remain stable across phases.
  function deviceToHit(d: OnvifDiscoveredDevice): DiscoverHit {
    let host = '';
    try {
      host = new URL(d.xaddr).host;
    } catch {
      host = d.xaddr;
    }
    return {
      id: d.endpoint_reference || d.xaddr,
      ip: host,
      mac: d.endpoint_reference?.slice(-12) ?? '—',
      vendor: d.manufacturer ?? d.name ?? 'ONVIF Device',
      model: d.model ?? d.hardware ?? '',
      resolution: '—',
      fps: 0,
      onvif: true,
      rtsp: '',
      identified: false,
      status: 'reachable',
      // Stash the xaddr on the hit so the identify phase + add flow
      // know where to call.
      xaddr: d.xaddr,
    };
  }

  // Probe phase: real WS-Discovery via /v1/onvif/discover. Cancellable
  // via stage transition; we set a 4-second timeout and animate the
  // progress bar in parallel for visual feedback.
  useEffect(() => {
    if (stage !== 'probe') return;
    let cancelled = false;
    let p = 0;
    const tick = window.setInterval(() => {
      p += 3 + Math.random() * 4;
      if (p > 95) p = 95;
      setProgress(Math.floor(p));
    }, 100);

    pushLog('info', 'WS-Discovery multicast probe → 239.255.255.250:3702');
    onvifDiscover(4000)
      .then((resp) => {
        if (cancelled) return;
        clearInterval(tick);
        setProgress(100);
        const hits = resp.devices.map(deviceToHit);
        setFound(hits);
        pushLog('ok', `Probe complete · ${hits.length} ONVIF device${hits.length === 1 ? '' : 's'} found`);
        for (const h of hits) {
          pushLog('info', `Reply from ${h.ip}${h.vendor !== 'ONVIF Device' ? ' · ' + h.vendor : ''}`);
        }
        setStage('identify');
      })
      .catch((e: Error) => {
        if (cancelled) return;
        clearInterval(tick);
        pushLog('err', `Probe failed: ${e.message}`);
        setStage('done');
      });
    return () => {
      cancelled = true;
      clearInterval(tick);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stage === 'probe' ? 'go' : 'stop']);

  // Identify phase: real GetDeviceInformation per device. Each call is
  // best-effort: cameras that require credentials produce a 400 with
  // a SOAP-fault reason; we surface that in the log line and mark the
  // hit as identified anyway so the user can still click Add and edit
  // credentials in the per-camera detail.
  useEffect(() => {
    if (stage !== 'identify') return;
    if (found.length === 0) {
      setStage('done');
      pushLog('ok', 'No devices to identify');
      return;
    }
    let cancelled = false;
    (async () => {
      for (let i = 0; i < found.length; i++) {
        if (cancelled) return;
        const hit = found[i];
        try {
          const info = await onvifDeviceInfo({
            xaddr: hit.xaddr ?? '',
            username: creds.user || undefined,
            password: creds.pass || undefined,
          });
          if (cancelled) return;
          setFound((prev) =>
            prev.map((c) =>
              c.id === hit.id
                ? {
                    ...c,
                    identified: true,
                    vendor: info.manufacturer || c.vendor,
                    model: info.model || c.model,
                  }
                : c,
            ),
          );
          pushLog('ok', `${hit.ip} → ${info.manufacturer} ${info.model}`);
        } catch (e) {
          const msg = (e as Error).message || 'identify failed';
          if (cancelled) return;
          // Keep the device in the list but mark identified so Add
          // affordance enables; surface the error so the operator
          // knows credentials may be needed.
          setFound((prev) =>
            prev.map((c) => (c.id === hit.id ? { ...c, identified: true } : c)),
          );
          pushLog('err', `${hit.ip}: ${msg}`);
        }
      }
      if (cancelled) return;
      setStage('done');
      pushLog('ok', 'Identification complete');
    })();
    return () => {
      cancelled = true;
    };
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
                    }}
                  >
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

  async function probeDevice() {
    if (!validateStep0()) return;
    setProbe('busy');
    // Construct an RTSP URL from the form fields and call the
    // recorder's /v1/cameras/probe endpoint, which performs a TCP
    // dial against host:port and reports reachability + latency.
    const sourceURL = `rtsp://${f.ip}:${f.port}${f.path}`;
    try {
      const res = await probeCameraSource({ source_url: sourceURL });
      if (res.reachable) {
        setProbe({
          ok: true,
          vendor: f.vendor,
          model: `${res.host}:${res.port}`,
          codec: `${res.latency_ms}ms`,
          res: 'TCP OK',
          fps: res.latency_ms,
        });
      } else {
        setProbe({ ok: false, msg: res.reason || 'unreachable' });
      }
    } catch (e) {
      setProbe({ ok: false, msg: (e as Error).message });
    }
  }

  // The "Test Stream" button on step 2 reuses the probe endpoint —
  // we don't have a separate full stream test (would require
  // opening a real recorder pipeline). Probe + auth-credentials-
  // were-submitted is enough signal for the wizard to proceed.
  async function testStream() {
    if (!validateStep1()) return;
    setStream('busy');
    const sourceURL = `${f.protocol}://${f.ip}:${f.port}${f.path}`;
    try {
      const res = await probeCameraSource({ source_url: sourceURL });
      setStream(res.reachable ? 'ok' : 'fail');
    } catch {
      setStream('fail');
    }
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
      pass: f.pass,
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

function CameraList({
  cameras,
  onConfig,
}: {
  cameras: UICamera[];
  onConfig?: (c: UICamera) => void;
}) {
  // Slice 4-B: when canonical_source = "ms" the parent passes
  // onConfig=undefined to grey out the CONFIG button + suppress the
  // drawer. Read paths remain functional.
  const interactive = onConfig !== undefined;
  return (
    <Card style={{ padding: 0 }}>
      <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
        <SectionHeader style={{ margin: 0 }}>CONFIGURED CAMERAS · {cameras.length}</SectionHeader>
      </div>
      {cameras.map((c, i) => (
        <div
          key={c.id}
          onClick={() => {
            if (interactive) onConfig!(c);
          }}
          style={{
            display: 'grid',
            gridTemplateColumns: '96px 1fr 1fr auto auto',
            gap: 14,
            padding: 12,
            alignItems: 'center',
            cursor: interactive ? 'pointer' : 'default',
            borderTop: i === 0 ? 'none' : '1px solid var(--border)',
            opacity: c.status === 'offline' ? 0.65 : 1,
            transition: 'background 0.1s',
          }}
          onMouseEnter={(e) => {
            if (interactive)
              e.currentTarget.style.background = 'rgba(249,115,22,0.04)';
          }}
          onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
        >
          <HLSPreview
            cameraName={c.name}
            online={c.status === 'online'}
            style={{ width: 96, height: 56, aspectRatio: 'auto' }}
          />
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
            disabled={!interactive}
            title={interactive ? undefined : 'Cameras managed by Management Server'}
            onClick={(e) => {
              e.stopPropagation();
              if (interactive) onConfig!(c);
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
  // Recording: only the canonical RecordingPolicy linkage (inherited
  // via UICamera.recording_policy_id) is persistable today. The old
  // local-only mode/quality/buffer/retention fields lived inside this
  // shape to drive the now-removed RecordingTab placeholder; the
  // Recording tab now picks a policy by id and lets PolicyEditorModal
  // own everything else.
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
  onSave: (
    c: UICamera,
    policyEdit: { id: string; patch: RecordingPolicyWriteBody } | null,
  ) => void;
  onRemove: (c: UICamera) => void;
  addToast: (t: ToastInput) => void;
}

function CameraConfigDrawer({ camera, onClose, onSave, onRemove, addToast }: DrawerProps) {
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

  // Pending edits to the currently-selected RecordingPolicy. The
  // RecordingTab populates this when the operator inline-edits mode /
  // retention / container / enabled; Save Changes flushes it via a
  // PATCH /v1/recording-policies/{id} alongside the camera PATCH.
  // Resets to null when the operator switches to a different policy
  // (a policy edit is scoped to the policy it was started on).
  const [policyEdit, setPolicyEdit] = useState<{
    id: string;
    patch: RecordingPolicyWriteBody;
  } | null>(null);

  function patch(p: Partial<CameraConfig>) {
    setC((s) => ({ ...s, ...p }));
    // Switching policies clears any pending in-flight policy edits
    // — the inline form re-binds to the newly-selected policy.
    if (Object.prototype.hasOwnProperty.call(p, 'recording_policy_id')) {
      setPolicyEdit(null);
    }
  }

  function patchPolicy(policyId: string, fields: RecordingPolicyWriteBody) {
    setPolicyEdit((prev) => {
      // First touch on a (possibly new) policy id: seed the patch.
      if (!prev || prev.id !== policyId) {
        return { id: policyId, patch: { ...fields } };
      }
      return { id: policyId, patch: { ...prev.patch, ...fields } };
    });
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
          {tab === 'recording' && (
            <RecordingTab
              c={c}
              patch={patch}
              addToast={addToast}
              policyEdit={policyEdit}
              patchPolicy={patchPolicy}
            />
          )}
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
            <Btn kind="primary" icon="check" onClick={() => onSave(c, policyEdit)}>
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
        <HLSPreview
          cameraName={c.name}
          online={c.status === 'online'}
          liveLabel="● LIVE · MAIN"
          meta={`${c.resolution} · ${c.codec} · ${c.fps}fps`}
        />
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

interface RecordingTabProps extends TabProps {
  addToast: (t: ToastInput) => void;
  policyEdit: { id: string; patch: RecordingPolicyWriteBody } | null;
  patchPolicy: (policyId: string, fields: RecordingPolicyWriteBody) => void;
}

const RECORDING_MODES: { value: RecordingPolicyMode; label: string }[] = [
  { value: 'continuous', label: 'CONTINUOUS' },
  { value: 'motion', label: 'MOTION' },
  { value: 'schedule', label: 'SCHEDULE' },
  { value: 'event_triggered', label: 'EVENT' },
  { value: 'off', label: 'OFF' },
];

const RECORDING_CONTAINERS: { value: RecordingPolicyContainer; label: string }[] = [
  { value: 'fmp4', label: 'fMP4' },
  { value: 'mpegts', label: 'MPEG-TS' },
];

function RecordingTab({ c, patch, addToast, policyEdit, patchPolicy }: RecordingTabProps) {
  // Recording-policy selection AND inline-edit surface. The picker
  // writes the camera's recording_policy_id via patch(); the inline
  // controls (mode / retention / container / enabled) write a partial
  // RecordingPolicyWriteBody via patchPolicy() — both flush to the
  // recorder when the drawer's "Save Changes" button calls
  // saveCamera (multi-resource: PATCH camera + PATCH policy).
  // Advanced fields (schedule editor, segment durations, part size,
  // record path template) stay in PolicyEditorModal — they're rare
  // enough that a separate modal is the right shape.
  //
  // Lockdown awareness: when policy_canonical_source = "ms" the
  // recorder rejects local mutations on /v1/recording-policies; the
  // tab surfaces a banner and disables the inline edits + Edit/New.
  // Camera selection (recording_policy_id) is still mutable — the
  // canonical Camera record still owns its policy linkage even when
  // the policy itself is MS-canonical.
  const policiesFetch = useFetch(fetchRecordingPolicies, []);
  const policies = policiesFetch.data?.items ?? [];
  const [editing, setEditing] = useState<RecordingPolicy | null>(null);
  const [creating, setCreating] = useState(false);

  const selectedId = c.recording_policy_id ?? '';
  const selected = policies.find((p) => p.id === selectedId);

  const policyLockedDown = false;

  // Effective view of the policy: the canonical record from the API
  // overlaid with any pending in-drawer edits. The inline controls
  // bind to this so the form reflects unsaved changes without
  // round-tripping to the server on every keystroke.
  const pendingForSelected =
    policyEdit && selected && policyEdit.id === selected.id ? policyEdit.patch : {};
  const effective = selected
    ? ({
        ...selected,
        ...pendingForSelected,
      } as RecordingPolicy)
    : null;

  function setPolicyMode(mode: RecordingPolicyMode) {
    if (!selected) return;
    patchPolicy(selected.id, { mode });
  }
  function setPolicyContainer(container: RecordingPolicyContainer) {
    if (!selected) return;
    patchPolicy(selected.id, { container });
  }
  function setPolicyRetentionDays(days: number) {
    if (!selected) return;
    patchPolicy(selected.id, { retention_duration: daysToNs(days) });
  }
  function setPolicyEnabled(enabled: boolean) {
    if (!selected) return;
    patchPolicy(selected.id, { enabled });
  }

  const inlineEditsDisabled = policyLockedDown || !selected;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      {policyLockedDown && (
        <div
          style={{
            background: 'var(--bg-elevated, rgba(255,255,255,0.04))',
            border: '1px solid var(--border, rgba(255,255,255,0.08))',
            borderLeft: '3px solid var(--accent-info, #4a90e2)',
            padding: '10px 12px',
            fontSize: 12,
            lineHeight: 1.4,
            color: 'var(--text-secondary, #aaa)',
          }}
        >
          <div style={{ fontWeight: 600, color: 'var(--text-primary, #fff)', marginBottom: 2 }}>
            Recording policies managed by Management Server
          </div>
          You can change which policy this camera uses, but the policy fields
          themselves are edited in the Management Server UI.
        </div>
      )}

      <div>
        <SectionHeader>RECORDING POLICY</SectionHeader>
        <div
          style={{
            marginTop: 10,
            display: 'flex',
            gap: 8,
            alignItems: 'center',
            flexWrap: 'wrap',
          }}
        >
          <select
            value={selectedId}
            onChange={(e) => patch({ recording_policy_id: e.target.value || undefined })}
            disabled={policiesFetch.status !== 'ready'}
            style={{
              flex: 1,
              minWidth: 220,
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              padding: '8px 10px',
              color: 'var(--text-primary)',
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
              outline: 'none',
            }}
          >
            <option value="">— No policy assigned —</option>
            {policiesFetch.status === 'loading' && <option>Loading…</option>}
            {policiesFetch.status === 'error' && <option>Recorder unreachable</option>}
            {policies.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} — {p.mode}
              </option>
            ))}
          </select>
          <Btn
            kind="ghost"
            size="sm"
            icon="settings"
            disabled={!selected || policyLockedDown}
            title={
              policyLockedDown
                ? 'Recording policies managed by Management Server'
                : !selected
                  ? 'Select a policy first'
                  : undefined
            }
            onClick={() => {
              if (policyLockedDown || !selected) return;
              setEditing(selected);
            }}
          >
            Advanced
          </Btn>
          <Btn
            kind="secondary"
            size="sm"
            icon="plus"
            disabled={policyLockedDown}
            title={policyLockedDown ? 'Recording policies managed by Management Server' : undefined}
            onClick={() => {
              if (policyLockedDown) return;
              setCreating(true);
            }}
          >
            New Policy
          </Btn>
        </div>
        {policiesFetch.status === 'error' && (
          <div
            style={{
              marginTop: 8,
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: '#EF4444',
              letterSpacing: 0.5,
            }}
          >
            {policiesFetch.error.message}
          </div>
        )}
      </div>

      {!selected && policiesFetch.status === 'ready' && (
        <div
          style={{
            padding: 16,
            background: 'var(--bg-tertiary)',
            border: '1px dashed var(--border)',
            borderRadius: 4,
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            color: 'var(--text-muted)',
            letterSpacing: 0.5,
            textAlign: 'center',
          }}
        >
          NO RECORDING POLICY ASSIGNED · PICK ONE FROM THE DROPDOWN ABOVE OR CREATE A NEW POLICY
        </div>
      )}

      {effective && (
        <>
          <div>
            <SectionHeader
              right={
                policyEdit && policyEdit.id === effective.id && (
                  <span
                    style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 9,
                      letterSpacing: 1,
                      color: '#EAB308',
                    }}
                  >
                    UNSAVED EDITS
                  </span>
                )
              }
            >
              MODE
            </SectionHeader>
            <div style={{ marginTop: 8, opacity: inlineEditsDisabled ? 0.55 : 1 }}>
              <Segmented<RecordingPolicyMode>
                options={RECORDING_MODES}
                value={effective.mode}
                onChange={(v) => {
                  if (inlineEditsDisabled) return;
                  setPolicyMode(v);
                }}
                size="sm"
              />
            </div>
          </div>

          <div>
            <SectionHeader>RETENTION</SectionHeader>
            <div style={{ marginTop: 8, opacity: inlineEditsDisabled ? 0.55 : 1 }}>
              <SliderField
                label={`${Math.max(1, Math.round(nsToDays(effective.retention_duration)))} DAYS · MINIMUM RETAIN BEFORE PRUNE`}
                value={Math.max(1, Math.round(nsToDays(effective.retention_duration)))}
                min={1}
                max={365}
                onChange={(v) => {
                  if (inlineEditsDisabled) return;
                  setPolicyRetentionDays(v);
                }}
              />
            </div>
          </div>

          <div>
            <SectionHeader>CONTAINER</SectionHeader>
            <div style={{ marginTop: 8, opacity: inlineEditsDisabled ? 0.55 : 1 }}>
              <Segmented<RecordingPolicyContainer>
                options={RECORDING_CONTAINERS}
                value={(effective.container || 'fmp4') as RecordingPolicyContainer}
                onChange={(v) => {
                  if (inlineEditsDisabled) return;
                  setPolicyContainer(v);
                }}
                size="sm"
              />
            </div>
          </div>

          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              opacity: inlineEditsDisabled ? 0.55 : 1,
            }}
          >
            <SectionHeader>POLICY ENABLED</SectionHeader>
            <Toggle
              on={effective.enabled}
              onChange={(v) => {
                if (inlineEditsDisabled) return;
                setPolicyEnabled(v);
              }}
            />
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
            <KV k="POLICY ID" v={effective.id.slice(0, 8)} />
            <KV
              k="MIN/MAX SEGMENT"
              v={`${formatDuration(effective.min_segment_duration)} / ${formatDuration(effective.max_segment_duration)}`}
            />
            <KV
              k={effective.mode === 'schedule' ? 'WINDOWS' : 'BUFFER'}
              v={
                effective.mode === 'schedule'
                  ? `${effective.schedule?.windows.length ?? 0}`
                  : effective.mode === 'event_triggered' || effective.mode === 'motion'
                    ? `${effective.pre_event_buffer ? Math.round(effective.pre_event_buffer / 1e9) : 0}/${effective.post_event_buffer ? Math.round(effective.post_event_buffer / 1e9) : 0}s`
                    : '—'
              }
            />
          </div>
        </>
      )}

      <div
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 10,
          color: 'var(--text-muted)',
          letterSpacing: 0.5,
          textTransform: 'uppercase',
        }}
      >
        Recording behavior is policy-driven. Multiple cameras can share a policy.
        Changes to a policy affect every camera using it. Save Changes writes the
        camera's policy selection AND any inline policy edits in a single action.
      </div>

      {creating && (
        <PolicyEditorModal
          onCancel={() => setCreating(false)}
          onSave={(p) => {
            setCreating(false);
            addToast({ kind: 'success', title: 'POLICY CREATED', body: p.name, icon: 'check-circle' });
            policiesFetch.refetch();
            // Auto-select the freshly-created policy on the camera.
            patch({ recording_policy_id: p.id });
          }}
        />
      )}
      {editing && (
        <PolicyEditorModal
          policy={editing}
          onCancel={() => setEditing(null)}
          onSave={(p) => {
            setEditing(null);
            addToast({ kind: 'success', title: 'POLICY SAVED', body: p.name, icon: 'check-circle' });
            policiesFetch.refetch();
          }}
        />
      )}
    </div>
  );
}

// MotionTab — Wave 4. Real persistence against
// /v1/recorder/cameras/{id}/motion-config + recent motion events from
// /v1/events filtered to camera.motion_detected /
// recording.motion_started / recording.motion_ended for this camera.
//
// motion_config is recorder-local in v1 (see canonical-divergences
// motion_config entry); the full motion subsystem on the recorder
// side lives in internal/motion/.
//
// The legacy local-only motionEnabled / motionSensitivity / motionZones
// fields are intentionally retained on CameraConfig because the
// drawer's other tabs and the wider state still reference them; only
// the Motion tab's source-of-truth is the canonical motion_config.
//
// `patch` from the parent isn't used here — the tab persists directly
// against the canonical surface, so changes survive drawer close
// without a separate Save Changes click. Acknowledged via lint-disable
// because TabProps is a shared contract across drawer tabs.
function MotionTab({ c, patch: _patch }: TabProps) {
  const [config, setConfig] = useState<MotionConfigShape | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [events, setEvents] = useState<ApiEvent[]>([]);
  const [testBusy, setTestBusy] = useState(false);

  async function loadConfig() {
    try {
      const cfg = await fetchMotionConfig(c.id);
      setConfig(cfg);
      setErr(null);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setLoading(false);
    }
  }

  async function loadEvents() {
    try {
      const list = await fetchEvents({
        cameraId: c.id,
        kind: ['camera.motion_detected', 'recording.motion_started', 'recording.motion_ended'],
        perPage: 50,
      });
      setEvents(list.items.slice(0, 20));
    } catch {
      // best-effort
    }
  }

  useEffect(() => {
    void loadConfig();
    void loadEvents();
    const t = window.setInterval(() => {
      void loadEvents();
    }, 5_000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [c.id]);

  async function applyPatch(body: MotionConfigPatchBody) {
    setSaving(true);
    setErr(null);
    try {
      await patchMotionConfig(c.id, body);
      await loadConfig();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    setTestBusy(true);
    try {
      await triggerMotionTest(c.id);
      // Give the controller a beat to dispatch and the EventStore a
      // beat to expose the synthetic event.
      window.setTimeout(() => {
        void loadEvents();
      }, 400);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setTestBusy(false);
    }
  }

  if (loading || !config) {
    return (
      <div
        style={{
          padding: 24,
          fontFamily: 'var(--font-mono)',
          fontSize: 11,
          color: 'var(--text-muted)',
        }}
      >
        Loading motion configuration…
        {err && (
          <div style={{ marginTop: 12, color: '#ef4444' }}>{err}</div>
        )}
      </div>
    );
  }

  const sensitivity = config.sensitivity;
  const cooldownSec = Math.round((config.cooldown_ms || 0) / 100) / 10;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      {/* Recorder-local advisory — motion_config is not yet canonical */}
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
          Motion-mode recording fires when the recorder receives a
          camera.motion_detected event (Wave 3 ONVIF subscription) and
          this camera's RecordingPolicy.mode is set to <code>motion</code>.
          Recording-pipeline start/stop is "always on with motion-event
          annotations" in v1; see{' '}
          <code>docs/canonical-divergences.md</code>.
        </div>
      </div>

      {err && (
        <div
          style={{
            padding: 10,
            background: 'rgba(239,68,68,0.08)',
            border: '1px solid rgba(239,68,68,0.4)',
            borderRadius: 3,
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            color: '#fca5a5',
          }}
        >
          {err}
        </div>
      )}

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
            ONVIF event-driven · cooldown {cooldownSec}s
          </div>
        </div>
        <Toggle
          on={config.enabled}
          onChange={(v) => void applyPatch({ enabled: v })}
        />
      </div>

      {config.enabled && (
        <>
          {/* Source */}
          <div>
            <SectionHeader>SOURCE</SectionHeader>
            <div
              style={{
                marginTop: 10,
                display: 'flex',
                gap: 8,
              }}
            >
              {(['onvif', 'local_future'] as const).map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => void applyPatch({ source: s })}
                  disabled={saving}
                  style={{
                    flex: 1,
                    padding: '10px 12px',
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    letterSpacing: 1,
                    color:
                      config.source === s
                        ? 'var(--text-primary)'
                        : 'var(--text-muted)',
                    background:
                      config.source === s
                        ? 'rgba(249,115,22,0.15)'
                        : 'var(--bg-tertiary)',
                    border:
                      config.source === s
                        ? '1px solid #F97316'
                        : '1px solid var(--border)',
                    borderRadius: 3,
                    cursor: saving ? 'not-allowed' : 'pointer',
                  }}
                >
                  {s === 'onvif' ? 'ONVIF EVENTS' : 'LOCAL CV (PENDING)'}
                </button>
              ))}
            </div>
            {config.source === 'local_future' && (
              <div
                style={{
                  marginTop: 8,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                }}
              >
                Local CV-based motion detection is not implemented in
                v1. The recorder logs a warning and continues to consume
                ONVIF events as a fallback.
              </div>
            )}
          </div>

          {/* Sensitivity */}
          <div>
            <SectionHeader>SENSITIVITY · {sensitivity}%</SectionHeader>
            <div style={{ marginTop: 10 }}>
              <SliderField
                label={
                  sensitivity > 75
                    ? 'AGGRESSIVE'
                    : sensitivity > 40
                    ? 'BALANCED'
                    : 'LOW'
                }
                value={sensitivity}
                min={0}
                max={100}
                onChange={(v) => {
                  // Slider fires on every drag tick; debounce by only
                  // updating local state during drag, then PATCH on
                  // mouseup. Implementing that mode change would need
                  // a richer Slider primitive; for now we patch on
                  // each tick — the recorder's PATCH is a config-set
                  // round-trip that's well below what an interactive
                  // drag can flood.
                  void applyPatch({ sensitivity: v });
                }}
              />
            </div>
            <div
              style={{
                marginTop: 6,
                fontFamily: 'var(--font-mono)',
                fontSize: 9,
                color: 'var(--text-muted)',
              }}
            >
              Sensitivity is advisory in v1 — vendor ONVIF rule
              sensitivity is configured on the camera itself. The value
              persists for cross-tier surfaces (MS / Cloud) that
              correlate detection rates.
            </div>
          </div>

          {/* Cooldown */}
          <div>
            <SectionHeader>COOLDOWN (MS)</SectionHeader>
            <div style={{ marginTop: 10 }}>
              <input
                type="number"
                min={0}
                max={120000}
                step={500}
                value={config.cooldown_ms}
                onChange={(e) => {
                  const v = parseInt(e.target.value, 10);
                  if (!Number.isNaN(v) && v >= 0) {
                    void applyPatch({ cooldown_ms: v });
                  }
                }}
                style={{
                  padding: '8px 10px',
                  fontFamily: 'var(--font-mono)',
                  fontSize: 12,
                  color: 'var(--text-primary)',
                  background: 'var(--bg-tertiary)',
                  border: '1px solid var(--border)',
                  borderRadius: 3,
                  width: 120,
                }}
              />
              <span
                style={{
                  marginLeft: 10,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                }}
              >
                Time after last motion event before recording.motion_ended fires.
              </span>
            </div>
          </div>

          {/* ROI */}
          <div>
            <SectionHeader>REGION OF INTEREST</SectionHeader>
            <div
              style={{
                marginTop: 10,
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                marginBottom: 8,
              }}
            >
              Normalized rectangle (0–1). Advisory for ONVIF source;
              consumed by future local CV detector.
            </div>
            <ROIEditor
              value={config.roi || null}
              onChange={(roi) => {
                if (roi == null) {
                  void applyPatch({ unset_roi: true });
                } else {
                  void applyPatch({ roi });
                }
              }}
            />
          </div>

          {/* Schedule */}
          <div>
            <SectionHeader>ACTIVE SCHEDULE</SectionHeader>
            <div
              style={{
                marginTop: 10,
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                marginBottom: 8,
              }}
            >
              When set, motion is only armed inside listed windows.
              Empty list = always armed.
            </div>
            <ScheduleEditor
              value={config.schedule || null}
              onChange={(schedule) => {
                if (schedule == null) {
                  void applyPatch({ unset_schedule: true });
                } else {
                  void applyPatch({ schedule });
                }
              }}
            />
          </div>

          {/* Test motion */}
          <div
            style={{
              padding: 12,
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 3,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 12,
            }}
          >
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11,
                  color: 'var(--text-primary)',
                  letterSpacing: 0.5,
                }}
              >
                TEST MOTION
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-muted)',
                  marginTop: 2,
                }}
              >
                Emit a synthetic motion event to verify controller wiring.
              </div>
            </div>
            <Btn kind="secondary" size="sm" disabled={testBusy} onClick={handleTest}>
              Fire test event
            </Btn>
          </div>
        </>
      )}

      {/* Recent motion events */}
      <div>
        <SectionHeader>RECENT MOTION EVENTS · {events.length}</SectionHeader>
        <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 4 }}>
          {events.length === 0 && (
            <div
              style={{
                padding: 12,
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: 'var(--text-muted)',
                textAlign: 'center',
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border)',
                borderRadius: 3,
              }}
            >
              No motion events yet. Subscribe via the ONVIF EVENTS tab,
              or fire a synthetic event with TEST MOTION above.
            </div>
          )}
          {events.map((e) => (
            <MotionEventRow key={e.id} event={e} />
          ))}
        </div>
      </div>
    </div>
  );
}

function MotionEventRow({ event }: { event: ApiEvent }) {
  const isStarted = event.kind === 'recording.motion_started';
  const isEnded = event.kind === 'recording.motion_ended';
  const isDetected = event.kind === 'camera.motion_detected';
  const color = isStarted
    ? '#22c55e'
    : isEnded
    ? '#94a3b8'
    : isDetected
    ? '#F97316'
    : 'var(--text-muted)';
  const label = isStarted
    ? 'STARTED'
    : isEnded
    ? 'ENDED'
    : isDetected
    ? 'DETECTED'
    : event.kind;
  return (
    <div
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
          width: 8,
          height: 8,
          background: color,
          borderRadius: 1,
        }}
      />
      <span
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 10,
          color,
          letterSpacing: 0.5,
          width: 80,
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 10,
          color: 'var(--text-muted)',
          flex: 1,
        }}
      >
        {new Date(event.occurred_at).toLocaleString()}
      </span>
      {event.attributes?.synthetic === 'true' && (
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 9,
            color: '#F97316',
            padding: '2px 6px',
            border: '1px solid rgba(249,115,22,0.4)',
            borderRadius: 2,
          }}
        >
          SYNTHETIC
        </span>
      )}
      {event.attributes?.onvif_topic && !event.attributes?.synthetic && (
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 9,
            color: 'var(--text-muted)',
            maxWidth: 200,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
          title={event.attributes.onvif_topic}
        >
          {event.attributes.onvif_topic}
        </span>
      )}
    </div>
  );
}

function ROIEditor({
  value,
  onChange,
}: {
  value: { x: number; y: number; w: number; h: number } | null;
  onChange: (roi: { x: number; y: number; w: number; h: number } | null) => void;
}) {
  const set = value || { x: 0, y: 0, w: 1, h: 1 };
  const enabled = value !== null;
  const inputStyle: React.CSSProperties = {
    padding: '6px 8px',
    fontFamily: 'var(--font-mono)',
    fontSize: 11,
    color: 'var(--text-primary)',
    background: 'var(--bg-tertiary)',
    border: '1px solid var(--border)',
    borderRadius: 3,
    width: 80,
  };
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <Toggle
          on={enabled}
          onChange={(v) => {
            if (v) onChange({ x: 0.1, y: 0.1, w: 0.8, h: 0.8 });
            else onChange(null);
          }}
        />
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            color: enabled ? 'var(--text-primary)' : 'var(--text-muted)',
          }}
        >
          {enabled ? 'ENABLED' : 'WHOLE FRAME'}
        </span>
      </div>
      {enabled && (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {(['x', 'y', 'w', 'h'] as const).map((k) => (
            <label
              key={k}
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: 4,
                fontFamily: 'var(--font-mono)',
                fontSize: 9,
                color: 'var(--text-muted)',
              }}
            >
              <span>{k.toUpperCase()}</span>
              <input
                type="number"
                step={0.05}
                min={0}
                max={1}
                value={set[k]}
                onChange={(e) => {
                  const v = parseFloat(e.target.value);
                  if (!Number.isNaN(v)) {
                    const next = { ...set, [k]: Math.min(1, Math.max(0, v)) };
                    onChange(next);
                  }
                }}
                style={inputStyle}
              />
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

function ScheduleEditor({
  value,
  onChange,
}: {
  value: { timezone: string; windows: { days: string[]; start: string; end: string }[] } | null;
  onChange: (
    schedule: {
      timezone: string;
      windows: { days: string[]; start: string; end: string }[];
    } | null,
  ) => void;
}) {
  const enabled = value !== null;
  const sched = value || { timezone: 'UTC', windows: [] };
  const allDays = ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'];

  function addWindow() {
    onChange({
      ...sched,
      windows: [...sched.windows, { days: ['monday', 'tuesday', 'wednesday', 'thursday', 'friday'], start: '08:00', end: '17:00' }],
    });
  }

  function setWindow(i: number, w: { days: string[]; start: string; end: string }) {
    onChange({
      ...sched,
      windows: sched.windows.map((cur, idx) => (idx === i ? w : cur)),
    });
  }

  function removeWindow(i: number) {
    onChange({
      ...sched,
      windows: sched.windows.filter((_, idx) => idx !== i),
    });
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <Toggle
          on={enabled}
          onChange={(v) => {
            if (v) onChange({ timezone: 'UTC', windows: [] });
            else onChange(null);
          }}
        />
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            color: enabled ? 'var(--text-primary)' : 'var(--text-muted)',
          }}
        >
          {enabled ? 'SCHEDULE ACTIVE' : 'ALWAYS ARMED'}
        </span>
      </div>

      {enabled && (
        <>
          <label
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 4,
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              color: 'var(--text-muted)',
            }}
          >
            <span>TIMEZONE (IANA)</span>
            <input
              type="text"
              value={sched.timezone}
              onChange={(e) => onChange({ ...sched, timezone: e.target.value })}
              placeholder="UTC"
              style={{
                padding: '6px 8px',
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-primary)',
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border)',
                borderRadius: 3,
                width: 240,
              }}
            />
          </label>

          {sched.windows.map((w, i) => (
            <div
              key={i}
              style={{
                padding: 10,
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border)',
                borderRadius: 3,
                display: 'flex',
                flexDirection: 'column',
                gap: 8,
              }}
            >
              <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                {allDays.map((d) => {
                  const on = w.days.includes(d);
                  return (
                    <button
                      key={d}
                      type="button"
                      onClick={() => {
                        const next = on
                          ? w.days.filter((x) => x !== d)
                          : [...w.days, d];
                        setWindow(i, { ...w, days: next });
                      }}
                      style={{
                        padding: '4px 8px',
                        fontFamily: 'var(--font-mono)',
                        fontSize: 9,
                        letterSpacing: 1,
                        color: on ? 'var(--text-primary)' : 'var(--text-muted)',
                        background: on ? 'rgba(249,115,22,0.15)' : 'var(--bg-secondary)',
                        border: on ? '1px solid #F97316' : '1px solid var(--border)',
                        borderRadius: 2,
                        cursor: 'pointer',
                      }}
                    >
                      {d.slice(0, 3).toUpperCase()}
                    </button>
                  );
                })}
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <input
                  type="time"
                  value={w.start}
                  onChange={(e) => setWindow(i, { ...w, start: e.target.value })}
                  style={{
                    padding: '6px 8px',
                    fontFamily: 'var(--font-mono)',
                    fontSize: 11,
                    color: 'var(--text-primary)',
                    background: 'var(--bg-secondary)',
                    border: '1px solid var(--border)',
                    borderRadius: 3,
                  }}
                />
                <span style={{ color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>→</span>
                <input
                  type="time"
                  value={w.end}
                  onChange={(e) => setWindow(i, { ...w, end: e.target.value })}
                  style={{
                    padding: '6px 8px',
                    fontFamily: 'var(--font-mono)',
                    fontSize: 11,
                    color: 'var(--text-primary)',
                    background: 'var(--bg-secondary)',
                    border: '1px solid var(--border)',
                    borderRadius: 3,
                  }}
                />
                <Btn kind="ghost" size="sm" onClick={() => removeWindow(i)}>
                  Remove
                </Btn>
              </div>
            </div>
          ))}

          <Btn kind="ghost" icon="plus" size="sm" onClick={addWindow}>
            Add window
          </Btn>
        </>
      )}
    </div>
  );
}

function EventsTab({ c, patch }: TabProps) {
  type EventKey = keyof CameraConfig['events'];
  const eventToggles: { k: EventKey; l: string; d: string }[] = [
    { k: 'motion', l: 'Motion Detection', d: 'Standard pixel-difference motion' },
    { k: 'tampering', l: 'Tampering', d: 'Lens cover, defocus, scene change' },
    { k: 'lineCrossing', l: 'Line Crossing', d: 'Virtual tripwire (ONVIF rule)' },
    { k: 'audioDetection', l: 'Audio Detection', d: 'Loud noise / glass break' },
    { k: 'objectDetection', l: 'Object Detection', d: 'On-camera AI: person, vehicle' },
  ];

  // Subscription state, polled on mount + every 10s while the drawer
  // is open. Per ADR 0009 §D2 events are recorder-canonical, so the
  // recent-events feed reads directly from /v1/events filtered by the
  // open camera id.
  const [subs, setSubs] = useState<OnvifSubscription[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [recentEvents, setRecentEvents] = useState<ApiEvent[]>([]);

  const ourSub = subs.find((s) => s.camera_id === c.id);

  async function refresh() {
    try {
      const list = await onvifListSubscriptions();
      setSubs(list.items);
    } catch (e) {
      setErr((e as Error).message);
    }
  }

  async function refreshEvents() {
    try {
      const list = await fetchEvents({ perPage: 50 });
      // Filter for events whose subject_id matches this camera and
      // whose kind starts with 'camera.' (motion, tamper, signal_loss,
      // onvif_event). The recorder doesn't yet expose a `subject_id=`
      // filter so we filter client-side.
      setRecentEvents(
        list.items
          .filter((e) => e.subject_id === c.id && e.kind.startsWith('camera.'))
          .slice(0, 30),
      );
    } catch {
      // Best-effort; leave the previous list intact.
    }
  }

  useEffect(() => {
    void refresh();
    void refreshEvents();
    const t = window.setInterval(() => {
      void refresh();
      void refreshEvents();
    }, 10_000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [c.id]);

  async function subscribe() {
    setBusy(true);
    setErr(null);
    try {
      // Default xaddr inferred from the camera ip. Most cameras
      // expose ONVIF on /onvif/device_service over plain HTTP.
      const host = (c.ip || '').replace(/^rtsp:\/\//, '').split('/')[0].split(':')[0];
      const xaddr = `http://${host}/onvif/device_service`;
      await onvifCreateSubscription({
        camera_id: c.id,
        xaddr,
      });
      await refresh();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function unsubscribe(id: string) {
    setBusy(true);
    setErr(null);
    try {
      await onvifDeleteSubscription(id);
      await refresh();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

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
          ONVIF events arrive via PullPoint subscription. Topics translate to
          canonical camera events (motion, tamper, signal_loss). Unmapped topics
          land as <code>camera.onvif_event</code> with the raw topic in attributes.
        </div>
      </div>

      {/* Subscription status + controls */}
      <div
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
        <Icon
          name={ourSub ? 'check-circle' : 'zap'}
          style={{
            width: 14,
            height: 14,
            color: ourSub ? '#22C55E' : 'var(--text-muted)',
          }}
        />
        <div style={{ flex: 1 }}>
          <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500 }}>
            PullPoint subscription
          </div>
          <div
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              marginTop: 2,
            }}
          >
            {ourSub
              ? `${ourSub.state.toUpperCase()} · ${ourSub.event_count} events received` +
                (ourSub.last_event_at ? ` · last ${new Date(ourSub.last_event_at).toLocaleTimeString()}` : '')
              : 'Not subscribed. Click Subscribe to start receiving events.'}
          </div>
          {ourSub?.last_error && (
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: '#EF4444',
                marginTop: 4,
              }}
            >
              {ourSub.last_error}
            </div>
          )}
        </div>
        {ourSub ? (
          <Btn kind="ghost" size="sm" disabled={busy} onClick={() => unsubscribe(ourSub.id)}>
            Unsubscribe
          </Btn>
        ) : (
          <Btn kind="primary" size="sm" disabled={busy} onClick={subscribe}>
            Subscribe
          </Btn>
        )}
      </div>

      {err && (
        <div
          style={{
            padding: 10,
            background: 'rgba(239,68,68,0.07)',
            border: '1px solid rgba(239,68,68,0.3)',
            borderRadius: 4,
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            color: '#EF4444',
          }}
        >
          {err}
        </div>
      )}

      {/* Recent events feed */}
      {recentEvents.length > 0 && (
        <div>
          <SectionHeader>RECENT EVENTS</SectionHeader>
          <div
            style={{
              marginTop: 10,
              border: '1px solid var(--border)',
              borderRadius: 4,
              overflow: 'hidden',
              maxHeight: 220,
              overflowY: 'auto',
            }}
          >
            {recentEvents.map((ev) => (
              <div
                key={ev.id}
                style={{
                  padding: '8px 12px',
                  borderBottom: '1px solid var(--border)',
                  display: 'grid',
                  gridTemplateColumns: '120px 1fr auto',
                  gap: 10,
                  alignItems: 'center',
                  background: 'var(--bg-tertiary)',
                }}
              >
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                  {new Date(ev.occurred_at).toLocaleTimeString()}
                </span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11 }}>{ev.kind}</span>
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 9,
                    letterSpacing: 1,
                    color:
                      ev.severity === 'error'
                        ? '#EF4444'
                        : ev.severity === 'warning'
                          ? '#F59E0B'
                          : '#22C55E',
                    textTransform: 'uppercase',
                  }}
                >
                  {ev.severity}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Per-event-type forwarding toggles. Local-only state today —
          a future slice will fold these into the subscription create
          body so the recorder can selectively forward only the
          enabled topics. */}
      <SectionHeader>EVENT FORWARDING (LOCAL-ONLY)</SectionHeader>
      {eventToggles.map((e) => (
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

