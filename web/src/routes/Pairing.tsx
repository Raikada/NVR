// Pairing route — real backend wiring per
// platform/docs/api-contracts/recorder-management-pairing.md.
//
// Discovery surfaces management servers heard via mDNS through the
// recorder's /v1/recorder/discovered-management endpoint (per pairing
// API contract §8). Pairing itself runs through /v1/recorder/pair +
// status long-poll: the operator types the MS URL + the token they
// received from the MS UI (the QR-with-fingerprint path is a future
// enhancement that pre-fills root_fingerprint), submits, and the UI
// polls until the recorder transitions to a terminal state.
//
// mDNS is informational — clicking a discovered card pre-fills the
// MS URL field but the operator still enters the token. This matches
// the platform's threat model: discovery is unauthenticated, the
// cryptographic pinning via the operator-typed token is the trust
// anchor.

import { useEffect, useRef, useState } from 'react';
import {
  Btn,
  Card,
  Input,
  MiniBlock,
  SectionHeader,
  StatusBadge,
} from '../components/primitives';
import { Icon } from '../components/Icon';
import { PageHeader } from '../components/PageHeader';
import {
  fetchDiscoveredManagement,
  fetchPairStatus,
  resetPairing,
  startPairing,
  unpairRecorder,
  type DiscoveredManagement,
  type PairStatus,
} from '../lib/api';
import type { AppState, ToastInput } from '../lib/types';

interface PairingProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  addToast: (t: ToastInput) => void;
}

const POLL_INTERVAL_MS = 1500;

