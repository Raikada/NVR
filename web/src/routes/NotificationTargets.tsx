// Notification targets list — webhook + email destinations.
// Mounted as a tab inside <Notifications/>.

import { useEffect, useState } from 'react';
import { Btn, Card, Input, SectionHeader, StatusBadge } from '../components/primitives';
import {
  createNotificationTarget,
  deleteNotificationTarget,
  listNotificationTargets,
  testNotificationTarget,
} from '../lib/api';
import type { NotificationTarget, NotificationTargetKind, ToastInput } from '../lib/types';

interface Props {
  addToast: (t: ToastInput) => void;
}

export function NotificationTargets({ addToast }: Props) {
  const [targets, setTargets] = useState<NotificationTarget[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  async function refresh() {
    setLoading(true);
    setError(null);
    try {
      setTargets(await listNotificationTargets());
    } catch (e) {
      setError((e as Error).message);
    }
    setLoading(false);
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function doTest(t: NotificationTarget) {
    try {
      const r = await testNotificationTarget(t.id);
      addToast({
        kind: r.ok ? 'success' : 'warning',
        title: r.ok ? 'TEST OK' : 'TEST FAILED',
        body: r.error || (r.status ? `HTTP ${r.status}` : t.name),
        icon: r.ok ? 'check-circle' : 'x',
      });
    } catch (e) {
      addToast({ kind: 'danger', title: 'TEST ERROR', body: (e as Error).message, icon: 'x' });
    }
  }

  async function doDelete(t: NotificationTarget) {
    if (!confirm(`Delete target "${t.name}"?`)) return;
    try {
      await deleteNotificationTarget(t.id);
      addToast({ kind: 'warning', title: 'TARGET DELETED', body: t.name, icon: 'trash-2' });
      void refresh();
    } catch (e) {
      addToast({ kind: 'danger', title: 'DELETE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
          {error ?? (loading ? 'Loading targets…' : `${targets.length} targets`)}
        </span>
        <Btn kind="primary" icon="plus" onClick={() => setShowAdd(true)}>
          Add Target
        </Btn>
      </div>
      <Card style={{ padding: 0 }}>
        <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border)' }}>
          <SectionHeader style={{ margin: 0 }}>TARGETS</SectionHeader>
        </div>
        {targets.length === 0 && !loading && (
          <div style={{ padding: 24, textAlign: 'center', fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>
            No targets configured.
          </div>
        )}
        {targets.map((t) => (
          <div
            key={t.id}
            style={{
              padding: '12px 16px',
              borderBottom: '1px solid var(--border)',
              display: 'grid',
              gridTemplateColumns: '1.5fr 0.7fr 2fr 0.8fr 220px',
              gap: 12,
              alignItems: 'center',
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
              color: 'var(--text-primary)',
            }}
          >
            <span>{t.name}</span>
            <span style={{ color: 'var(--text-secondary)' }}>{t.kind}</span>
            <span style={{ color: 'var(--text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {t.kind === 'webhook' ? t.webhook_url : t.email_address}
            </span>
            <StatusBadge kind={t.enabled ? 'online' : 'offline'} label={t.enabled ? 'ENABLED' : 'DISABLED'} size="sm" />
            <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
              <Btn kind="ghost" size="sm" onClick={() => doTest(t)}>
                Test
              </Btn>
              <Btn kind="danger" size="sm" onClick={() => doDelete(t)}>
                Delete
              </Btn>
            </div>
          </div>
        ))}
      </Card>

      {showAdd && (
        <AddTargetModal
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

function AddTargetModal({
  onClose,
  onCreated,
  addToast,
}: {
  onClose: () => void;
  onCreated: () => void;
  addToast: (t: ToastInput) => void;
}) {
  const [kind, setKind] = useState<NotificationTargetKind>('webhook');
  const [name, setName] = useState('');
  const [webhookUrl, setWebhookUrl] = useState('');
  const [webhookSecret, setWebhookSecret] = useState('');
  const [emailAddress, setEmailAddress] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    if (!name) return;
    if (kind === 'webhook' && !webhookUrl) return;
    if (kind === 'email' && !emailAddress) return;
    setSubmitting(true);
    try {
      await createNotificationTarget({
        kind,
        name,
        webhook_url: kind === 'webhook' ? webhookUrl : undefined,
        webhook_secret: kind === 'webhook' && webhookSecret ? webhookSecret : undefined,
        email_address: kind === 'email' ? emailAddress : undefined,
        enabled: true,
      });
      addToast({ kind: 'success', title: 'TARGET CREATED', body: name, icon: 'bell' });
      onCreated();
    } catch (e) {
      addToast({ kind: 'danger', title: 'CREATE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSubmitting(false);
  }

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
          Add notification target
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>KIND</span>
          <select
            value={kind}
            onChange={(e) => setKind(e.target.value as NotificationTargetKind)}
            style={{
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              padding: '8px 10px',
              color: 'var(--text-primary)',
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
            }}
          >
            <option value="webhook">webhook</option>
            <option value="email">email</option>
          </select>
        </div>
        <Input label="NAME" value={name} onChange={setName} />
        {kind === 'webhook' && (
          <>
            <Input label="WEBHOOK URL" value={webhookUrl} onChange={setWebhookUrl} mono />
            <Input label="WEBHOOK SECRET (OPTIONAL)" value={webhookSecret} onChange={setWebhookSecret} mono />
          </>
        )}
        {kind === 'email' && <Input label="EMAIL ADDRESS" value={emailAddress} onChange={setEmailAddress} mono />}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 6 }}>
          <Btn kind="ghost" onClick={onClose}>
            Cancel
          </Btn>
          <Btn kind="primary" onClick={submit} disabled={submitting || !name}>
            {submitting ? 'Creating…' : 'Create'}
          </Btn>
        </div>
      </div>
    </div>
  );
}
