// Onboarding — first-run stepper (also reachable from Settings).
// Welcome → system check (real health/volumes/info) → first camera
// (real WS-Discovery + adopt) → done.
import { useEffect, useState } from 'react';
import { Icon } from '../components/Icon';
import { Brackets, Btn, FormRow, StatusBadge, TxtInput, bareBtn } from '../components/ui';
import {
  ApiError, adoptCamera, fetchHealth, fetchStorageVolumes, getSystemInfo,
  probeDiscovery, type DiscoveredCamera,
} from '../lib/api';
import { useConsole } from '../lib/console';

const STEPS = ['WELCOME', 'SYSTEM CHECK', 'FIRST CAMERA', 'READY'];

export function Onboarding({ onComplete }: { onComplete: () => void }) {
  const [step, setStep] = useState(0);
  const total = STEPS.length;
  const [version, setVersion] = useState('');
  useEffect(() => {
    void getSystemInfo().then((i) => setVersion(i.version)).catch(() => {});
  }, []);

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 200,
      background: 'radial-gradient(ellipse at center, var(--accent-7) 0%, var(--bg-primary) 70%)',
      display: 'flex', flexDirection: 'column',
    }}>
      <header style={{ height: 56, padding: '0 24px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexShrink: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <img src="assets/logo-wordmark.svg" alt="Raikada" style={{ height: 22, display: 'block' }} />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>NVR · FIRST RUN{version ? ` · ${version}` : ''}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>STEP {step + 1} / {total}</span>
          <button onClick={onComplete} style={{ ...bareBtn, fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '.5px', color: 'var(--text-muted)' }}>Skip setup →</button>
        </div>
      </header>

      <div style={{ padding: '20px 24px 0', display: 'flex', gap: 8 }}>
        {STEPS.map((_, i) => (
          <div key={i} style={{ flex: 1, height: 3, background: i <= step ? 'var(--accent)' : 'var(--bg-tertiary)', transition: 'background 200ms' }} />
        ))}
      </div>

      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24, overflowY: 'auto' }}>
        <div style={{ width: '100%', maxWidth: 720 }}>
          {step === 0 && <StepWelcome />}
          {step === 1 && <StepSystem />}
          {step === 2 && <StepCamera />}
          {step === 3 && <StepDone />}
        </div>
      </div>

      <footer style={{ height: 64, padding: '0 24px', borderTop: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexShrink: 0, background: 'var(--bg-secondary)' }}>
        <Btn kind="ghost" onClick={() => setStep((s) => Math.max(0, s - 1))} disabled={step === 0}>
          <Icon name="chevron-left" size={12} />Back
        </Btn>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>{STEPS[step]}</span>
        {step < total - 1 ? (
          <Btn kind="primary" onClick={() => setStep((s) => s + 1)}>Continue<Icon name="chevron-right" size={12} /></Btn>
        ) : (
          <Btn kind="primary" onClick={onComplete}>Enter console<Icon name="arrow-right" size={12} /></Btn>
        )}
      </footer>
    </div>
  );
}

