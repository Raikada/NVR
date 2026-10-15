// Notification subscriptions list — wires (target × event_type ×
// camera × min_severity × quiet_hours).

import { useEffect, useState } from 'react';
import { Btn, Card, Input, SectionHeader } from '../components/primitives';
import {
  createNotificationSubscription,
  deleteNotificationSubscription,
  listEventTypes,
  listNotificationSubscriptions,
  listNotificationTargets,
} from '../lib/api';
import { fetchCameras } from '../lib/api';
import type { Camera } from '../lib/api';
import type {
  EventType,
  NotificationSeverity,
  NotificationSubscription,
  NotificationTarget,
  ToastInput,
} from '../lib/types';

interface Props {
  addToast: (t: ToastInput) => void;
}

export function NotificationSubscriptions({ addToast }: Props) {
  const [subs, setSubs] = useState<NotificationSubscription[]>([]);
  const [targets, setTargets] = useState<NotificationTarget[]>([]);
  const [eventTypes, setEventTypes] = useState<EventType[]>([]);
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  async function refresh() {
    setLoading(true);
    setError(null);
    try {
      const [s, t, et, cl] = await Promise.all([
        listNotificationSubscriptions(),
        listNotificationTargets(),
        listEventTypes().catch(() => [] as EventType[]),
        fetchCameras(0, 200).then((r) => r.items).catch(() => [] as Camera[]),
      ]);
      setSubs(s);
      setTargets(t);
      setEventTypes(et);
      setCameras(cl);
    } catch (e) {
      setError((e as Error).message);
    }
    setLoading(false);
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function doDelete(s: NotificationSubscription) {
    if (!confirm('Delete subscription?')) return;
    try {
      await deleteNotificationSubscription(s.id);
      addToast({ kind: 'warning', title: 'SUBSCRIPTION DELETED', icon: 'trash-2' });
      void refresh();
    } catch (e) {
      addToast({ kind: 'danger', title: 'DELETE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  function targetName(id: string) {
    return targets.find((t) => t.id === id)?.name ?? id.slice(0, 8);
  }
  function eventTypeName(id?: string) {
    if (!id) return '(any)';
    return eventTypes.find((e) => e.id === id)?.display_name ?? id;
  }
  function cameraName(id?: string) {
    if (!id) return '(any)';
    return cameras.find((c) => c.id === id)?.name ?? id.slice(0, 8);
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
          {error ?? (loading ? 'Loading subscriptions…' : `${subs.length} subscriptions`)}
        </span>
        <Btn kind="primary" icon="plus" disabled={targets.length === 0} onClick={() => setShowAdd(true)}>
          Add Subscription
        </Btn>
      </div>
      <Card style={{ padding: 0 }}>
        <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border)' }}>
          <SectionHeader style={{ margin: 0 }}>SUBSCRIPTIONS</SectionHeader>
        </div>
        {subs.length === 0 && !loading && (
          <div style={{ padding: 24, textAlign: 'center', fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>
            No subscriptions configured.
          </div>
        )}
        {subs.map((s) => (
          <div
            key={s.id}
            style={{
              padding: '12px 16px',
              borderBottom: '1px solid var(--border)',
              display: 'grid',
              gridTemplateColumns: '1.5fr 1.5fr 1.5fr 0.7fr 0.8fr 100px',
              gap: 12,
              alignItems: 'center',
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
              color: 'var(--text-primary)',
            }}
          >
            <span>{targetName(s.target_id)}</span>
            <span style={{ color: 'var(--text-secondary)' }}>{eventTypeName(s.event_type_id)}</span>
            <span style={{ color: 'var(--text-secondary)' }}>{cameraName(s.camera_id)}</span>
            <span style={{ color: 'var(--text-secondary)' }}>{s.min_severity ?? 'info'}</span>
            <span style={{ color: 'var(--text-muted)', fontSize: 10 }}>
              {s.quiet_hours_start_minute != null && s.quiet_hours_end_minute != null
                ? `quiet ${s.quiet_hours_start_minute}→${s.quiet_hours_end_minute}`
                : '—'}
            </span>
            <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
              <Btn kind="danger" size="sm" onClick={() => doDelete(s)}>
                Delete
              </Btn>
            </div>
          </div>
        ))}
      </Card>

      {showAdd && (
        <AddSubscriptionModal
          targets={targets}
          eventTypes={eventTypes}
          cameras={cameras}
          onClose={() => setShowAdd(false)}
          onCreated={() => {
            setShowAdd(false);
            void refresh();
          }}
          addToast={addToast}
        />
      )}
    </div>
  );
}

function AddSubscriptionModal({
  targets,
  eventTypes,
  cameras,
  onClose,
  onCreated,
  addToast,
}: {
  targets: NotificationTarget[];
  eventTypes: EventType[];
  cameras: Camera[];
  onClose: () => void;
  onCreated: () => void;
  addToast: (t: ToastInput) => void;
}) {
  const [targetId, setTargetId] = useState(targets[0]?.id ?? '');
  const [eventTypeId, setEventTypeId] = useState('');
  const [cameraId, setCameraId] = useState('');
  const [severity, setSeverity] = useState<NotificationSeverity>('info');
  const [qhStart, setQhStart] = useState('');
  const [qhEnd, setQhEnd] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    if (!targetId) return;
    setSubmitting(true);
    try {
      await createNotificationSubscription({
        target_id: targetId,
        event_type_id: eventTypeId || undefined,
        camera_id: cameraId || undefined,
        min_severity: severity,
        quiet_hours_start_minute: qhStart ? Number(qhStart) : undefined,
        quiet_hours_end_minute: qhEnd ? Number(qhEnd) : undefined,
      });
      addToast({ kind: 'success', title: 'SUBSCRIPTION CREATED', icon: 'bell' });
      onCreated();
    } catch (e) {
      addToast({ kind: 'danger', title: 'CREATE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSubmitting(false);
  }

  const selectStyle: React.CSSProperties = {
    background: 'var(--bg-tertiary)',
    border: '1px solid var(--border)',
    borderRadius: 4,
    padding: '8px 10px',
    color: 'var(--text-primary)',
    fontFamily: 'var(--font-mono)',
    fontSize: 12,
  };

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 7000,
        background: 'rgba(0,0,0,0.7)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
      }}
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 520,
          maxWidth: '100%',
          background: 'var(--bg-secondary)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          padding: 20,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 2, color: '#F97316', textTransform: 'uppercase' }}>
          Add subscription
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>TARGET</span>
          <select value={targetId} onChange={(e) => setTargetId(e.target.value)} style={selectStyle}>
            {targets.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.kind})
              </option>
            ))}
          </select>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>EVENT TYPE</span>
          <select value={eventTypeId} onChange={(e) => setEventTypeId(e.target.value)} style={selectStyle}>
            <option value="">(any)</option>
            {eventTypes.map((e) => (
              <option key={e.id} value={e.id}>
                {e.display_name}
              </option>
            ))}
          </select>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>CAMERA</span>
          <select value={cameraId} onChange={(e) => setCameraId(e.target.value)} style={selectStyle}>
            <option value="">(any)</option>
            {cameras.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>MIN SEVERITY</span>
          <select value={severity} onChange={(e) => setSeverity(e.target.value as NotificationSeverity)} style={selectStyle}>
            <option value="info">info</option>
            <option value="warning">warning</option>
            <option value="critical">critical</option>
          </select>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
          <Input label="QUIET START (MIN OF DAY)" value={qhStart} onChange={setQhStart} mono />
          <Input label="QUIET END (MIN OF DAY)" value={qhEnd} onChange={setQhEnd} mono />
        </div>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 6 }}>
          <Btn kind="ghost" onClick={onClose}>
            Cancel
          </Btn>
          <Btn kind="primary" onClick={submit} disabled={submitting || !targetId}>
            {submitting ? 'Creating…' : 'Create'}
          </Btn>
        </div>
      </div>
    </div>
  );
}
