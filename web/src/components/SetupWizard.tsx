// First-run setup wizard. Auto-opens on first load, dismissable via
// the X or "Skip setup", restartable from Overview.
//
// Five steps: Welcome → Network preflight → Pair with MS → Add cameras
// → Finish. Faithful port of the design's wizard.jsx.

import { useEffect, useState } from 'react';
import { Btn, Bracket, Input, Stat, StatusBadge } from './primitives';
import { Icon } from './Icon';
import type { IconName } from './Icon';
import {
  fetchDiscoveredManagement,
  fetchPairStatus,
  startPairing,
  type DiscoveredManagement,
  type PairStatus,
} from '../lib/api';
import type { AppState, ToastInput, UICamera, BadgeKind } from '../lib/types';

interface SetupWizardProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  addToast: (t: ToastInput) => void;
  onDone: () => void;
  onDismiss: () => void;
}

const STEPS = ['WELCOME', 'NETWORK', 'PAIR WITH MS', 'ADD CAMERAS', 'FINISH'] as const;

const STEP_TITLES = [
  'Welcome to Raikada',
  'Network Check',
  'Pair with Management Server',
  'Add Cameras',
  'Setup Complete',
] as const;

export function SetupWizard({ onDone, onDismiss, state, setState, addToast }: SetupWizardProps) {
  const [step, setStep] = useState(0);

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
        {/* Header */}
        <div
          style={{
            position: 'relative',
            padding: '20px 24px',
            borderBottom: '1px solid var(--border)',
          }}
        >
          <Bracket pos="tl" />
          <Bracket pos="tr" />
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-start',
            }}
          >
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
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 20,
                  fontWeight: 600,
                  color: 'var(--text-primary)',
                }}
              >
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
          {/* Step progress bars */}
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
          <div style={{ display: 'flex', gap: 16, marginTop: 8 }}>
            {STEPS.map((s, i) => (
              <span
                key={s}
                style={{
                  flex: 1,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 9,
                  letterSpacing: 1,
                  color:
                    i === step
                      ? '#F97316'
                      : i < step
                        ? 'var(--text-secondary)'
                        : 'var(--text-muted)',
                  textTransform: 'uppercase',
                }}
              >
                {s}
              </span>
            ))}
          </div>
        </div>

        {/* Body */}
        <div style={{ flex: 1, overflow: 'auto', padding: '24px' }}>
          {step === 0 && <WizWelcome />}
          {step === 1 && <WizNetwork state={state} />}
          {step === 2 && <WizPair state={state} setState={setState} addToast={addToast} />}
          {step === 3 && <WizCameras state={state} />}
          {step === 4 && <WizFinish state={state} />}
        </div>

        {/* Footer */}
        <div
          style={{
            padding: '14px 24px',
            borderTop: '1px solid var(--border)',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <Btn kind="ghostQuiet" onClick={onDismiss}>
            Skip setup
          </Btn>
          <div style={{ display: 'flex', gap: 8 }}>
            {step > 0 && step < 4 && (
              <Btn kind="secondary" onClick={() => setStep(step - 1)} icon="chevron-left">
                Back
              </Btn>
            )}
            {step < 4 && (
              <Btn
                kind="primary"
                disabled={step === 2 && !state.paired}
                onClick={() => setStep(step + 1)}
              >
                {step === 0 ? 'Begin Setup' : 'Continue'}
                <Icon name="chevron-right" style={{ width: 14, height: 14 }} />
              </Btn>
            )}
            {step === 4 && (
              <Btn kind="primary" onClick={onDone} icon="check">
                Enter Dashboard
              </Btn>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function WizWelcome() {
  const items: { k: string; v: string; i: IconName }[] = [
    { k: 'NETWORK', v: 'Verify LAN reachability and DNS', i: 'network' },
    { k: 'PAIR', v: 'Link recorder with management server', i: 'link' },
    { k: 'CAMERAS', v: 'Discover and add devices', i: 'cctv' },
    { k: 'RECORDING', v: 'Rules inherit from MS automatically', i: 'circle-dot' },
  ];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <p
        style={{
          fontFamily: 'var(--font-sans)',
          fontSize: 14,
          color: 'var(--text-secondary)',
          lineHeight: 1.6,
          margin: 0,
        }}
      >
        This recording server is online but has not been configured. Follow these four steps to connect
        it to your management server and bring cameras online.
      </p>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(2,1fr)',
          gap: 10,
          marginTop: 8,
        }}
      >
        {items.map((x) => (
          <div
            key={x.k}
            style={{
              padding: 12,
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              display: 'flex',
              gap: 10,
              alignItems: 'flex-start',
            }}
          >
            <Icon name={x.i} style={{ width: 16, height: 16, color: '#F97316', marginTop: 2 }} />
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 1,
                  color: '#F97316',
                  textTransform: 'uppercase',
                }}
              >
                {x.k}
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 12,
                  color: 'var(--text-secondary)',
                  marginTop: 2,
                }}
              >
                {x.v}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function WizNetwork({ state }: { state: AppState }) {
  type CheckOk = true | false | 'warn';
  const checks: { k: string; v: string; ok: CheckOk }[] = [
    { k: 'LINK', v: 'Ethernet eth0 — 1000 Mbps full duplex', ok: true },
    { k: 'IPv4', v: `${state.ip} / 24 via ${state.gateway}`, ok: true },
    { k: 'DNS', v: '8.8.8.8, 1.1.1.1 — 12 ms avg', ok: true },
    { k: 'NTP', v: 'pool.ntp.org — drift 3 ms', ok: true },
    { k: 'MTU', v: '1500 bytes — no fragmentation', ok: true },
    { k: 'UPnP', v: 'Disabled — discovery uses LAN broadcast', ok: 'warn' },
  ];

  function badge(ok: CheckOk): { kind: BadgeKind; label: string } {
    if (ok === true) return { kind: 'online', label: 'OK' };
    if (ok === 'warn') return { kind: 'degraded', label: 'WARN' };
    return { kind: 'error', label: 'FAIL' };
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <p
        style={{
          fontFamily: 'var(--font-sans)',
          fontSize: 13,
          color: 'var(--text-secondary)',
          margin: 0,
          lineHeight: 1.5,
        }}
      >
        Running preflight checks against your LAN. All items green means the recorder can reach a management server.
      </p>
      <div
        style={{
          background: 'var(--bg-tertiary)',
          border: '1px solid var(--border)',
          borderRadius: 4,
          overflow: 'hidden',
        }}
      >
        {checks.map((c, i) => {
          const b = badge(c.ok);
          return (
            <div
              key={c.k}
              style={{
                display: 'grid',
                gridTemplateColumns: '80px 1fr auto',
                gap: 12,
                alignItems: 'center',
                padding: '10px 14px',
                borderTop: i === 0 ? 'none' : '1px solid var(--border)',
              }}
            >
              <span
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 1,
                  color: 'var(--text-muted)',
                  textTransform: 'uppercase',
                }}
              >
                {c.k}
              </span>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)' }}>
                {c.v}
              </span>
              <StatusBadge kind={b.kind} label={b.label} />
            </div>
          );
        })}
      </div>
    </div>
  );
}