function StepWelcome() {
  return (
    <div style={{ textAlign: 'center', display: 'flex', flexDirection: 'column', gap: 24, alignItems: 'center' }}>
      <div style={{ width: 80, height: 80, position: 'relative' }}>
        <Brackets />
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <img src="assets/logo.png" alt="Raikada" style={{ width: 36, height: 36, display: 'block' }} />
        </div>
      </div>
      <div>
        <h1 style={{ fontFamily: 'var(--font-sans)', fontSize: 32, fontWeight: 700, color: 'var(--text-primary)', margin: 0, letterSpacing: '-0.01em' }}>
          Welcome to your NVR
        </h1>
        <p style={{ fontFamily: 'var(--font-sans)', fontSize: 15, color: 'var(--text-secondary)', maxWidth: 460, margin: '12px auto 0', lineHeight: 1.6 }}>
          A few short steps: we'll check the system and connect your first camera.
        </p>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12, width: '100%', marginTop: 12 }}>
        {([
          ['shield-check', 'LAN-only', 'Footage never leaves your network. No cloud account, no open ports.'],
          ['hard-drive', 'Plain files', 'Standard MP4 segments on your NAS volume. Readable by anything.'],
          ['plug', 'ONVIF & RTSP', 'Works with any standards-compliant camera you already own.'],
        ] as Array<[Parameters<typeof Icon>[0]['name'], string, string]>).map(([icon, title, body]) => (
          <div key={title} style={{ padding: 16, background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, textAlign: 'left', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <Icon name={icon} size={16} color="var(--accent)" />
            <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>{title}</span>
            <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>{body}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function StepSystem() {
  const [checks, setChecks] = useState<Array<{ label: string; status: 'ok' | 'warn' | 'pending'; detail: string }>>([
    { label: 'Recorder services', status: 'pending', detail: '…' },
    { label: 'Recording volume', status: 'pending', detail: '…' },
  ]);
  useEffect(() => {
    void fetchHealth().then((h) => {
      setChecks((cs) => cs.map((c) => c.label === 'Recorder services'
        ? { ...c, status: h.overall === 'healthy' ? 'ok' : 'warn', detail: `${h.overall} · cpu ${h.cpu_pct.toFixed(0)}%` }
        : c));
    }).catch(() => {
      setChecks((cs) => cs.map((c) => c.label === 'Recorder services' ? { ...c, status: 'warn', detail: 'health endpoint unreachable' } : c));
    });
    void fetchStorageVolumes().then((r) => {
      const vols = r.items ?? [];
      const cap = vols.reduce((a, v) => a + v.capacity_bytes, 0);
      setChecks((cs) => cs.map((c) => c.label === 'Recording volume'
        ? {
            ...c,
            status: vols.length > 0 && vols.every((v) => v.status === 'healthy') ? 'ok' : 'warn',
            detail: vols.length > 0 ? `${vols[0].mount_path} · ${(cap / 1e12).toFixed(1)} TB` : 'no volume reported',
          }
        : c));
    }).catch(() => {});
  }, []);
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div>
        <h2 style={{ fontFamily: 'var(--font-sans)', fontSize: 24, fontWeight: 600, color: 'var(--text-primary)', margin: 0 }}>System check</h2>
        <p style={{ fontFamily: 'var(--font-sans)', fontSize: 14, color: 'var(--text-secondary)', margin: '8px 0 0', lineHeight: 1.6 }}>
          Verifying the recorder has everything it needs before cameras come online.
        </p>
      </div>
      <div style={{ background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, overflow: 'hidden' }}>
        {checks.map((c, i) => (
          <div key={c.label} style={{ padding: '14px 16px', borderBottom: i < checks.length - 1 ? '1px solid var(--border)' : 'none', display: 'flex', alignItems: 'center', gap: 14 }}>
            <Icon
              name={c.status === 'ok' ? 'check-circle-2' : c.status === 'warn' ? 'alert-circle' : 'refresh-cw'}
              size={18}
              color={c.status === 'ok' ? 'var(--success)' : c.status === 'warn' ? 'var(--warning)' : 'var(--text-muted)'}
            />
            <span style={{ fontFamily: 'var(--font-sans)', fontSize: 14, color: 'var(--text-primary)', flex: 1 }}>{c.label}</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>{c.detail}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function StepCamera() {
  const data = useConsole();
  const [scanning, setScanning] = useState(false);
  const [found, setFound] = useState<DiscoveredCamera[]>([]);
  const [adopting, setAdopting] = useState<DiscoveredCamera | null>(null);
  const [name, setName] = useState('');
  const [user, setUser] = useState('admin');
  const [pass, setPass] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState('');

  const scan = () => {
    setScanning(true);
    void probeDiscovery()
      .then((r) => setFound((r.items ?? []).filter((d) => !d.matched_camera_id)))
      .catch(() => {})
      .finally(() => setScanning(false));
  };
  useEffect(scan, []);

  const adopt = async () => {
    if (!adopting) return;
    setBusy(true);
    setErr('');
    try {
      await adoptCamera({ endpoint_reference: adopting.endpoint_reference, name, rtsp_username: user, rtsp_password: pass });
      setDone(name);
      setAdopting(null);
      data.refreshCameras();
      scan();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'adopt failed');
    }
    setBusy(false);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div>
        <h2 style={{ fontFamily: 'var(--font-sans)', fontSize: 24, fontWeight: 600, color: 'var(--text-primary)', margin: 0 }}>Add your first camera</h2>
        <p style={{ fontFamily: 'var(--font-sans)', fontSize: 14, color: 'var(--text-secondary)', margin: '8px 0 0', lineHeight: 1.6 }}>
          Scanning the local network for ONVIF devices. Pick one to adopt now — you can add more later under Devices.
        </p>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: 1, color: 'var(--accent)' }}>
          {scanning ? 'SCANNING…' : `${found.length} NEW DEVICE${found.length === 1 ? '' : 'S'} FOUND${data.cameras.length ? ` · ${data.cameras.length} ADOPTED` : ''}`}
        </span>
        <Btn kind="secondary" onClick={scan} disabled={scanning}><Icon name="refresh-cw" size={12} />Rescan</Btn>
      </div>
      {done && (
        <div style={{ padding: 10, background: 'var(--success-7)', border: '1px solid var(--success-27)', display: 'flex', alignItems: 'center', gap: 8, borderRadius: 4 }}>
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--success)', boxShadow: '0 0 6px var(--success)' }} />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--success)', letterSpacing: '.5px' }}>ADOPTED · {done}</span>
        </div>
      )}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {found.map((d) => (
          <div key={d.endpoint_reference} style={{ padding: 14, background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, display: 'flex', alignItems: 'center', gap: 14, animation: 'fadein 300ms ease-out' }}>
            <div style={{ width: 64, height: 40, position: 'relative', background: '#0d0c0a' }}>
              <Brackets />
            </div>
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
              <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)' }}>
                {[d.manufacturer, d.model].filter(Boolean).join(' ') || d.name || 'ONVIF device'}
              </span>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{d.xaddr}</span>
            </div>
            <Btn kind="primary" onClick={() => {
              setAdopting(d);
              setName((d.model || d.name || 'camera').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, ''));
            }}>ADD</Btn>
          </div>
        ))}
        {!scanning && found.length === 0 && (
          <div style={{ padding: 28, textAlign: 'center', fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
            No unadopted devices found. You can add cameras manually under Devices.
          </div>
        )}
      </div>
      {adopting && (
        <div style={{ padding: 16, background: 'var(--bg-secondary)', border: '1px solid var(--accent-27)', borderRadius: 6, display: 'flex', flexDirection: 'column', gap: 12, animation: 'fadein 200ms ease-out' }}>
          <StatusBadge kind="live" label={`ADOPT · ${[adopting.manufacturer, adopting.model].filter(Boolean).join(' ')}`} />
          <FormRow label="Name"><TxtInput value={name} onChange={setName} /></FormRow>
          <FormRow label="Username"><TxtInput value={user} onChange={setUser} /></FormRow>
          <FormRow label="Password"><TxtInput type="password" value={pass} onChange={setPass} /></FormRow>
          {err && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--danger)' }}>{err}</span>}
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Btn kind="ghost" onClick={() => setAdopting(null)}>Cancel</Btn>
            <Btn kind="primary" disabled={busy || !name} onClick={() => void adopt()}>{busy ? 'Probing…' : 'Adopt camera'}</Btn>
          </div>
        </div>
      )}
    </div>
  );
}

function StepDone() {
  const data = useConsole();
  return (
    <div style={{ textAlign: 'center', display: 'flex', flexDirection: 'column', gap: 20, alignItems: 'center' }}>
      <div style={{ width: 64, height: 64, borderRadius: '50%', background: 'var(--success-7)', border: '1px solid var(--success-27)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Icon name="check" size={30} color="var(--success)" />
      </div>
      <h2 style={{ fontFamily: 'var(--font-sans)', fontSize: 28, fontWeight: 600, color: 'var(--text-primary)', margin: 0 }}>You're set up.</h2>
      <p style={{ fontFamily: 'var(--font-sans)', fontSize: 14, color: 'var(--text-secondary)', maxWidth: 460, margin: 0, lineHeight: 1.6 }}>
        {data.cameras.length} camera{data.cameras.length === 1 ? '' : 's'} connected. Everything can be changed later.
      </p>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8, width: '100%', marginTop: 8 }}>
        {['Press C to toggle the camera panel', 'Press S to toggle alerts', 'Press / to search events'].map((h) => (
          <div key={h} style={{ padding: '10px 12px', background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', letterSpacing: '.5px' }}>{h}</div>
        ))}
      </div>
    </div>
  );
}
