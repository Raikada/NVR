// Network route — interface details, ports/protocols list,
// bandwidth stats. The recorder's /v1/recorder/config block exposes
// which protocol servers are enabled (api, rtsp, rtmp, hls, webrtc,
// srt) so the ports list reflects actual recorder config rather
// than design constants. Bandwidth and per-interface link details
// (gateway, DNS, MAC, MTU, VLAN) stay STUB — recorder doesn't
// expose system network probe data today.

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
import { fetchHealth, fetchRecorderConfig } from '../lib/api';
import { useFetch, usePoll } from '../lib/hooks';
import type { AppState } from '../lib/types';

export function Network({ state }: { state: AppState }) {
  const config = useFetch(fetchRecorderConfig, []);
  const health = usePoll(fetchHealth, 5000, []);

  type PortStatus = true | false | 'warn';
  // Recorder's GlobalConf has bool toggles for the server families.
  // Coerce the unknown to bool so the UI surfaces actual state.
  const cfg = config.data ?? {};
  const enabled = (k: string) => Boolean(cfg[k]);

  const ports: { p: string; v: string; ok: PortStatus }[] = [
    { p: 'HTTPS / Web UI', v: '9997', ok: enabled('api') },
    { p: 'RTSP ingest', v: '554', ok: enabled('rtsp') },
    { p: 'RTMP ingest', v: '1935', ok: enabled('rtmp') },
    { p: 'HLS', v: '8888', ok: enabled('hls') },
    { p: 'WebRTC', v: '8889', ok: enabled('webrtc') },
    { p: 'SRT', v: '8890', ok: enabled('srt') },
    { p: 'MS tunnel (WSS)', v: '7443', ok: state.paired }, // STUB until pairing client lands
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
        sub={
          health.data
            ? `MS reachable: ${health.data.network.management_server_reachable ? 'yes' : 'no'} · Cloud reachable: ${health.data.network.cloud_reachable ? 'yes' : 'no'}`
            : 'Loading network probe…'
        }
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <Card>
          <SectionHeader>NETWORK REACHABILITY</SectionHeader>
          {/* /v1/health.network: TCP-reachability probes against
              configured MS/Cloud endpoints. last_sync_at stays nil
              until the MS pairing client lands. */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10, marginTop: 6 }}>
            <MiniBlock
              k="MS REACHABLE"
              v={health.data ? (health.data.network.management_server_reachable ? 'YES' : 'NO') : '—'}
            />
            <MiniBlock
              k="CLOUD REACHABLE"
              v={health.data ? (health.data.network.cloud_reachable ? 'YES' : 'NO') : '—'}
            />
            <MiniBlock
              k="LAST SYNC"
              v={
                health.data?.network.last_sync_at
                  ? new Date(health.data.network.last_sync_at).toLocaleTimeString('en-GB', { hour12: false })
                  : '—'
              }
            />
          </div>
        </Card>
        <Card>
          <SectionHeader
            right={
              <Btn kind="ghost" size="sm">
                Edit
              </Btn>
            }
          >
            INTERFACE · PRIMARY
          </SectionHeader>
          {/* STUB: link / IPv4 / gateway / DNS / MAC / MTU / NTP /
              VLAN are not surfaced by the recorder API. Would need
              an OS-level probe extension. */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 10, marginTop: 6 }}>
            <MiniBlock k="LINK" v="—" />
            <MiniBlock k="IPV4" v={state.ip} />
            <MiniBlock k="GATEWAY" v={state.gateway} />
            <MiniBlock k="DNS" v="—" />
            <MiniBlock k="MAC" v="—" />
            <MiniBlock k="MTU" v="—" />
            <MiniBlock k="NTP" v="—" />
            <MiniBlock k="VLAN" v="—" />
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
                options={['INGRESS', 'EGRESS', 'TOTAL'] as const}
                value="TOTAL"
                onChange={() => {}}
              />
            }
          >
            BANDWIDTH USAGE
          </SectionHeader>
          {/* /v1/health.bandwidth — current sample only. PEAK and
              AVG over a rolling window would need a recorder-side
              ring buffer or Prometheus integration; not exposed
              yet. STUB on those two cells. */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12, marginTop: 8 }}>
            <Stat
              label="INGRESS · NOW"
              value={
                health.data ? ((health.data.bandwidth.rx_bps * 8) / 1e6).toFixed(2) : '—'
              }
              unit="Mb/s"
              tone="accent"
            />
            <Stat
              label="EGRESS · NOW"
              value={
                health.data ? ((health.data.bandwidth.tx_bps * 8) / 1e6).toFixed(2) : '—'
              }
              unit="Mb/s"
              tone="accent"
            />
            <Stat
              label="TOTAL · NOW"
              value={
                health.data
                  ? (((health.data.bandwidth.rx_bps + health.data.bandwidth.tx_bps) * 8) / 1e6).toFixed(2)
                  : '—'
              }
              unit="Mb/s"
              tone="accent"
              sub="PEAK / AVG NOT EXPOSED"
            />
          </div>
        </Card>
      </div>
    </div>
  );
}
