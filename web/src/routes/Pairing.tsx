// Pairing route — LAN autodiscover + manual fallback. Faithful port
// of the design's PairingRoute. Discovery seeds three candidate MS
// hosts over ~1.4 seconds; pairing/unpairing is local-state only
// (mock).

import { useEffect, useState } from 'react';
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
import type { AppState, ManagementServer, ToastInput } from '../lib/types';

interface PairingProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  addToast: (t: ToastInput) => void;
}

const POOL: ManagementServer[] = [
  { host: 'ms-prod-01.local', ip: '10.0.1.21', mac: 'AC:DE:48:00:11:22', cameras: 24, ver: '4.2.1', trust: 'SIGNED' },
  { host: 'ms-backup-02.local', ip: '10.0.1.22', mac: 'AC:DE:48:00:11:33', cameras: 8, ver: '4.2.0', trust: 'SIGNED' },
  { host: 'ms-lab-dev.local', ip: '10.0.1.45', mac: 'AC:DE:48:00:22:01', cameras: 2, ver: '4.3.0-beta', trust: 'SELF' },
];

export function Pairing({ state, setState, addToast }: PairingProps) {
  const [scanning, setScanning] = useState(false);
  const [found, setFound] = useState<ManagementServer[]>([]);
  const [serverAddr, setServerAddr] = useState('');
  const [token, setToken] = useState('');

  function rescan() {
    setScanning(true);
    setFound([]);
    POOL.forEach((c, i) =>
      window.setTimeout(() => setFound((prev) => [...prev, c]), 500 + i * 450),
    );
    window.setTimeout(() => setScanning(false), 500 + POOL.length * 450 + 500);
  }

  useEffect(() => {
    if (!state.paired) rescan();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function pair(c: ManagementServer) {
    setState((s) => ({ ...s, paired: true, managementServer: c }));
    addToast({ kind: 'success', title: 'PAIRED', body: `Linked to ${c.host}`, icon: 'check-circle' });
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
              <Btn
                kind="danger"
                icon="unlink"
                onClick={() => {
                  setState((s) => ({ ...s, paired: false, managementServer: null }));
                  addToast({
                    kind: 'warning',
                    title: 'UNPAIRED',
                    body: 'Recorder is standalone',
                    icon: 'unlink',
                  });
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
              <SectionHeader style={{ margin: 0 }}>DISCOVERED SERVERS</SectionHeader>
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
            {found.map((c, i) => (
              <div
                key={c.host}
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
                      {c.host}
                    </span>
                    <StatusBadge kind="online" label="REACHABLE" size="sm" />
                    <StatusBadge
                      kind={c.trust === 'SIGNED' ? 'paired' : 'warn'}
                      label={c.trust === 'SIGNED' ? 'TRUSTED CERT' : 'SELF-SIGNED'}
                      size="sm"
                    />
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
                    <span>{c.ip}</span>
                    <span>{c.mac}</span>
                    <span>{c.cameras} CAM MANAGED</span>
                    <span>v{c.ver}</span>
                  </div>
                </div>
                <Btn kind="primary" onClick={() => pair(c)}>
                  Pair
                </Btn>
              </div>
            ))}
            {found.length === 0 && !scanning && (
              <div
                style={{
                  padding: 30,
                  textAlign: 'center',
                  fontFamily: 'var(--font-sans)',
                  fontSize: 12,
                  color: 'var(--text-muted)',
                }}
              >
                No management servers found. Ensure a server is reachable on this subnet.
              </div>
            )}
          </Card>
        )}
        <Card>
          <SectionHeader>MANUAL PAIRING</SectionHeader>
          <p
            style={{
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: 'var(--text-secondary)',
              lineHeight: 1.5,
              margin: '0 0 12px',
            }}
          >
            If autodiscovery is blocked (different VLAN, firewall), enter the management server address and use a
            bearer token to pair.
          </p>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 12 }}>
            <Input
              label="SERVER ADDRESS"
              value={serverAddr}
              onChange={setServerAddr}
              placeholder="https://ms.example.com:7443"
              mono
            />
            <Input
              label="PAIRING TOKEN"
              value={token}
              onChange={setToken}
              placeholder="XXXX-XXXX-XXXX"
              mono
            />
          </div>
          <div style={{ marginTop: 12, display: 'flex', justifyContent: 'flex-end' }}>
            <Btn kind="secondary" icon="link">
              Pair Manually
            </Btn>
          </div>
        </Card>
      </div>
    </div>
  );
}