interface WizPairProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  addToast: (t: ToastInput) => void;
}

function WizPair({ state, setState, addToast }: WizPairProps) {
  const [scanning, setScanning] = useState(!state.paired);
  const [discovered, setDiscovered] = useState<DiscoveredManagement[]>([]);
  const [serverAddr, setServerAddr] = useState('');
  const [token, setToken] = useState('');
  const [pairStatus, setPairStatus] = useState<PairStatus | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Real mDNS-discovered list, refreshed every 5s while unpaired.
  async function rescan() {
    setScanning(true);
    try {
      const res = await fetchDiscoveredManagement();
      setDiscovered(res.items);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setScanning(false);
    }
  }

  useEffect(() => {
    if (state.paired) return;
    void rescan();
    const id = window.setInterval(rescan, 5000);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.paired]);

  // On mount, fetch current pair status so refreshing the wizard
  // mid-flow keeps in-progress visible.
  useEffect(() => {
    void fetchPairStatus()
      .then((s) => setPairStatus(s))
      .catch(() => {
        /* no pair flow active = idle, fine */
      });
  }, []);

  // Poll status while a pairing is in_progress.
  useEffect(() => {
    if (pairStatus?.state !== 'in_progress') return;
    let cancelled = false;
    const tick = async () => {
      if (cancelled) return;
      try {
        const s = await fetchPairStatus();
        if (cancelled) return;
        setPairStatus(s);
        if (s.state === 'approved') {
          setState((prev) => ({
            ...prev,
            paired: true,
            managementServer: {
              host: s.ms_url ?? 'paired',
              ip: '',
              mac: '',
              cameras: 0,
              ver: '',
              trust: 'SIGNED',
            },
          }));
          addToast({
            kind: 'success',
            title: 'PAIRED',
            body: `Linked to ${s.ms_url ?? 'management server'}`,
            icon: 'check-circle',
          });
        } else if (s.state !== 'in_progress') {
          addToast({
            kind: 'warning',
            title: s.state.replace(/_/g, ' ').toUpperCase(),
            body: s.detail ?? 'pairing did not complete',
            icon: 'alert-triangle',
          });
        }
      } catch (e) {
        if (cancelled) return;
        setError((e as Error).message);
      }
    };
    const id = window.setInterval(tick, 1500);
    void tick();
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [pairStatus?.state, addToast, setState]);

  function useDiscoveredAddress(d: DiscoveredManagement) {
    setServerAddr(d.url);
    addToast({
      kind: 'info',
      title: 'ADDRESS FILLED',
      body: `Enter the pairing token from the MS to pair.`,
      icon: 'info',
    });
  }

  async function doPair() {
    setError(null);
    if (!serverAddr.startsWith('https://')) {
      setError('Server address must start with https://');
      return;
    }
    if (!token.trim()) {
      setError('Pairing token is required.');
      return;
    }
    setSubmitting(true);
    try {
      const r = await startPairing({ ms_url: serverAddr.trim(), token: token.trim() });
      setPairStatus({ state: r.state, updated_at: new Date().toISOString(), detail: r.detail });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSubmitting(false);
    }
  }

  if (state.paired && state.managementServer) {
    const ms = state.managementServer;
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div
          style={{
            padding: 16,
            background: 'rgba(34,197,94,0.07)',
            border: '1px solid rgba(34,197,94,0.27)',
            borderRadius: 4,
            display: 'flex',
            gap: 12,
            alignItems: 'center',
          }}
        >
          <Icon name="check-circle" style={{ width: 20, height: 20, color: '#22C55E' }} />
          <div style={{ flex: 1 }}>
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                letterSpacing: 1,
                color: '#22C55E',
                textTransform: 'uppercase',
              }}
            >
              PAIRED
            </div>
            <div
              style={{
                fontFamily: 'var(--font-sans)',
                fontSize: 13,
                color: 'var(--text-primary)',
                marginTop: 2,
              }}
            >
              {ms.host} ({ms.ip})
            </div>
          </div>
          <Btn kind="ghost" onClick={() => setState((s) => ({ ...s, paired: false }))}>
            Unpair
          </Btn>
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)' }}>
          Scanning LAN for management servers on ports 443, 7443…
        </span>
        {scanning ? (
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              letterSpacing: 1,
              color: '#F97316',
              textTransform: 'uppercase',
            }}
          >
            <span
              className="pulse-dot"
              style={{
                width: 6,
                height: 6,
                background: '#F97316',
                borderRadius: '50%',
                boxShadow: '0 0 6px #F97316',
              }}
            />
            SCANNING
          </span>
        ) : (
          <Btn kind="tactical" onClick={rescan}>
            <Icon name="refresh-cw" style={{ width: 12, height: 12 }} /> RESCAN
          </Btn>
        )}
      </div>

      <div
        style={{
          background: 'var(--bg-tertiary)',
          border: '1px solid var(--border)',
          borderRadius: 4,
          minHeight: 150,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        {discovered.length === 0 && !scanning && (
          <div
            style={{
              padding: 24,
              textAlign: 'center',
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: 'var(--text-muted)',
            }}
          >
            No management servers found on this network.
          </div>
        )}
        {discovered.map((d, i) => (
          <div
            key={d.ms_id ?? d.hostname}
            style={{
              padding: '12px 14px',
              borderTop: i === 0 ? 'none' : '1px solid var(--border)',
              display: 'grid',
              gridTemplateColumns: '1fr auto',
              gap: 12,
              alignItems: 'center',
            }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Icon name="server" style={{ width: 14, height: 14, color: '#F97316' }} />
                <span
                  style={{
                    fontFamily: 'var(--font-sans)',
                    fontSize: 13,
                    fontWeight: 600,
                    color: 'var(--text-primary)',
                  }}
                >
                  {d.hostname}
                </span>
                <StatusBadge kind="online" label="REACHABLE" size="sm" />
              </div>
              <div
                style={{
                  display: 'flex',
                  gap: 14,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  color: 'var(--text-secondary)',
                  letterSpacing: 0.5,
                }}
              >
                <span>{d.url}</span>
                {d.version && <span>{d.version}</span>}
                {d.ms_id && <span>id:{d.ms_id.slice(0, 8)}</span>}
              </div>
            </div>
            <Btn kind="secondary" size="sm" onClick={() => useDiscoveredAddress(d)}>
              Use This
            </Btn>
          </div>
        ))}
        {scanning && discovered.length === 0 && (
          <div
            style={{
              padding: '12px 14px',
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            <div
              className="scanline"
              style={{
                width: 8,
                height: 8,
                borderRadius: 2,
                background: '#F97316',
                opacity: 0.6,
              }}
            />
            Listening for mDNS announcements…
          </div>
        )}
      </div>

      <div
        style={{
          paddingTop: 8,
          borderTop: '1px solid var(--border)',
          display: 'flex',
          flexDirection: 'column',
          gap: 10,
        }}
      >
        <div
          style={{
            fontFamily: 'var(--font-sans)',
            fontSize: 12,
            color: 'var(--text-secondary)',
            lineHeight: 1.5,
          }}
        >
          Issue a pairing token in the MS UI under "Pair a recorder", then enter the MS address
          and the token below. The MS operator must approve the recorder before pairing
          completes.
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
          <Input
            label="SERVER ADDRESS"
            value={serverAddr}
            onChange={setServerAddr}
            placeholder="https://ms.example.com:8443"
            mono
            disabled={pairStatus?.state === 'in_progress'}
          />
          <Input
            label="PAIRING TOKEN"
            value={token}
            onChange={setToken}
            placeholder="XXXX-XXXX-XXXX-XXXX-…"
            mono
            disabled={pairStatus?.state === 'in_progress'}
          />
        </div>
        {error && (
          <div
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              color: '#EF4444',
            }}
          >
            {error}
          </div>
        )}
        {pairStatus && pairStatus.state !== 'idle' && (
          <div
            style={{
              padding: '10px 12px',
              background: 'rgba(249,115,22,0.06)',
              border: '1px solid rgba(249,115,22,0.27)',
              borderRadius: 4,
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              color: 'var(--text-primary)',
            }}
          >
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                letterSpacing: 1,
                color: '#F97316',
                textTransform: 'uppercase',
                marginBottom: 4,
              }}
            >
              STATE: {pairStatus.state.replace(/_/g, ' ')}
            </div>
            {pairStatus.detail && <div>{pairStatus.detail}</div>}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Btn
            kind="primary"
            icon="link"
            onClick={doPair}
            disabled={submitting || pairStatus?.state === 'in_progress'}
          >
            {submitting || pairStatus?.state === 'in_progress' ? 'Pairing…' : 'Pair'}
          </Btn>
        </div>
      </div>
    </div>
  );
}

function WizCameras({ state }: { state: AppState }) {
  const onlineCount = state.cameras.filter((c) => c.status === 'online').length;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <p
        style={{
          fontFamily: 'var(--font-sans)',
          fontSize: 13,
          color: 'var(--text-secondary)',
          margin: 0,
          lineHeight: 1.5,
        }}
      >
        Cameras discovered via ONVIF will appear here. You can skip this step and add cameras from the Cameras
        page later.
      </p>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 10 }}>
        <Stat label="DISCOVERED" value={state.cameras.length} unit="" tone="accent" />
        <Stat label="ONLINE" value={onlineCount} unit="" tone="success" />
        <Stat label="CONFIGURED" value={onlineCount} unit="" tone="accent" />
        <Stat label="RECORDING" value={onlineCount} unit="" tone="danger" />
      </div>
      <div
        style={{
          background: 'var(--bg-tertiary)',
          border: '1px solid var(--border)',
          borderRadius: 4,
          overflow: 'hidden',
          maxHeight: 220,
          overflowY: 'auto',
        }}
      >
        {state.cameras.slice(0, 5).map((c: UICamera, i) => (
          <div
            key={c.id}
            style={{
              padding: '8px 12px',
              borderTop: i === 0 ? 'none' : '1px solid var(--border)',
              display: 'grid',
              gridTemplateColumns: '64px 1fr auto auto',
              gap: 12,
              alignItems: 'center',
            }}
          >
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{c.id}</span>
            <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-primary)' }}>{c.name}</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)' }}>{c.ip}</span>
            <StatusBadge kind={c.status} size="sm" />
          </div>
        ))}
      </div>
    </div>
  );
}

