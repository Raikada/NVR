// First-run setup wizard for the consumer NVR foundation. Walks
// the operator through site name + timezone + language + optional
// TLS + optional SMTP. No pairing — this is a single-site appliance.
//
// Mounts when getSetupStatus() reports setup_required:true. The
// "Skip setup" affordance dismisses the overlay without flipping
// the backend flag; clicking "Done" persists the setup_complete
// system_settings flag via patchSystemSettings.

import { useEffect, useState } from 'react';
import { Btn, Bracket, Input } from './primitives';
import { Icon } from './Icon';
import {
  patchSystemSettings,
  putSystemTLS,
} from '../lib/api';
import type { AppState, ToastInput } from '../lib/types';

interface SetupWizardProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  addToast: (t: ToastInput) => void;
  onDone: () => void;
  onDismiss: () => void;
}

const STEPS = ['SITE', 'TIME', 'LANGUAGE', 'TLS', 'SMTP', 'DONE'] as const;
const STEP_TITLES = [
  'Name this recorder',
  'Confirm timezone',
  'Choose language',
  'Optional: upload TLS certificate',
  'Optional: configure SMTP',
  'Setup complete',
] as const;

function detectTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

export function SetupWizard({ onDone, onDismiss, addToast }: SetupWizardProps) {
  const [step, setStep] = useState(0);

  const [siteName, setSiteName] = useState('Home');
  const [timezone, setTimezone] = useState<string>(detectTimezone());
  const [language, setLanguage] = useState<string>('en');

  const [tlsCert, setTlsCert] = useState('');
  const [tlsKey, setTlsKey] = useState('');
  const [tlsSubmitting, setTlsSubmitting] = useState(false);

  const [smtpHost, setSmtpHost] = useState('');
  const [smtpPort, setSmtpPort] = useState('587');
  const [smtpUser, setSmtpUser] = useState('');
  const [smtpPass, setSmtpPass] = useState('');
  const [smtpFrom, setSmtpFrom] = useState('');

  const [finishing, setFinishing] = useState(false);

  async function saveSite() {
    try {
      await patchSystemSettings({ site_name: siteName });
    } catch (e) {
      addToast({ kind: 'danger', title: 'SAVE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  async function saveTimezone() {
    try {
      await patchSystemSettings({ timezone });
    } catch (e) {
      addToast({ kind: 'danger', title: 'SAVE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  async function saveLanguage() {
    try {
      await patchSystemSettings({ language });
    } catch (e) {
      addToast({ kind: 'danger', title: 'SAVE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  async function saveTLS() {
    if (!tlsCert.trim() || !tlsKey.trim()) return;
    setTlsSubmitting(true);
    try {
      await putSystemTLS(tlsCert, tlsKey);
      addToast({ kind: 'success', title: 'TLS UPLOADED', icon: 'check-circle' });
    } catch (e) {
      addToast({ kind: 'danger', title: 'TLS UPLOAD FAILED', body: (e as Error).message, icon: 'x' });
    }
    setTlsSubmitting(false);
  }

  async function saveSMTP() {
    if (!smtpHost.trim()) return;
    try {
      await patchSystemSettings({
        smtp_host: smtpHost,
        smtp_port: smtpPort,
        smtp_username: smtpUser,
        smtp_password: smtpPass,
        smtp_from: smtpFrom,
      });
      addToast({ kind: 'success', title: 'SMTP SAVED', icon: 'check-circle' });
    } catch (e) {
      addToast({ kind: 'danger', title: 'SMTP SAVE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  async function finish() {
    setFinishing(true);
    try {
      await patchSystemSettings({ setup_complete: 'true' });
    } catch {
      // Best-effort; the overlay still closes so the operator isn't stranded.
    }
    setFinishing(false);
    onDone();
  }

  // When entering specific steps, persist the previous step's data
  // automatically so the operator's choices survive a "Skip setup".
  useEffect(() => {
    if (step === 1) void saveSite();
    if (step === 2) void saveTimezone();
    if (step === 3) void saveLanguage();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 8000,
        background: 'rgba(10,10,10,0.88)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
      }}
    >
      <div
        style={{
          width: 720,
          maxWidth: '100%',
          maxHeight: '92vh',
          background: 'var(--bg-secondary)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        <div style={{ position: 'relative', padding: '20px 24px', borderBottom: '1px solid var(--border)' }}>
          <Bracket pos="tl" />
          <Bracket pos="tr" />
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 2,
                  color: '#F97316',
                  textTransform: 'uppercase',
                  marginBottom: 6,
                }}
              >
                FIRST-RUN SETUP — STEP {step + 1}/{STEPS.length}
              </div>
              <div style={{ fontFamily: 'var(--font-sans)', fontSize: 20, fontWeight: 600, color: 'var(--text-primary)' }}>
                {STEP_TITLES[step]}
              </div>
            </div>
            <button
              onClick={onDismiss}
              style={{
                background: 'transparent',
                border: '1px solid var(--border)',
                borderRadius: 4,
                width: 28,
                height: 28,
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
          <div style={{ display: 'flex', gap: 4, marginTop: 16 }}>
            {STEPS.map((s, i) => (
              <div
                key={s}
                style={{
                  flex: 1,
                  height: 3,
                  background: i <= step ? '#F97316' : 'var(--border)',
                  boxShadow: i === step ? '0 0 8px rgba(249,115,22,0.7)' : 'none',
                  borderRadius: 2,
                  transition: 'all 200ms var(--ease-out)',
                }}
              />
            ))}
          </div>
        </div>

        <div style={{ flex: 1, overflow: 'auto', padding: 24 }}>
          {step === 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <p style={{ margin: 0, fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
                Pick a friendly name for this recorder. It will appear in the topbar
                and notifications.
              </p>
              <Input label="SITE NAME" value={siteName} onChange={setSiteName} />
            </div>
          )}
          {step === 1 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <p style={{ margin: 0, fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
                Detected from this browser. Override if the recorder is in a different timezone.
              </p>
              <Input label="TIMEZONE (IANA)" value={timezone} onChange={setTimezone} mono />
            </div>
          )}
          {step === 2 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <p style={{ margin: 0, fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
                Default UI language for this recorder.
              </p>
              <Input label="LANGUAGE (BCP47)" value={language} onChange={setLanguage} mono />
            </div>
          )}
          {step === 3 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <p style={{ margin: 0, fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
                Optional. Paste your PEM-encoded certificate and key. Leave blank to
                continue using the recorder's self-signed certificate.
              </p>
              <label style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                CERTIFICATE (PEM)
              </label>
              <textarea
                value={tlsCert}
                onChange={(e) => setTlsCert(e.target.value)}
                rows={6}
                style={{
                  background: 'var(--bg-tertiary)',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  padding: 10,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11,
                  color: 'var(--text-primary)',
                }}
              />
              <label style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                PRIVATE KEY (PEM)
              </label>
              <textarea
                value={tlsKey}
                onChange={(e) => setTlsKey(e.target.value)}
                rows={6}
                style={{
                  background: 'var(--bg-tertiary)',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  padding: 10,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11,
                  color: 'var(--text-primary)',
                }}
              />
              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Btn kind="secondary" disabled={tlsSubmitting || !tlsCert || !tlsKey} onClick={saveTLS}>
                  {tlsSubmitting ? 'Uploading…' : 'Upload TLS'}
                </Btn>
              </div>
            </div>
          )}
          {step === 4 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <p style={{ margin: 0, fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
                Optional. Configure outgoing email for notifications. Leave blank to skip.
              </p>
              <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
                <Input label="SMTP HOST" value={smtpHost} onChange={setSmtpHost} mono />
                <Input label="PORT" value={smtpPort} onChange={setSmtpPort} mono />
              </div>
              <Input label="USERNAME" value={smtpUser} onChange={setSmtpUser} />
              <Input label="PASSWORD" value={smtpPass} onChange={setSmtpPass} />
              <Input label="FROM ADDRESS" value={smtpFrom} onChange={setSmtpFrom} />
              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Btn kind="secondary" disabled={!smtpHost} onClick={saveSMTP}>
                  Save SMTP
                </Btn>
              </div>
            </div>
          )}
          {step === 5 && (
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: 16,
                alignItems: 'center',
                textAlign: 'center',
                padding: '20px 0',
              }}
            >
              <div
                style={{
                  width: 64,
                  height: 64,
                  borderRadius: '50%',
                  background: 'rgba(249,115,22,0.13)',
                  border: '2px solid #F97316',
                  boxShadow: '0 0 24px rgba(249,115,22,0.45)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <Icon name="check" style={{ width: 28, height: 28, color: '#F97316', strokeWidth: 3 }} />
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11,
                  letterSpacing: 2,
                  color: '#F97316',
                  textTransform: 'uppercase',
                }}
              >
                READY
              </div>
              <p style={{ margin: 0, fontFamily: 'var(--font-sans)', fontSize: 14, color: 'var(--text-secondary)', maxWidth: 420, lineHeight: 1.5 }}>
                Site "{siteName}" configured. Add cameras from the Cameras page to begin
                recording.
              </p>
            </div>
          )}
        </div>

        <div style={{ padding: '14px 24px', borderTop: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Btn kind="ghostQuiet" onClick={onDismiss}>
            Skip setup
          </Btn>
          <div style={{ display: 'flex', gap: 8 }}>
            {step > 0 && step < STEPS.length - 1 && (
              <Btn kind="secondary" onClick={() => setStep(step - 1)} icon="chevron-left">
                Back
              </Btn>
            )}
            {step < STEPS.length - 1 && (
              <Btn kind="primary" onClick={() => setStep(step + 1)}>
                Continue
                <Icon name="chevron-right" style={{ width: 14, height: 14 }} />
              </Btn>
            )}
            {step === STEPS.length - 1 && (
              <Btn kind="primary" onClick={finish} icon="check" disabled={finishing}>
                {finishing ? 'Finishing…' : 'Enter Dashboard'}
              </Btn>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
