// Storage & Retention — volume usage (stacked by tier), retention
// tiers (recording policies), per-camera tier assignment. All real:
// /v1/storage-volumes, /v1/storage-volumes/breakdown,
// /v1/recording-policies, PATCH /v1/cameras/:id.
import { useEffect, useMemo, useState } from 'react';
import { Icon } from '../components/Icon';
import { Btn, FormRow, Modal, Panel, Segmented, Slider, TxtInput, pageTitle, screenHdr } from '../components/ui';
import {
  ApiError, createRecordingPolicy, deleteRecordingPolicy, fetchStorageBreakdown,
  fetchStorageVolumes, patchCamera, runRetentionSweep, updateRecordingPolicy,
  type RecordingPolicy, type StorageBreakdownResponse, type StorageVolume,
} from '../lib/api';
import { daysToNs, nsToDays } from '../lib/duration';
import { useConsole } from '../lib/console';

const TIER_COLORS = ['var(--accent)', 'var(--warning)', 'var(--success)', '#3B82F6', '#A855F7', '#14B8A6'];

export function Storage() {
  const data = useConsole();
  const [volumes, setVolumes] = useState<StorageVolume[]>([]);
  const [breakdown, setBreakdown] = useState<StorageBreakdownResponse | null>(null);
  const [editing, setEditing] = useState<RecordingPolicy | 'new' | null>(null);
  const [sweeping, setSweeping] = useState(false);
  const [note, setNote] = useState('');

  const load = () => {
    void fetchStorageVolumes().then((r) => setVolumes(r.items ?? [])).catch(() => {});
    void fetchStorageBreakdown().then(setBreakdown).catch(() => {});
  };
  useEffect(load, []);

  const cap = volumes.reduce((a, v) => a + v.capacity_bytes, 0);
  const used = volumes.reduce((a, v) => a + v.used_bytes, 0);
  const pct = cap > 0 ? (used / cap) * 100 : 0;
  const writeBps = volumes.reduce((a, v) => a + (v.write_bytes_per_second ?? 0), 0);
  const daysLeft = writeBps > 0 ? Math.floor((cap - used) / (writeBps * 86400)) : null;
  const dailyWrite = writeBps > 0 ? `${((writeBps * 86400) / 1e9).toFixed(0)} GB` : '—';

  // Per-tier on-disk usage: sum the per-camera breakdown through each
  // camera's policy assignment.
  const tierUsage = useMemo(() => {
    const m: Record<string, number> = {};
    if (!breakdown) return m;
    for (const bc of breakdown.by_camera) {
      const cam = data.cameras.find((c) => c.id === bc.camera_id || c.name === bc.camera_id);
      const key = cam?.policyId ?? '';
      m[key] = (m[key] ?? 0) + bc.byte_size;
    }
    return m;
  }, [breakdown, data.cameras]);

  const oldest = useMemo(() => {
    // Oldest footage ≈ the longest retention among enabled tiers.
    const days = data.policies.filter((p) => p.enabled).map((p) => nsToDays(p.retention_duration));
    return days.length ? Math.max(...days) : null;
  }, [data.policies]);

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)', overflowY: 'auto' }}>
      <header style={screenHdr}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={pageTitle}>Storage & Retention</span>
          {cap > 0 && (
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}>
              {(used / 1e12).toFixed(2)} / {(cap / 1e12).toFixed(1)} TB
            </span>
          )}
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <Btn
            kind="ghost"
            disabled={sweeping}
            onClick={() => {
              setSweeping(true);
              void runRetentionSweep()
                .then((r) => setNote(`Sweep freed ${r.swept.segments} segments · ${r.swept.events} events · ${r.swept.clips} clips`))
                .catch(() => setNote('Sweep failed'))
                .finally(() => { setSweeping(false); load(); });
            }}
          ><Icon name="refresh-cw" size={12} />{sweeping ? 'Sweeping…' : 'Run sweep now'}</Btn>
          <Btn kind="primary" onClick={() => setEditing('new')}><Icon name="plus" size={12} />New tier</Btn>
        </div>
      </header>

      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 20 }}>
        {note && (
          <div style={{ padding: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>{note}</div>
        )}
        <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 12 }}>
          <div style={{ background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, padding: 16 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
              <div>
                <div style={{ fontFamily: 'var(--font-sans)', fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>Recording volume</div>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>
                  {volumes.map((v) => `${v.mount_path} · ${v.kind}`).join('  ·  ') || 'no volumes reported'}
                </div>
              </div>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 24, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums' }}>{pct.toFixed(0)}%</span>
            </div>
            <StackedBar policies={data.policies} tierUsage={tierUsage} total={cap} used={used} />
          </div>
          <Panel title="FORECAST">
            <div style={{ padding: 16, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
              <Forecast label="DAILY WRITE" value={dailyWrite} sub="MEASURED RATE" />
              <Forecast label="EST. HEADROOM" value={daysLeft != null ? `${daysLeft} days` : '—'} sub="AT CURRENT RATE" />
              <Forecast label="MAX RETENTION" value={oldest != null ? `${oldest} days` : '—'} sub="LONGEST TIER" />
              <Forecast label="SEGMENTS" value={breakdown ? String(breakdown.segment_count) : '—'} sub="ON DISK" />
            </div>
          </Panel>
        </div>

        <Panel title="RETENTION TIERS" right={<span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>TIER = RECORDING POLICY</span>}>
          <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr 1fr 1fr 1fr 60px', padding: '8px 14px', borderBottom: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>
            <span>TIER</span><span>MODE</span><span>KEEP FOOTAGE</span><span>CAMERAS</span><span style={{ textAlign: 'right' }}>ON DISK</span><span></span>
          </div>
          {data.policies.map((p) => {
            const camCount = data.cameras.filter((c) => c.policyId === p.id).length;
            const sz = tierUsage[p.id] ?? 0;
            return (
              <div key={p.id} style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr 1fr 1fr 1fr 60px', padding: '12px 14px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}>
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)' }}>
                  {p.name}{!p.enabled && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', marginLeft: 8 }}>DISABLED</span>}
                </span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', textTransform: 'uppercase' }}>{p.mode}</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--accent)' }}>{nsToDays(p.retention_duration)} days</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>{camCount}</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', textAlign: 'right' }}>{sz > 0 ? `${(sz / 1e9).toFixed(1)} GB` : '—'}</span>
                <button onClick={() => setEditing(p)} style={{ width: 24, height: 24, justifySelf: 'end', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, cursor: 'pointer', color: 'var(--text-muted)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
                  <Icon name="settings-2" size={11} />
                </button>
              </div>
            );
          })}
          <div style={{ padding: '12px 14px', fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
            Footage older than a tier's retention window is deleted by the nightly sweep. If the volume fills, the oldest footage goes first regardless of tier.
          </div>
        </Panel>

        <Panel title="CAMERA ASSIGNMENT">
          {data.cameras.map((c, i) => (
            <div key={c.id} style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: 14, alignItems: 'center', padding: '8px 14px', borderBottom: i < data.cameras.length - 1 ? '1px solid var(--border)' : 'none' }}>
              <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                {c.name} <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', marginLeft: 6 }}>{c.host}</span>
              </span>
              <Segmented
                options={data.policies.map((p) => ({ value: p.id, label: p.name.toUpperCase().slice(0, 12) }))}
                value={c.policyId}
                onChange={(pid) => {
                  void patchCamera(c.id, { recording_policy_id: pid })
                    .then(() => data.refreshCameras())
                    .catch((e) => setNote(e instanceof ApiError ? e.message : 'assignment failed'));
                }}
              />
            </div>
          ))}
          {data.cameras.length === 0 && (
            <div style={{ padding: 20, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No cameras yet.</div>
          )}
        </Panel>
      </div>

      {editing && (
        <TierEditor
          policy={editing === 'new' ? null : editing}
          camCount={editing === 'new' ? 0 : data.cameras.filter((c) => c.policyId === (editing as RecordingPolicy).id).length}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); window.location.reload(); }}
        />
      )}
    </div>
  );
}

function StackedBar({ policies, tierUsage, total, used }: {
  policies: RecordingPolicy[];
  tierUsage: Record<string, number>;
  total: number;
  used: number;
}) {
  if (total <= 0) {
    return <div style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No storage volumes reported yet.</div>;
  }
  const entries = policies.map((p, i) => ({ id: p.id, name: p.name, size: tierUsage[p.id] ?? 0, color: TIER_COLORS[i % TIER_COLORS.length] }));
  const other = Math.max(0, used - entries.reduce((a, e) => a + e.size, 0));
  return (
    <div>
      <div style={{ display: 'flex', height: 18, border: '1px solid var(--border)', borderRadius: 3, overflow: 'hidden', background: 'var(--bg-tertiary)' }}>
        {entries.filter((e) => e.size > 0).map((e) => (
          <div key={e.id} title={`${e.name} ${(e.size / 1e9).toFixed(1)} GB`} style={{ width: `${(e.size / total) * 100}%`, background: e.color, opacity: 0.85, borderRight: '1px solid var(--bg-primary)' }} />
        ))}
        {other > 0 && <div title={`other ${(other / 1e9).toFixed(1)} GB`} style={{ width: `${(other / total) * 100}%`, background: 'var(--text-muted)', opacity: 0.5 }} />}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16, marginTop: 12 }}>
        {entries.map((e) => (
          <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span style={{ width: 10, height: 10, background: e.color, opacity: 0.85 }} />
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>{e.name.toUpperCase()}</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{e.size > 0 ? `${(e.size / 1e9).toFixed(1)} GB` : '—'}</span>
          </div>
        ))}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 10, height: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--border)' }} />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-secondary)' }}>FREE</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{((total - used) / 1e12).toFixed(2)} TB</span>
        </div>
      </div>
    </div>
  );
}

