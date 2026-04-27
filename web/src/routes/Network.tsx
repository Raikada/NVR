// Network route — interface details, ports/protocols list, bandwidth
// stats. Faithful port of the design's NetworkRoute.

import {
  Btn,
  Card,
  MiniBlock,
  SectionHeader,
  Segmented,
  Stat,
  StatusBadge,
} from '../components/primitives';
import type { StatusBadgeKind } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import type { AppState } from '../lib/types';

export function Network({ state }: { state: AppState }) {
  type PortStatus = true | false | 'warn';
  const ports: { p: string; v: string; ok: PortStatus }[] = [
    { p: 'HTTPS / Web UI', v: '443', ok: true },
    { p: 'RTSP ingest', v: '554', ok: true },
    { p: 'ONVIF discovery', v: '3702', ok: true },
    { p: 'MS tunnel (WSS)', v: '7443', ok: state.paired },
    { p: 'SNMP', v: '161', ok: 'warn' },
    { p: 'SSH', v: '22', ok: 'warn' },
  ];

  function badge(ok: PortStatus): { kind: StatusBadgeKind; label: string } {
    if (ok === true) return { kind: 'online', label: 'LISTEN' };
    if (ok === 'warn') return { kind: 'degraded', label: 'RESTRICTED' };
    return { kind: 'offline', label: 'CLOSED' };
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
        breadcrumb="RECORDING SERVER / NETWORK"
        title="Network"
        sub="Primary: eth0 · DHCP reservation · gateway 10.0.1.1"
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <Card>
          <SectionHeader
            right={
              <Btn kind="ghost" size="sm">
                Edit
              </Btn>
            }
          >
            INTERFACE · ETH0
          </SectionHeader>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 10, marginTop: 6 }}>
            <MiniBlock k="LINK" v="1 GBPS FD" />
            <MiniBlock k="IPV4" v={state.ip} />
            <MiniBlock k="GATEWAY" v={state.gateway} />
            <MiniBlock k="DNS" v="8.8.8.8, 1.1.1.1" />
            <MiniBlock k="MAC" v="B8:27:EB:E4:12:03" />
            <MiniBlock k="MTU" v="1500" />
            <MiniBlock k="NTP" v="POOL.NTP.ORG" />
            <MiniBlock k="VLAN" v="UNTAGGED" />
          </div>
        </Card>
        <Card>
          <SectionHeader>PORTS & PROTOCOLS</SectionHeader>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 10, marginTop: 8 }}>
            {ports.map((r) => {
              const b = badge(r.ok);
              return (
                <div
                  key={r.p}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '1fr auto auto',
                    alignItems: 'center',
                    gap: 10,
                    padding: '10px 12px',
                    background: 'var(--bg-tertiary)',
                    border: '1px solid var(--border)',
                    borderRadius: 4,
                  }}
                >
                  <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-primary)' }}>
                    {r.p}
                  </span>
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: '#F97316' }}>:{r.v}</span>
                  <StatusBadge kind={b.kind} label={b.label} size="sm" />
                </div>
              );
            })}
          </div>
        </Card>
        <Card>
          <SectionHeader
            right={
              <Segmented
                size="sm"
                options={['INGRESS', 'EGRESS'] as const}
                value="INGRESS"
                onChange={() => {}}
              />
            }
          >
            BANDWIDTH USAGE
          </SectionHeader>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12, marginTop: 8 }}>
            <Stat label="NOW" value="18.4" unit="Mb/s" />
            <Stat label="PEAK · 24H" value="34.2" unit="Mb/s" />
            <Stat label="AVG · 24H" value="17.1" unit="Mb/s" />
          </div>
        </Card>
      </div>
    </div>
  );
}
