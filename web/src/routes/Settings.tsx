// Settings route — identity / firmware update / system actions.
//
// Identity card: wires to /v1/recorder/identity. hostname and
// timezone are read-only at the recorder API (changing them is the
// host's provisioning system's job); location is operator-set and
// PATCHes through to conf.ServerLocation.
//
// Firmware: surfaces a.Version (firmware_version) from the same
// identity endpoint. Update flow is STUB — recorder has no update
// endpoint yet (architectural decision pending; would need binary
// signing + rollback semantics).
//
// System actions:
//   - Reboot wires to POST /v1/recorder/reboot (admin-gated, audited).
//   - Backup Config wires to GET /v1/recorder/config-backup (browser
//     download).
//   - Restore Config stays a STUB — destructive, queued for a
//     careful follow-up.
//   - Factory Reset stays a STUB — needs an architectural decision
//     about what "factory" means.

import { useEffect, useState } from 'react';
import { Btn, Card, Input, SectionHeader, Toggle } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import {
  fetchIdentity,
  patchIdentity,
  rebootRecorder,
  configBackupURL,
} from '../lib/api';
import { useFetch } from '../lib/hooks';
import type { AppState, ToastInput } from '../lib/types';

interface SettingsProps {
  state: AppState;
  addToast: (t: ToastInput) => void;
}