export function Pairing({ state, setState, addToast }: PairingProps) {
  const [discovered, setDiscovered] = useState<DiscoveredManagement[]>([]);
  const [scanning, setScanning] = useState(false);
  const [serverAddr, setServerAddr] = useState('');
  const [token, setToken] = useState('');
  const [pairStatus, setPairStatus] = useState<PairStatus | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Poll the recorder's pair-status endpoint while a flow is
  // in_progress. Exits when the state becomes terminal.
  const stopPollRef = useRef<(() => void) | null>(null);
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
    const id = window.setInterval(tick, POLL_INTERVAL_MS);
    void tick();
    stopPollRef.current = () => window.clearInterval(id);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [pairStatus?.state, addToast, setState]);

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

  // Initial discovery + a refresh on a 5s cadence while the page is
  // open and the recorder is unpaired. mDNS announcements arrive
  // every ~30s on the LAN, so this is conservative.
  useEffect(() => {
    if (state.paired) return;
    void rescan();
    const id = window.setInterval(rescan, 5000);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.paired]);

  // On mount, also fetch the current pair status so refreshing the
  // page mid-flow doesn't lose the in-progress signal.
  useEffect(() => {
    void fetchPairStatus()
      .then((s) => setPairStatus(s))
      .catch(() => {
        /* idle pre-pair returns idle; non-200 is rare here */
      });
  }, []);

  function useDiscoveredAddress(d: DiscoveredManagement) {
    setServerAddr(d.url);
    addToast({
      kind: 'info',
      title: 'ADDRESS FILLED',
      body: `Enter the pairing token from the MS to complete pairing.`,
      icon: 'info',
    });
  }

  async function pairManually() {
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

  async function clearPairState() {
    try {
      await resetPairing();
    } catch {
      /* ignore — best-effort */
    }
    setPairStatus(null);
    setError(null);
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
        breadcrumb="RECORDING SERVER / PAIRING"
        title="Management Server Pairing"
        sub={
          state.paired
            ? 'Recorder is linked to a management server'
            : 'Recorder is unpaired — discover a management server on your LAN'
        }
        right={
          <Btn kind="tactical" icon="refresh-cw" onClick={rescan} disabled={scanning}>
            {scanning ? 'SCANNING…' : 'RESCAN'}
          </Btn>
        }
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        {state.paired && state.managementServer && (
          <Card>
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'flex-start',
                marginBottom: 14,
              }}
            >
              <div>
                <SectionHeader>CURRENT LINK</SectionHeader>
                <div
                  style={{
                    fontFamily: 'var(--font-sans)',
                    fontSize: 18,
                    fontWeight: 600,
                    color: 'var(--text-primary)',
                    marginTop: 4,
                  }}
                >
                  {state.managementServer.host}
                </div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 11,
                    color: 'var(--text-secondary)',
                    marginTop: 4,
                  }}
                >
                  {state.managementServer.ip} · v{state.managementServer.ver}
                </div>
              </div>
              <StatusBadge kind="paired" />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 10 }}>
              <MiniBlock k="HEARTBEAT" v="2s" />
              <MiniBlock k="LATENCY" v="12 MS" />
              <MiniBlock k="TUNNEL" v="WSS/443" />
              <MiniBlock k="CERT" v="mTLS — 89D" />
            </div>
            <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
              <Btn kind="secondary" icon="activity">
                Test Link
              </Btn>
              {/* Recorder-local unpair: wipes the issued cert +
                * chain + pinned roots via /v1/recorder/unpair. The
                * recorder's UUIDv7 + keypair survive (ADR 0002 D3).
                * The MS still has a pairing record + RecordingServer
                * entry until an MS operator cleans it up there; the
                * MS-initiated unpair flow per pairing-flows.md §2.5
                * lands when the recorder ↔ MS WebSocket exists. */}
              <Btn
                kind="danger"
                icon="unlink"
                onClick={async () => {
                  if (
                    !window.confirm(
                      'Unpair this recorder? The locally-stored DeviceIdentity will be wiped. ' +
                        'The MS will still have a pairing record until an operator cleans it up there. ' +
                        'You can re-pair afterwards. Recordings are not deleted.',
                    )
                  ) {
                    return;
                  }
                  try {
                    await unpairRecorder();
                    setState((s) => ({ ...s, paired: false, managementServer: null }));
                    setPairStatus(null);
                    addToast({
                      kind: 'warning',
                      title: 'UNPAIRED',
                      body: 'Recorder is standalone. The MS may still hold a stale pairing record.',
                      icon: 'unlink',
                    });
                  } catch (e) {
                    addToast({
                      kind: 'danger',
                      title: 'UNPAIR FAILED',
                      body: (e as Error).message,
                      icon: 'alert-triangle',
                    });
                  }
                }}
              >
                Unpair
              </Btn>
            </div>
          </Card>
        )}
        {!state.paired && (
          <Card style={{ padding: 0 }}>
            <div
              style={{
                padding: '14px 16px',
                borderBottom: '1px solid var(--border)',
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}
            >
              <SectionHeader style={{ margin: 0 }}>DISCOVERED SERVERS (mDNS)</SectionHeader>
              {scanning && (
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
                  />{' '}
                  SCANNING
                </span>
              )}
            </div>
            {discovered.map((d, i) => (
              <div
                key={d.ms_id ?? d.hostname}
                style={{
                  padding: 14,
                  borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                  display: 'grid',
                  gridTemplateColumns: '1fr auto',
                  gap: 14,
                  alignItems: 'center',
                }}
              >
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Icon name="server" style={{ width: 14, height: 14, color: '#F97316' }} />
                    <span
                      style={{
                        fontFamily: 'var(--font-sans)',
                        fontSize: 14,
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
                      gap: 16,
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
                <Btn kind="secondary" onClick={() => useDiscoveredAddress(d)}>
                  Use This
                </Btn>
              </div>
            ))}
            {discovered.length === 0 && !scanning && (
              <div
                style={{
                  padding: 30,
                  textAlign: 'center',
                  fontFamily: 'var(--font-sans)',
                  fontSize: 12,
                  color: 'var(--text-muted)',
                }}
              >
                No management servers found via mDNS. Use manual pairing below if your network
                blocks multicast.
              </div>
            )}
          </Card>
        )}
        {!state.paired && (
          <Card>
            <SectionHeader>
              {pairStatus?.state === 'in_progress' ? 'PAIRING IN PROGRESS' : 'PAIR WITH MANAGEMENT SERVER'}
            </SectionHeader>
            <p
              style={{
                fontFamily: 'var(--font-sans)',
                fontSize: 12,
                color: 'var(--text-secondary)',
                lineHeight: 1.5,
                margin: '0 0 12px',
              }}
            >
              Issue a pairing token in the MS UI under "Pair a recorder", then enter the MS
              address and the token here. The MS operator must approve this recorder before
              pairing completes.
            </p>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 12 }}>
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
                placeholder="XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX"
                mono
                disabled={pairStatus?.state === 'in_progress'}
              />
            </div>
            {error && (
              <div
                style={{
                  marginTop: 10,
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
                  marginTop: 12,
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
            <div style={{ marginTop: 12, display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
              {pairStatus && pairStatus.state !== 'idle' && pairStatus.state !== 'in_progress' && (
                <Btn kind="ghost" onClick={clearPairState}>
                  Reset
                </Btn>
              )}
              <Btn
                kind="primary"
                icon="link"
                onClick={pairManually}
                disabled={submitting || pairStatus?.state === 'in_progress'}
              >
                {submitting || pairStatus?.state === 'in_progress' ? 'Pairing…' : 'Pair'}
              </Btn>
            </div>
          </Card>
        )}
      </div>
    </div>
  );
}