function Forecast({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)', textTransform: 'uppercase' }}>{label}</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 22, color: 'var(--accent)', fontVariantNumeric: 'tabular-nums', lineHeight: 1 }}>{value}</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{sub}</span>
    </div>
  );
}

function TierEditor({ policy, camCount, onClose, onSaved }: {
  policy: RecordingPolicy | null;
  camCount: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isNew = policy === null;
  const [name, setName] = useState(policy?.name ?? '');
  const [days, setDays] = useState(policy ? Math.max(1, nsToDays(policy.retention_duration)) : 14);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const save = async () => {
    setBusy(true);
    setErr('');
    try {
      if (isNew) {
        await createRecordingPolicy({ name: name.trim(), mode: 'continuous', retention_duration: daysToNs(days), enabled: true });
      } else {
        await updateRecordingPolicy(policy.id, { name: name.trim(), retention_duration: daysToNs(days) });
      }
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'save failed');
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!policy || camCount > 0) return;
    if (!window.confirm(`Delete tier "${policy.name}"?`)) return;
    setBusy(true);
    try {
      await deleteRecordingPolicy(policy.id);
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'delete failed');
      setBusy(false);
    }
  };

  return (
    <Modal
      title={isNew ? 'New retention tier' : `Edit · ${policy.name}`}
      onClose={onClose}
      footer={
        <>
          <Btn kind="ghost" onClick={onClose}>Cancel</Btn>
          <div style={{ display: 'flex', gap: 8 }}>
            {!isNew && (
              <Btn kind="danger" disabled={camCount > 0 || busy} title={camCount > 0 ? 'Reassign cameras first' : ''} onClick={() => void remove()}>Delete tier</Btn>
            )}
            <Btn kind="primary" disabled={busy || !name.trim()} onClick={() => void save()}>{busy ? 'Saving…' : isNew ? 'Create' : 'Save'}</Btn>
          </div>
        </>
      }
    >
      <FormRow label="Name"><TxtInput value={name} onChange={setName} placeholder="e.g. Entrances" /></FormRow>
      <FormRow label="Keep footage"><Slider value={days} onChange={setDays} min={1} max={180} suffix="d" ticks={10} /></FormRow>
      <div style={{ padding: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.6 }}>
        ≈ {(days * 0.14).toFixed(1)} TB per 4 cameras at 2.5 Mbps continuous.
      </div>
      {err && (
        <div style={{ padding: 10, background: 'var(--danger-7)', border: '1px solid var(--danger-27)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--danger)' }}>{err}</div>
      )}
    </Modal>
  );
}
