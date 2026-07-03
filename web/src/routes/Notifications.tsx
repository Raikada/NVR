// Notifications page — tabbed Targets / Subscriptions sub-views.

import { useState } from 'react';
import { PageHeader } from '../components/PageHeader';
import { NotificationTargets } from './NotificationTargets';
import { NotificationSubscriptions } from './NotificationSubscriptions';
import type { ToastInput } from '../lib/types';

interface Props {
  addToast: (t: ToastInput) => void;
}

type Tab = 'targets' | 'subscriptions';

export function Notifications({ addToast }: Props) {
  const [tab, setTab] = useState<Tab>('targets');

  function tabBtn(key: Tab, label: string) {
    const active = tab === key;
    return (
      <button
        onClick={() => setTab(key)}
        style={{
          background: active ? 'rgba(249,115,22,0.13)' : 'transparent',
          border: active ? '1px solid rgba(249,115,22,0.27)' : '1px solid var(--border)',
          borderRadius: 4,
          padding: '6px 14px',
          color: active ? '#F97316' : 'var(--text-secondary)',
          fontFamily: 'var(--font-mono)',
          fontSize: 11,
          letterSpacing: 1,
          textTransform: 'uppercase',
          cursor: 'pointer',
        }}
      >
        {label}
      </button>
    );
  }

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
        breadcrumb="RECORDING SERVER / NOTIFICATIONS"
        title="Notifications"
        sub="Routing rules and delivery targets"
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div style={{ display: 'flex', gap: 8 }}>
          {tabBtn('targets', 'Targets')}
          {tabBtn('subscriptions', 'Subscriptions')}
        </div>
        {tab === 'targets' && <NotificationTargets addToast={addToast} />}
        {tab === 'subscriptions' && <NotificationSubscriptions addToast={addToast} />}
      </div>
    </div>
  );
}
