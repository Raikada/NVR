// Devices — camera inventory with real ONVIF discovery, adopt flow,
// manual RTSP add, detail flyout (hardware from capabilities, live
// jump, re-probe, reboot, remove).
import { useEffect, useState } from 'react';
import { Icon } from '../components/Icon';
import { Brackets, Btn, FormRow, Modal, SectionHeader, Segmented, StatusBadge, TxtInput, bareBtn, pageTitle, screenHdr } from '../components/ui';
import {
  ApiError, adoptCamera, createCamera, deleteCamera, getDiscoveredCameras,
  patchCamera, probeCameraCapabilities, probeDiscovery, rebootCamera, setCameraCredentials,
  type DiscoveredCamera,
} from '../lib/api';
import { fmtAgo } from '../lib/format';
import { useSnapshotURL } from '../lib/hooks';
import { useConsole, type ConsoleCamera } from '../lib/console';
import { CameraTile } from './CameraTile';

export function Devices({ onOpenLive }: { onOpenLive: (camId: string) => void }) {
  const data = useConsole();
  const [discovering, setDiscovering] = useState(false);
  const [discovered, setDiscovered] = useState<DiscoveredCamera[] | null>(null);
  const [showAdd, setShowAdd] = useState<null | { prefill?: DiscoveredCamera }>(null);
  const [filter, setFilter] = useState('all');
  const [view, setView] = useState('list');
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const startDiscover = () => {
    setDiscovering(true);
    setDiscovered([]);
    void probeDiscovery()
      .then((r) => setDiscovered(r.items ?? []))
      .catch(() => void getDiscoveredCameras().then((r) => setDiscovered(r.items ?? [])).catch(() => setDiscovered([])))
      .finally(() => setDiscovering(false));
  };

  const cams = filter === 'all' ? data.cameras
    : filter === 'online' ? data.cameras.filter((c) => c.status === 'online')
    : data.cameras.filter((c) => c.status !== 'online');
  const selected = selectedId ? data.getCam(selectedId) : null;
  const unmatched = (discovered ?? []).filter((d) => !d.matched_camera_id);

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)' }}>
      <header style={screenHdr}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={pageTitle}>Devices</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}>
            {data.cameras.length} CAMERAS · {data.cameras.filter((c) => c.status === 'online').length} ONLINE
          </span>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <Btn kind="secondary" onClick={startDiscover}><Icon name="radar" size={12} />Discover</Btn>
          <Btn kind="primary" onClick={() => setShowAdd({})}><Icon name="plus" size={12} />Add Camera</Btn>
        </div>
      </header>

      {(discovering || discovered !== null) && (
        <div style={{ borderBottom: '1px solid var(--border)', padding: '14px 20px', background: 'var(--bg-secondary)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: unmatched.length ? 12 : 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <Icon name="radar" size={14} color="var(--accent)" style={{ animation: discovering ? 'spin 2s linear infinite' : 'none' }} />
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 1, color: 'var(--accent)', textTransform: 'uppercase' }}>
                {discovering ? 'SCANNING NETWORK · WS-DISCOVERY' : `SCAN COMPLETE · ${unmatched.length} NEW DEVICE${unmatched.length === 1 ? '' : 'S'}${(discovered?.length ?? 0) - unmatched.length > 0 ? ` · ${(discovered?.length ?? 0) - unmatched.length} ALREADY ADOPTED` : ''}`}
              </span>
            </div>
            <button onClick={() => { setDiscovered(null); setDiscovering(false); }} style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}>
              <Icon name="x" size={14} />
            </button>
          </div>
          {unmatched.length > 0 && (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 8 }}>
              {unmatched.map((d) => (
                <div key={d.endpoint_reference} style={{ padding: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--accent-27)', borderRadius: 4, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10, animation: 'fadein 300ms ease-out' }}>
                  <div style={{ minWidth: 0, display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {[d.manufacturer, d.model].filter(Boolean).join(' ') || d.name || 'ONVIF device'}
                    </span>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{hostOfXaddr(d.xaddr)} · seen {fmtAgo((Date.now() - new Date(d.last_seen_at).getTime()) / 1000)}</span>
                  </div>
                  <Btn kind="tactical" onClick={() => setShowAdd({ prefill: d })}>ADD</Btn>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      <div style={{ padding: '14px 20px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Segmented options={[{ value: 'all', label: 'ALL' }, { value: 'online', label: 'ONLINE' }, { value: 'issues', label: 'ISSUES' }]} value={filter} onChange={setFilter} />
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>VIEW</span>
          <Segmented options={[{ value: 'list', label: 'LIST' }, { value: 'grid', label: 'GRID' }]} value={view} onChange={setView} />
        </div>
      </div>

      <div style={{
        flex: 1, padding: '0 20px 20px', overflowY: 'auto',
        display: view === 'grid' ? 'grid' : 'flex',
        gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
        flexDirection: 'column', gap: 8, alignContent: 'start',
      }}>
        {cams.map((c) => view === 'grid' ? (
          <div key={c.id} onClick={() => setSelectedId(c.id)} style={{ cursor: 'pointer', border: `1px solid ${selectedId === c.id ? 'var(--accent-40)' : 'var(--border)'}`, borderRadius: 6, overflow: 'hidden', background: 'var(--bg-secondary)' }}>
            <CameraTile camera={c} />
            <div style={{ padding: '8px 10px', display: 'flex', justifyContent: 'space-between', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)' }}>
              <span>{c.model || c.manufacturer || '—'}</span><span>{c.host}</span>
            </div>
          </div>
        ) : (
          <DeviceCard key={c.id} c={c} onClick={() => setSelectedId(c.id)} active={selectedId === c.id} />
        ))}
        {cams.length === 0 && (
          <div style={{ padding: 40, textAlign: 'center', fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
            {filter === 'issues' ? 'All cameras healthy.' : 'No cameras yet — run Discover or add one manually.'}
          </div>
        )}
      </div>

      {showAdd && (
        <AddCameraModal
          prefill={showAdd.prefill}
          onClose={() => setShowAdd(null)}
          onAdded={() => { setShowAdd(null); data.refreshCameras(); }}
        />
      )}
      {selected && <DeviceDetail c={selected} onClose={() => setSelectedId(null)} onOpenLive={onOpenLive} />}
    </div>
  );
}

function hostOfXaddr(xaddr: string): string {
  try { return new URL(xaddr).hostname; } catch { return xaddr; }
}

function DeviceCard({ c, onClick, active }: { c: ConsoleCamera; onClick: () => void; active: boolean }) {
  const offline = c.status === 'offline';
  const degraded = c.status === 'degraded';
  const snap = useSnapshotURL(c.id, !offline, 30_000);
  return (
    <div onClick={onClick} style={{
      display: 'flex', alignItems: 'center', gap: 14, padding: 12,
      background: 'var(--bg-secondary)',
      border: `1px solid ${active ? 'var(--accent-40)' : offline ? 'var(--danger-27)' : 'var(--border)'}`,
      borderRadius: 6, cursor: 'pointer',
      opacity: offline ? 0.7 : 1,
    }}>
      <div style={{ width: 96, height: 56, position: 'relative', flexShrink: 0, background: 'linear-gradient(135deg, #1a1410, #0d0c0a)', overflow: 'hidden' }}>
        {snap && !offline && (
          <img src={snap} alt="" style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover' }} />
        )}
        <Brackets />
        {offline && (
          <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Icon name="wifi-off" size={16} color="var(--danger)" />
          </div>
        )}
      </div>
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>{c.name}</span>
          <StatusBadge kind={c.status} />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: '.5px' }}>{c.group}</span>
        </div>
        <div style={{ display: 'flex', gap: 14, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', flexWrap: 'wrap' }}>
          <span>{c.model || c.manufacturer || c.raw.source_type.toUpperCase()}</span>
          {c.resolution && <span>{c.resolution}</span>}
          {c.codec && <span>{c.codec}</span>}
          {degraded && <span style={{ color: 'var(--warning)' }}>RECONNECTING</span>}
          {offline && c.health?.last_seen_at && (
            <span style={{ color: 'var(--danger)' }}>LAST SEEN {fmtAgo((Date.now() - new Date(c.health.last_seen_at).getTime()) / 1000).toUpperCase()}</span>
          )}
        </div>
        <div style={{ display: 'flex', gap: 6, marginTop: 4 }}>
          {['REC', ...(c.hasPTZ ? ['PTZ'] : []), ...(c.hasAudio ? ['AUDIO'] : []), ...(c.hasSub ? ['SUB'] : []), ...(c.caps?.has_motion ? ['EVENTS'] : [])].map((cap) => (
            <span key={cap} style={{ padding: '2px 6px', fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', color: 'var(--text-secondary)', borderRadius: 2 }}>{cap}</span>
          ))}
        </div>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{c.host}</span>
        <Icon name="chevron-right" size={16} color="var(--text-muted)" />
      </div>
    </div>
  );
}

/* ---------- add / adopt modal ---------- */

function AddCameraModal({ prefill, onClose, onAdded }: {
  prefill?: DiscoveredCamera;
  onClose: () => void;
  onAdded: () => void;
}) {
  const [name, setName] = useState(prefill ? (prefill.model || prefill.name || '').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') : '');
  const [user, setUser] = useState('admin');
  const [pass, setPass] = useState('');
  const [url, setUrl] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async () => {
    setBusy(true);
    setErr('');
    try {
      if (prefill) {
        await adoptCamera({ endpoint_reference: prefill.endpoint_reference, name, rtsp_username: user, rtsp_password: pass });
      } else {
        const cam = await createCamera({ name, source_type: 'rtsp', source_url: url });
        if (user && pass) await setCameraCredentials(cam.id, { rtsp_username: user, rtsp_password: pass });
      }
      onAdded();
    } catch (e) {
      setErr(e instanceof ApiError ? (e.message || `HTTP ${e.status}`) : 'request failed');
      setBusy(false);
    }
  };

  return (
    <Modal
      title="Add Camera"
      width={540}
      onClose={onClose}
      footer={
        <>
          <Btn kind="ghost" onClick={onClose}>Cancel</Btn>
          <Btn kind="primary" onClick={() => void submit()} disabled={busy || !name || (!prefill && !url)}>
            {busy ? (prefill ? 'Probing camera…' : 'Adding…') : prefill ? 'Adopt Camera' : 'Add Camera'}
          </Btn>
        </>
      }
    >
      {prefill && (
        <div style={{ padding: 12, background: 'var(--bg-tertiary)', border: '1px solid var(--accent-27)', borderRadius: 4 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--accent)' }}>DISCOVERED DEVICE</span>
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 12, rowGap: 4, marginTop: 8 }}>
            {([
              ['MODEL', [prefill.manufacturer, prefill.model].filter(Boolean).join(' ') || '—'],
              ['HOST', hostOfXaddr(prefill.xaddr)],
              ['XADDR', prefill.xaddr],
            ] as Array<[string, string]>).map(([k, v]) => (
              <span key={k} style={{ display: 'contents' }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>{k}</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{v}</span>
              </span>
            ))}
          </div>
        </div>
      )}
      {!prefill && (
        <FormRow label="Stream URL"><TxtInput value={url} onChange={setUrl} placeholder="rtsp://192.168.1.x:554/stream1" /></FormRow>
      )}
      {/* This is the immutable stream path (ADR 0009), not the editable
          display label — that's renameable later from the device detail. */}
      <FormRow label="Name"><TxtInput value={name} onChange={setName} placeholder="e.g. front_door" /></FormRow>
      <FormRow label="Username"><TxtInput value={user} onChange={setUser} /></FormRow>
      <FormRow label="Password"><TxtInput value={pass} onChange={setPass} placeholder="••••••••" type="password" /></FormRow>
      {prefill && (
        <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
          Adopting probes the camera over ONVIF with these credentials, stores its capabilities, and starts recording per the default policy.
        </span>
      )}
      {err && (
        <div style={{ padding: 10, background: 'var(--danger-7)', border: '1px solid var(--danger-27)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--danger)' }}>{err}</div>
      )}
    </Modal>
  );
}

/* ---------- detail flyout ---------- */

function DeviceDetail({ c, onClose, onOpenLive }: {
  c: ConsoleCamera;
  onClose: () => void;
  onOpenLive: (camId: string) => void;
}) {
  const data = useConsole();
  const [busy, setBusy] = useState('');
  const [note, setNote] = useState('');
  const [editingName, setEditingName] = useState<string | null>(null);
  useEffect(() => { setNote(''); setBusy(''); setEditingName(null); }, [c.id]);

  const saveName = () => {
    if (editingName === null) return;
    const v = editingName.trim();
    if (v === c.name) { setEditingName(null); return; }
    setBusy('rename');
    setNote('');
    // Empty clears the label back to the stream path; the path itself
    // (c.path) is immutable — it keys the stream URL and recordings.
    void patchCamera(c.id, { display_name: v === c.path ? '' : v })
      .then(() => { setNote('renamed'); setEditingName(null); data.refreshCameras(); })
      .catch((e) => setNote(e instanceof ApiError ? (e.message || `HTTP ${e.status}`) : 'rename failed'))
      .finally(() => setBusy(''));
  };

  const act = (label: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(label);
    setNote('');
    void fn()
      .then(() => { setNote(done); data.refreshCameras(); })
      .catch((e) => setNote(e instanceof ApiError ? (e.message || `HTTP ${e.status}`) : 'failed'))
      .finally(() => setBusy(''));
  };

  return (
    <aside style={{ position: 'fixed', top: 0, right: 0, bottom: 0, width: 380, zIndex: 80, background: 'var(--bg-secondary)', borderLeft: '1px solid var(--border)', display: 'flex', flexDirection: 'column', overflowY: 'auto' }}>
      <div style={{ padding: '12px 14px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--border)' }}>
        <SectionHeader>{c.host || c.name}</SectionHeader>
        <button onClick={onClose} style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}>
          <Icon name="x" size={14} />
        </button>
      </div>
      <div style={{ padding: 14 }}>
        <CameraTile camera={c} large />
      </div>
      <div style={{ padding: '0 14px 14px', display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div>
          {editingName === null ? (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              <span style={{ fontFamily: 'var(--font-sans)', fontSize: 16, fontWeight: 600, color: 'var(--text-primary)' }}>{c.name}</span>
              <button
                title="Rename camera"
                onClick={() => setEditingName(c.name)}
                style={{ ...bareBtn, color: 'var(--text-secondary)', display: 'flex' }}
              >
                <Icon name="settings-2" size={13} />
              </button>
            </span>
          ) : (
            <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <TxtInput
                value={editingName}
                onChange={setEditingName}
                placeholder={c.path}
                autoFocus
                onKeyDown={(e) => {
                  if (e.key === 'Enter') saveName();
                  if (e.key === 'Escape') setEditingName(null);
                }}
              />
              <Btn kind="primary" onClick={saveName} disabled={busy !== ''}>
                {busy === 'rename' ? 'Saving…' : 'Save'}
              </Btn>
              <Btn kind="ghost" onClick={() => setEditingName(null)} disabled={busy !== ''}>Cancel</Btn>
            </span>
          )}
          <div style={{ display: 'flex', gap: 8, marginTop: 4, flexWrap: 'wrap' }}>
            <StatusBadge kind={c.status} />
            {['REC', ...(c.hasPTZ ? ['PTZ'] : []), ...(c.hasAudio ? ['AUDIO'] : []), ...(c.hasSub ? ['SUB'] : [])].map((cap) => (
              <span key={cap} style={{ padding: '2px 6px', fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', color: 'var(--text-secondary)', borderRadius: 2 }}>{cap}</span>
            ))}
          </div>
        </div>

        <div>
          <SectionHeader right={
            <button
              onClick={() => act('probe', () => probeCameraCapabilities(c.id), 'capabilities refreshed')}
              style={{ ...bareBtn, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--accent)', letterSpacing: '.5px' }}
              disabled={busy !== ''}
            >{busy === 'probe' ? 'PROBING…' : 'RE-PROBE'}</button>
          }>HARDWARE</SectionHeader>
          <div style={{ marginTop: 10, display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 12, rowGap: 6 }}>
            {([
              ['VENDOR', c.manufacturer || '—'],
              ['MODEL', c.model || '—'],
              ['STREAM PATH', c.path],
              ['HOST', c.host || '—'],
              ['CODEC', c.codec || '—'],
              ['RES', c.resolution || '—'],
              ['FIRMWARE', c.vendor?.device?.firmware_version ?? '—'],
              ['SERIAL', c.vendor?.device?.serial_number ?? '—'],
              ['RTSP', c.health?.rtsp_state ?? '—'],
              ['EVENTS', c.raw.event_channel || 'auto'],
            ] as Array<[string, string]>).map(([k, v]) => (
              <span key={k} style={{ display: 'contents' }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{k}</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', fontVariantNumeric: 'tabular-nums', overflow: 'hidden', textOverflow: 'ellipsis' }}>{v}</span>
              </span>
            ))}
          </div>
        </div>

        <div>
          <SectionHeader>RETENTION</SectionHeader>
          <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--accent)' }}>{c.group}</span>
            <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
              Tier assignment and retention windows live under Storage & Retention.
            </span>
          </div>
        </div>

        {note && (
          <div style={{ padding: 8, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)' }}>{note}</div>
        )}

        <div style={{ display: 'flex', gap: 6 }}>
          <Btn kind="primary" onClick={() => onOpenLive(c.id)}><Icon name="eye" size={12} />Live</Btn>
          <Btn
            kind="secondary"
            disabled={busy !== ''}
            onClick={() => act('reboot', () => rebootCamera(c.id), 'reboot requested')}
          ><Icon name="refresh-cw" size={12} />{busy === 'reboot' ? 'Rebooting…' : 'Reboot'}</Btn>
          <Btn
            kind="danger"
            disabled={busy !== ''}
            onClick={() => {
              if (!window.confirm(`Remove ${c.name}? Recordings follow the retention policy; the camera itself is untouched.`)) return;
              act('remove', async () => { await deleteCamera(c.id); onClose(); }, 'removed');
            }}
          ><Icon name="trash-2" size={12} />Remove</Btn>
        </div>
      </div>
    </aside>
  );
}