export function Settings({ state, addToast }: SettingsProps) {
  const identity = useFetch(fetchIdentity, []);

  const [autoUpdate, setAutoUpdate] = useState(true); // STUB — no recorder flag yet
  const [telemetry, setTelemetry] = useState(true); // STUB — no recorder flag yet
  const [hostname, setHostname] = useState(state.hostname);
  const [location, setLocation] = useState('');
  const [timezone, setTimezone] = useState('');
  const [savingLocation, setSavingLocation] = useState(false);
  const [rebooting, setRebooting] = useState(false);

  // Once identity loads, mirror its fields into local edit state.
  useEffect(() => {
    if (identity.data) {
      setHostname(identity.data.hostname);
      setLocation(identity.data.location);
      setTimezone(identity.data.timezone);
    }
  }, [identity.data]);

  async function saveLocation() {
    setSavingLocation(true);
    try {
      await patchIdentity({ location });
      addToast({
        kind: 'success',
        title: 'SAVED',
        body: `Location: ${location || '(empty)'}`,
        icon: 'check-circle',
      });
      identity.refetch();
    } catch (e) {
      addToast({ kind: 'danger', title: 'SAVE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSavingLocation(false);
  }

  async function doReboot() {
    if (!confirm('Reboot the recorder? Recording will pause for ~30 seconds.')) return;
    setRebooting(true);
    try {
      await rebootRecorder();
      addToast({
        kind: 'warning',
        title: 'REBOOT QUEUED',
        body: 'Recorder will restart in ~5 seconds',
        icon: 'rotate-ccw',
      });
    } catch (e) {
      addToast({ kind: 'danger', title: 'REBOOT FAILED', body: (e as Error).message, icon: 'x' });
    }
    setRebooting(false);
  }

  function downloadBackup() {
    // Drive the browser's native download flow rather than fetch +
    // blob: the GET endpoint sets Content-Disposition: attachment so
    // a top-level navigation triggers the save dialog. Window.open
    // with _blank avoids losing the SPA's hash-routed state.
    window.open(configBackupURL, '_blank');
    addToast({
      kind: 'info',
      title: 'BACKUP STARTED',
      body: 'Saving recorder config…',
      icon: 'download-cloud',
    });
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
        breadcrumb="RECORDING SERVER / SETTINGS"
        title="Settings"
        sub={
          identity.status === 'error'
            ? `Recorder unreachable — ${identity.error.message}`
            : identity.status === 'loading'
              ? 'Loading recorder identity…'
              : 'System-level controls for this recorder'
        }
      />
      <div style={{ padding: 20, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <Card>
          <SectionHeader
            right={
              <Btn
                kind="primary"
                size="sm"
                disabled={savingLocation || identity.status !== 'ready' || location === identity.data?.location}
                onClick={saveLocation}
              >
                {savingLocation ? 'Saving…' : 'Save Location'}
              </Btn>
            }
          >
            IDENTITY
          </SectionHeader>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 8 }}>
            <Input label="HOSTNAME (READ-ONLY)" value={hostname} onChange={() => {}} mono disabled />
            <Input label="LOCATION" value={location} onChange={setLocation} />
            <Input label="TIMEZONE (READ-ONLY)" value={timezone} onChange={() => {}} mono disabled />
          </div>
        </Card>
        <Card>
          <SectionHeader>FIRMWARE</SectionHeader>
          <div
            style={{
              padding: 10,
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              marginTop: 8,
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 1,
                  color: 'var(--text-muted)',
                  textTransform: 'uppercase',
                }}
              >
                CURRENT VERSION
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 13,
                  color: 'var(--text-primary)',
                  marginTop: 3,
                }}
              >
                {identity.data?.firmware_version || '—'}
              </div>
            </div>
            {/* STUB: update flow needs an architectural decision
                (binary signing, rollback semantics, update channel).
                No /v1/recorder/update endpoint today. */}
            <Btn
              kind="ghost"
              icon="info"
              onClick={() =>
                addToast({
                  kind: 'info',
                  title: 'UPDATE FLOW NOT WIRED',
                  body: 'See docs/web-ui.md stub list',
                  icon: 'info',
                })
              }
            >
              Check for Updates
            </Btn>
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              marginTop: 14,
              padding: '8px 0',
              borderTop: '1px solid var(--border)',
            }}
          >
            <div>
              <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                Auto-update
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 11,
                  color: 'var(--text-muted)',
                  marginTop: 2,
                }}
              >
                Apply security patches on 02:00 maintenance window
              </div>
            </div>
            <Toggle on={autoUpdate} onChange={setAutoUpdate} />
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              padding: '8px 0',
              borderTop: '1px solid var(--border)',
            }}
          >
            <div>
              <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                Telemetry to Raikada
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 11,
                  color: 'var(--text-muted)',
                  marginTop: 2,
                }}
              >
                Anonymous crash + performance reports
              </div>
            </div>
            <Toggle on={telemetry} onChange={setTelemetry} />
          </div>
        </Card>
        <Card style={{ gridColumn: '1/3' }}>
          <SectionHeader>SYSTEM ACTIONS</SectionHeader>
          <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
            <Btn kind="secondary" icon="rotate-ccw" disabled={rebooting} onClick={doReboot}>
              {rebooting ? 'Reboot queued…' : 'Reboot'}
            </Btn>
            <Btn kind="secondary" icon="download-cloud" onClick={downloadBackup}>
              Backup Config
            </Btn>
            {/* STUB: config-restore is intentionally unimplemented
                server-side until atomic-replacement + rollback are
                designed. Button toasts a stub message rather than
                pretending to work. */}
            <Btn
              kind="secondary"
              icon="upload-cloud"
              onClick={() =>
                addToast({
                  kind: 'info',
                  title: 'RESTORE NOT WIRED',
                  body: 'Restore is a destructive op; queued for a careful follow-up',
                  icon: 'info',
                })
              }
            >
              Restore Config
            </Btn>
            {/* STUB: factory reset needs an architectural decision
                about what state survives. Button toasts. */}
            <Btn
              kind="danger"
              icon="trash-2"
              onClick={() =>
                addToast({
                  kind: 'info',
                  title: 'FACTORY RESET NOT WIRED',
                  body: 'No recorder endpoint; needs scope decision',
                  icon: 'info',
                })
              }
            >
              Factory Reset
            </Btn>
          </div>
        </Card>
      </div>
    </div>
  );
}
