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
  restoreConfig,
  configReset,
  factoryWipeRequest,
  factoryWipeConfirm,
  exportRecorderRecoveryBundle,
} from '../lib/api';
import { useFetch } from '../lib/hooks';
import { useRef } from 'react';
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

  // Wave 7 / ADR 0015 device-lifecycle state.
  const [resetting, setResetting] = useState(false);
  const [wipeStage, setWipeStage] = useState<'idle' | 'confirm'>('idle');
  const [wipeToken, setWipeToken] = useState('');
  const [wipeAck, setWipeAck] = useState(false);
  const [wiping, setWiping] = useState(false);
  const [exportingBundle, setExportingBundle] = useState(false);

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

  // Wave 7 / ADR 0015 lifecycle handlers.
  async function doConfigReset(returnToUnpaired: boolean) {
    const msg = returnToUnpaired
      ? 'Reset config + return to unpaired state? This wipes the issued MS identity material and returns the recorder to pre-pair operation. Recordings + identity UUID + keypair survive.'
      : 'Reset recorder config? This is non-destructive — recordings + identity + audit + pairing all survive.';
    if (!confirm(msg)) return;
    setResetting(true);
    try {
      await configReset({ return_to_unpaired: returnToUnpaired });
      addToast({
        kind: 'success',
        title: 'CONFIG RESET',
        body: returnToUnpaired ? 'Reset + unpaired' : 'Reset complete; identity preserved',
        icon: 'rotate-ccw',
      });
    } catch (e) {
      addToast({ kind: 'danger', title: 'RESET FAILED', body: (e as Error).message, icon: 'x' });
    }
    setResetting(false);
  }

  async function doFactoryWipeStage1() {
    setWiping(true);
    try {
      const r = await factoryWipeRequest();
      setWipeToken(r.confirmation_token);
      setWipeStage('confirm');
      addToast({
        kind: 'warning',
        title: 'CONFIRMATION ISSUED',
        body: `Token expires ${r.expires_at}`,
        icon: 'alert-triangle',
      });
    } catch (e) {
      addToast({ kind: 'danger', title: 'WIPE REQUEST FAILED', body: (e as Error).message, icon: 'x' });
    }
    setWiping(false);
  }

  async function doFactoryWipeStage2() {
    if (!wipeAck) return;
    setWiping(true);
    try {
      const r = await factoryWipeConfirm(wipeToken);
      addToast({
        kind: 'warning',
        title: 'FACTORY WIPE COMPLETE',
        body: `wiped ~${r.wiped_segment_bytes} bytes; restart recorder to regenerate identity`,
        icon: 'trash-2',
      });
      setWipeStage('idle');
      setWipeToken('');
      setWipeAck(false);
    } catch (e) {
      addToast({ kind: 'danger', title: 'WIPE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setWiping(false);
  }

  async function doRecoveryBundleExport() {
    setExportingBundle(true);
    try {
      const bundle = await exportRecorderRecoveryBundle({
        purpose: 'evidence',
        include_audit: true,
      });
      const blob = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `raikada-recorder-bundle-${new Date().toISOString().replace(/[:.]/g, '').slice(0, 15)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      addToast({
        kind: 'success',
        title: 'BUNDLE EXPORTED',
        body: 'Signed manifest saved (no private keys).',
        icon: 'download-cloud',
      });
    } catch (e) {
      addToast({ kind: 'danger', title: 'BUNDLE EXPORT FAILED', body: (e as Error).message, icon: 'x' });
    }
    setExportingBundle(false);
  }

  // Restore: hidden <input type="file"> opens the OS file picker;
  // the file's text contents POST to /v1/recorder/config-restore.
  // Recorder validates against the same Conf.Validate machinery
  // bootstrap uses; an invalid file is rejected and the running
  // config stays untouched.
  const restoreInputRef = useRef<HTMLInputElement>(null);
  async function onRestoreFileChosen(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = ''; // reset so picking the same file twice still triggers
    if (!file) return;
    if (!confirm(`Restore recorder config from ${file.name}? Running config will be replaced.`)) {
      return;
    }
    try {
      const text = await file.text();
      const parsed = JSON.parse(text);
      await restoreConfig(parsed);
      addToast({
        kind: 'success',
        title: 'RESTORED',
        body: 'Config applied; recorder reloading',
        icon: 'check-circle',
      });
    } catch (err) {
      addToast({
        kind: 'danger',
        title: 'RESTORE FAILED',
        body: (err as Error).message,
        icon: 'x',
      });
    }
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
            {/* Wave 6: updates are coordinated by the paired
                Management Server per ADR 0014 D6. The recorder is the
                apply mechanism, not the approval surface — operators
                drive approve/apply from the MS UI. The button below
                just opens an info toast pointing operators at the MS. */}
            <Btn
              kind="ghost"
              icon="info"
              onClick={() =>
                addToast({
                  kind: 'info',
                  title: 'UPDATES ARE COORDINATED BY THE MS',
                  body:
                    identity.data?.paired
                      ? 'Approve + apply updates from the Management Server UI (ADR 0014 D6).'
                      : 'Pair this recorder with a Management Server to receive updates.',
                  icon: 'info',
                })
              }
            >
              Updates →
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
            <Btn
              kind="secondary"
              icon="upload-cloud"
              onClick={() => restoreInputRef.current?.click()}
            >
              Restore Config
            </Btn>
            <input
              ref={restoreInputRef}
              type="file"
              accept="application/json,.json"
              onChange={onRestoreFileChosen}
              style={{ display: 'none' }}
            />
          </div>
        </Card>
        {/* Wave 7 / ADR 0015 lifecycle controls. Mirrors MS-side
            Failover tab semantics but locally (operator standing in
            front of the recorder, not in the MS UI). */}
        <Card style={{ gridColumn: '1/3' }}>
          <SectionHeader>RESET (ADR 0015 D7)</SectionHeader>
          <div
            style={{
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: 'var(--text-muted)',
              marginTop: 6,
              marginBottom: 10,
              lineHeight: 1.5,
            }}
          >
            Non-destructive. Preserves identity (UUID + keypair + cert),
            recordings, audit chain, update trust roots. Refreshes the
            last-known-good config from the paired MS.
          </div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <Btn kind="secondary" icon="rotate-ccw" disabled={resetting} onClick={() => doConfigReset(false)}>
              {resetting ? 'Resetting…' : 'Config Reset'}
            </Btn>
            <Btn kind="secondary" icon="plug" disabled={resetting} onClick={() => doConfigReset(true)}>
              {resetting ? 'Resetting…' : 'Reset + Return to Unpaired'}
            </Btn>
            <Btn kind="secondary" icon="download-cloud" disabled={exportingBundle} onClick={doRecoveryBundleExport}>
              {exportingBundle ? 'Exporting…' : 'Export Recovery Bundle'}
            </Btn>
          </div>
        </Card>
        <Card style={{ gridColumn: '1/3', borderColor: 'rgba(239,68,68,0.3)' }}>
          <SectionHeader>FACTORY WIPE (ADR 0015 D8)</SectionHeader>
          <div
            style={{
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: '#EF4444',
              marginTop: 6,
              marginBottom: 4,
              fontWeight: 600,
            }}
          >
            DESTRUCTIVE — clears recordings, identity, audit, and pairing material.
          </div>
          <div
            style={{
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: 'var(--text-muted)',
              marginBottom: 10,
              lineHeight: 1.5,
            }}
          >
            Two-stage: first call issues a confirmation token (10-minute
            window); second call performs the wipe. After wipe, restart
            the recorder process — identity regenerates clean per ADR
            0015 D8. External / mounted storage outside PathDefaults.RecordPath
            is NOT auto-wiped; clean those manually.
          </div>
          {wipeStage === 'idle' && (
            <Btn kind="danger" icon="trash-2" disabled={wiping} onClick={doFactoryWipeStage1}>
              {wiping ? 'Requesting…' : 'Request Wipe Confirmation'}
            </Btn>
          )}
          {wipeStage === 'confirm' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <label
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 12,
                  color: '#E5E5E5',
                  display: 'flex',
                  alignItems: 'flex-start',
                  gap: 8,
                  cursor: 'pointer',
                }}
              >
                <input
                  type="checkbox"
                  checked={wipeAck}
                  onChange={(e) => setWipeAck(e.target.checked)}
                  style={{ marginTop: 2 }}
                />
                <span>
                  I understand my recordings, audit chain, and pairing
                  material will be destroyed. Update trust roots survive.
                </span>
              </label>
              <div style={{ display: 'flex', gap: 8 }}>
                <Btn kind="danger" icon="trash-2" disabled={wiping || !wipeAck} onClick={doFactoryWipeStage2}>
                  {wiping ? 'Wiping…' : 'Confirm Factory Wipe'}
                </Btn>
                <Btn
                  kind="ghost"
                  icon="x"
                  disabled={wiping}
                  onClick={() => {
                    setWipeStage('idle');
                    setWipeToken('');
                    setWipeAck(false);
                  }}
                >
                  Cancel
                </Btn>
              </div>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
