// Policies route — list / create / edit / delete RecordingPolicy.
//
// Wires to /v1/recording-policies. Shows every policy with a usage
// count joined client-side from /v1/cameras (recording_policy_id
// linkage). The seeded "Default" policy is always present and cannot
// be deleted (the recorder also rejects backend-side, but the button
// is disabled in the UI for clarity).
//
// Create / edit use PolicyEditorModal; delete uses an inline confirm
// row (a separate confirm-modal would be infra drift for one use).

import { useMemo, useState } from 'react';
import {
  Btn,
  Card,
  SectionHeader,
  StatusBadge,
} from '../components/primitives';
import { Icon } from '../components/Icon';
import { PageHeader } from '../components/PageHeader';
import { PolicyEditorModal } from '../components/PolicyEditorModal';
import { SchedulesEditor } from './SchedulesEditor';
import {
  ApiError,
  DEFAULT_RECORDING_POLICY_ID,
  deleteRecordingPolicy,
  fetchCameras,
  fetchRecordingPolicies,
} from '../lib/api';
import type { RecordingPolicy } from '../lib/api';
import { useFetch } from '../lib/hooks';
import { formatDuration } from '../lib/duration';
import type { ToastInput } from '../lib/types';

interface PoliciesProps {
  addToast: (t: ToastInput) => void;
}

const MODE_LABEL: Record<RecordingPolicy['mode'], string> = {
  continuous: 'CONTINUOUS',
  motion: 'MOTION',
  schedule: 'SCHEDULE',
  event_triggered: 'EVENT',
  off: 'OFF',
};