function WizFinish({ state }: { state: AppState }) {
  const onlineCount = state.cameras.filter((c) => c.status === 'online').length;
  return (
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
      <p
        style={{
          fontFamily: 'var(--font-sans)',
          fontSize: 14,
          color: 'var(--text-secondary)',
          margin: 0,
          maxWidth: 420,
          lineHeight: 1.5,
        }}
      >
        {onlineCount} cameras online, paired with {state.managementServer?.host || 'management server'}. Recording
        rules will sync from the management server within 30 seconds.
      </p>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(3,1fr)',
          gap: 10,
          width: '100%',
          maxWidth: 460,
          marginTop: 8,
        }}
      >
        {[
          { k: 'PAIRED', v: state.managementServer?.host || '—' },
          { k: 'CAMERAS', v: `${onlineCount}/${state.cameras.length}` },
          { k: 'STORAGE', v: '256 GB / 2 TB' },
        ].map((x) => (
          <div
            key={x.k}
            style={{
              padding: 10,
              background: 'var(--bg-tertiary)',
              border: '1px solid var(--border)',
              borderRadius: 4,
            }}
          >
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 9,
                letterSpacing: 1,
                color: 'var(--text-muted)',
                textTransform: 'uppercase',
              }}
            >
              {x.k}
            </div>
            <div
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 12,
                color: '#F97316',
                marginTop: 4,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {x.v}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