export function Policies({ addToast }: PoliciesProps) {
  const policies = useFetch(fetchRecordingPolicies, []);
  const cameras = useFetch(() => fetchCameras(0, 200), []);

  const lockedDown = false;

  const [editing, setEditing] = useState<RecordingPolicy | null>(null);
  const [creating, setCreating] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [scheduleFor, setScheduleFor] = useState<string | null>(null);

  const items = policies.data?.items ?? [];

  // Map recording_policy_id -> count of cameras attached to it.
  const usageById = useMemo<Record<string, number>>(() => {
    const out: Record<string, number> = {};
    for (const c of cameras.data?.items ?? []) {
      const id = c.recording_policy_id;
      if (id) out[id] = (out[id] ?? 0) + 1;
    }
    return out;
  }, [cameras.data]);

  async function doDelete(id: string) {
    try {
      await deleteRecordingPolicy(id);
      addToast({ kind: 'warning', title: 'POLICY DELETED', icon: 'trash-2' });
      policies.refetch();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : (e as Error).message;
      addToast({ kind: 'danger', title: 'DELETE FAILED', body: msg, icon: 'x' });
    } finally {
      setConfirmDelete(null);
    }
  }

  const errMsg =
    policies.status === 'error'
      ? `Recorder unreachable — ${policies.error.message}`
      : null;

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
        breadcrumb="RECORDING SERVER / POLICIES"
        title="Recording Policies"
        sub={
          errMsg ??
          (policies.status === 'loading'
            ? 'Loading policies…'
            : `${items.length} policy${items.length === 1 ? '' : ''}${items.length === 1 ? '' : ' · multiple cameras can share a policy'}`)
        }
        right={
          <Btn
            kind="primary"
            icon="plus"
            disabled={lockedDown}
            title={lockedDown ? 'Recording policies managed by Management Server' : undefined}
            onClick={() => {
              if (lockedDown) return;
              setCreating(true);
            }}
          >
            New Policy
          </Btn>
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
              Recording policies managed by Management Server
            </div>
            This recorder is paired with a Management Server that has taken canonical
            authority for recording policies. Add, edit, and delete operations are
            performed in the Management Server UI. The list view remains available here
            for reference.
          </div>
        )}
        <Card style={{ padding: 0 }}>
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader style={{ margin: 0 }}>POLICIES · {items.length}</SectionHeader>
          </div>

          {/* Header row */}
          <div
            style={{
              padding: '10px 16px',
              borderBottom: '1px solid var(--border)',
              display: 'grid',
              gridTemplateColumns: '2fr 1fr 1fr 1fr 0.6fr 140px',
              gap: 12,
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
              background: 'var(--bg-tertiary)',
            }}
          >
            <span>NAME</span>
            <span>MODE</span>
            <span>RETENTION</span>
            <span>CAMERAS</span>
            <span>STATUS</span>
            <span style={{ textAlign: 'right' }}>ACTIONS</span>
          </div>

          {items.length === 0 && policies.status === 'ready' && (
            <div
              style={{
                padding: 30,
                textAlign: 'center',
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-muted)',
                letterSpacing: 1,
              }}
            >
              NO POLICIES
            </div>
          )}
          {policies.status === 'loading' && (
            <div
              style={{
                padding: 30,
                textAlign: 'center',
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-muted)',
                letterSpacing: 1,
              }}
            >
              LOADING…
            </div>
          )}

          {items.map((p, i) => {
            const used = usageById[p.id] ?? 0;
            const isDefault = p.id === DEFAULT_RECORDING_POLICY_ID;
            const blockedReason = isDefault
              ? 'Default policy cannot be deleted'
              : used > 0
                ? `In use by ${used} camera${used === 1 ? '' : 's'}`
                : null;
            return (
              <div
                key={p.id}
                style={{
                  padding: '12px 16px',
                  borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                  display: 'grid',
                  gridTemplateColumns: '2fr 1fr 1fr 1fr 0.6fr 140px',
                  gap: 12,
                  alignItems: 'center',
                }}
              >
                <div>
                  <div
                    style={{
                      fontFamily: 'var(--font-sans)',
                      fontSize: 13,
                      color: 'var(--text-primary)',
                      fontWeight: 600,
                    }}
                  >
                    {p.name}
                    {isDefault && (
                      <span
                        style={{
                          marginLeft: 8,
                          fontFamily: 'var(--font-mono)',
                          fontSize: 9,
                          letterSpacing: 1,
                          color: '#F97316',
                          textTransform: 'uppercase',
                        }}
                      >
                        DEFAULT
                      </span>
                    )}
                  </div>
                  <div
                    style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 9,
                      color: 'var(--text-muted)',
                      letterSpacing: 0.5,
                      marginTop: 2,
                    }}
                  >
                    {p.id}
                  </div>
                </div>
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 11,
                    color: '#F97316',
                    letterSpacing: 1,
                  }}
                >
                  {MODE_LABEL[p.mode]}
                </span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)' }}>
                  {formatDuration(p.retention_duration)}
                </span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)' }}>
                  {used}
                </span>
                <StatusBadge kind={p.enabled ? 'online' : 'offline'} label={p.enabled ? 'ON' : 'OFF'} size="sm" />
                <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                  {confirmDelete === p.id ? (
                    <>
                      <Btn kind="ghost" size="sm" onClick={() => setConfirmDelete(null)}>
                        Cancel
                      </Btn>
                      <Btn kind="danger" size="sm" icon="trash-2" onClick={() => doDelete(p.id)}>
                        Confirm
                      </Btn>
                    </>
                  ) : (
                    <>
                      <Btn
                        kind="ghost"
                        size="sm"
                        onClick={() => setScheduleFor(scheduleFor === p.id ? null : p.id)}
                      >
                        Schedule
                      </Btn>
                      <Btn
                        kind="ghost"
                        size="sm"
                        icon="settings"
                        disabled={lockedDown}
                        title={lockedDown ? 'Recording policies managed by Management Server' : undefined}
                        onClick={() => {
                          if (lockedDown) return;
                          setEditing(p);
                        }}
                      >
                        Edit
                      </Btn>
                      <Btn
                        kind="danger"
                        size="sm"
                        icon="trash-2"
                        disabled={lockedDown || Boolean(blockedReason)}
                        title={lockedDown ? 'Recording policies managed by Management Server' : undefined}
                        onClick={() => {
                          if (lockedDown) return;
                          setConfirmDelete(p.id);
                        }}
                      >
                        Delete
                      </Btn>
                    </>
                  )}
                </div>
                {blockedReason && confirmDelete !== p.id && (
                  <div
                    style={{
                      gridColumn: '1 / -1',
                      fontFamily: 'var(--font-mono)',
                      fontSize: 9,
                      color: 'var(--text-muted)',
                      letterSpacing: 0.5,
                      textTransform: 'uppercase',
                      marginTop: 2,
                    }}
                  >
                    <Icon name="info" style={{ width: 10, height: 10, verticalAlign: 'middle' }} />{' '}
                    {blockedReason}
                  </div>
                )}
              </div>
            );
          })}
        </Card>
        {scheduleFor && (
          <SchedulesEditor policyID={scheduleFor} addToast={addToast} />
        )}
      </div>

      {creating && (
        <PolicyEditorModal
          onCancel={() => setCreating(false)}
          onSave={(p) => {
            setCreating(false);
            addToast({ kind: 'success', title: 'POLICY CREATED', body: p.name, icon: 'check-circle' });
            policies.refetch();
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
            policies.refetch();
          }}
        />
      )}
    </div>
  );
}
