// Overview (hero) route. Faithful port of the design's overview.jsx:
// pairing card, system-vitals knobs, four stat cards, cameras grid,
// recent events feed, health-checks list, ingress timeline. Live
// data is simulated by the mockdata helpers; replaced with real
// /v1/health + /v1/events polling in the data-layer follow-up.

import { useMemo } from 'react';
import {
  Btn,
  Brackets,
  Knob,
  Progress,
  SectionHeader,
  Segmented,
  StatusBadge,
} from '../components/primitives';
import { Icon } from '../components/Icon';
import type { IconName } from '../components/Icon';
import { PageHeader } from '../components/PageHeader';
import { fetchHealth, fetchEvents } from '../lib/api';
import type { Event as ApiEvent } from '../lib/api';
import { usePoll, formatUptime, formatTime } from '../lib/hooks';
import type { AppState, Route, ToastInput, UICamera, BadgeKind } from '../lib/types';

interface OverviewProps {
  state: AppState;
  setState: React.Dispatch<React.SetStateAction<AppState>>;
  go: (r: Route) => void;
  addToast: (t: ToastInput) => void;
  setShowWizard: (open: boolean) => void;
}

export function Overview({ state, go, addToast, setShowWizard, setState }: OverviewProps) {
  // Live recorder health — polls /v1/health every second to drive
  // CPU / mem / cameras_online / network knobs and the storage tile.
  const health = usePoll(fetchHealth, 1000, []);
  // Recent events: latest 6 entries from /v1/events. Polls every 4s
  // (slower than the heartbeat — the ring buffer doesn't change as
  // fast as the vitals).
  const recent = usePoll(() => fetchEvents({ perPage: 6 }), 4000, []);

  const cpuPct = health.data ? Math.round(health.data.cpu_pct) : 0;
  const memPct = health.data ? Math.round(health.data.mem_pct) : 0;
  // Bandwidth: total RX + TX bytes/sec from /v1/health.bandwidth,
  // converted to Mbit/s for display. First /v1/health call returns
  // (0, 0) while the sampler primes; subsequent calls deliver real
  // rates. The knob's max=50 was chosen for typical 6-camera
  // residential ingress; clamps internally.
  const totalBps = health.data
    ? health.data.bandwidth.rx_bps + health.data.bandwidth.tx_bps
    : 0;
  const bw = (totalBps * 8) / 1_000_000;
  // Storage: from /v1/health.storage[]. Used = sum(used_pct *
  // assumed-2TB) is meaningless without absolute capacity, so we
  // pull from /v1/storage-volumes-derived stats once that route
  // wires up. For now use the first volume's used_pct against the
  // legend's 2 TB total (mock).
  const recordingCount = health.data?.cameras_recording ?? 0;
  const cameraTotal = health.data?.cameras_total ?? state.cameras.length;
  const storage = { used: 256.4, total: 2048, retained: 14 }; // STUB
  const storagePct = (storage.used / storage.total) * 100;

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
        breadcrumb="RAIKADA / RECORDING SERVER / OVERVIEW"
        title="Overview"
        sub={
          health.status === 'error'
            ? `Recorder unreachable — ${health.error.message}`
            : health.status === 'loading'
              ? 'Loading status…'
              : state.paired
                ? `Reported at ${formatTime(health.data.reported_at)}`
                : 'Recorder is unpaired'
        }
        right={
          <>
            <Btn
              kind="tactical"
              icon="refresh-cw"
              onClick={() => {
                health.refetch();
                recent.refetch();
                addToast({
                  kind: 'info',
                  title: 'REFRESHED',
                  body: 'Status reloaded from recorder',
                  icon: 'refresh-cw',
                });
              }}
            >
              REFRESH
            </Btn>
            <Btn kind="secondary" icon="play" onClick={() => setShowWizard(true)}>
              Run Setup Again
            </Btn>
          </>
        }
      />

      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* Row 1: pairing hero + system vitals */}
        <div style={{ display: 'grid', gridTemplateColumns: '1.35fr 1fr', gap: 12 }}>
          <div
            style={{
              position: 'relative',
              background: 'var(--bg-secondary)',
              border: `1px solid ${state.paired ? 'var(--border)' : 'rgba(234,179,8,0.27)'}`,
              borderRadius: 6,
              padding: 20,
              overflow: 'hidden',
            }}
          >
            <Brackets />
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'flex-start',
                marginBottom: 14,
              }}
            >
              <div>
                <SectionHeader>MANAGEMENT SERVER LINK</SectionHeader>
                {state.paired && state.managementServer ? (
                  <>
                    <div
                      style={{
                        fontFamily: 'var(--font-sans)',
                        fontSize: 20,
                        fontWeight: 600,
                        color: 'var(--text-primary)',
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
                      {state.managementServer.ip} · v{state.managementServer.ver} · Authenticated via mTLS
                    </div>
                  </>
                ) : (
                  <>
                    <div
                      style={{
                        fontFamily: 'var(--font-sans)',
                        fontSize: 20,
                        fontWeight: 600,
                        color: 'var(--text-primary)',
                      }}
                    >
                      Not paired
                    </div>
                    <div
                      style={{
                        fontFamily: 'var(--font-mono)',
                        fontSize: 11,
                        color: 'var(--text-secondary)',
                        marginTop: 4,
                      }}
                    >
                      This recorder is operating standalone. Recording rules and user access will not sync.
                    </div>
                  </>
                )}
              </div>
              <StatusBadge kind={state.paired ? 'paired' : 'unpaired'} />
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 10 }}>
              {(
                [
                  { k: 'LAST HEARTBEAT', v: state.paired ? '2s ago' : '—' },
                  { k: 'RULES SYNC', v: state.paired ? 'UP TO DATE' : 'PENDING' },
                  { k: 'LATENCY', v: state.paired ? '12 MS' : '—' },
                  { k: 'TUNNEL', v: state.paired ? 'WSS/443' : 'CLOSED' },
                ] as const
              ).map((x) => (
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
                      fontSize: 13,
                      color: state.paired ? '#F97316' : 'var(--text-muted)',
                      marginTop: 4,
                      letterSpacing: 0.5,
                    }}
                  >
                    {x.v}
                  </div>
                </div>
              ))}
            </div>

            <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
              {state.paired ? (
                <>
                  <Btn kind="secondary" icon="activity" onClick={() => go('diagnostics')}>
                    Test Connection
                  </Btn>
                  <Btn
                    kind="danger"
                    icon="unlink"
                    onClick={() => {
                      setState((s) => ({ ...s, paired: false, managementServer: null }));
                      addToast({
                        kind: 'warning',
                        title: 'UNPAIRED',
                        body: 'Recorder is no longer linked',
                        icon: 'unlink',
                      });
                    }}
                  >
                    Unpair
                  </Btn>
                </>
              ) : (
                <Btn kind="primary" icon="link" onClick={() => go('pairing')}>
                  Pair with Management Server
                </Btn>
              )}
            </div>
          </div>

          {/* System vitals: three knobs + uptime/temp */}
          <div
            style={{
              background: 'var(--bg-secondary)',
              border: '1px solid var(--border)',
              borderRadius: 6,
              padding: 20,
            }}
          >
            <SectionHeader
              right={
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 9,
                    letterSpacing: 1,
                    color: 'var(--text-muted)',
                  }}
                >
                  LIVE · 1Hz
                </span>
              }
            >
              SYSTEM VITALS
            </SectionHeader>
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(3, 1fr)',
                gap: 8,
                marginTop: 8,
                alignItems: 'center',
                justifyItems: 'center',
              }}
            >
              <Knob value={cpuPct} label="CPU" unit="%" tone={cpuPct > 70 ? 'warning' : 'accent'} />
              <Knob value={bw} max={50} label="BANDWIDTH" unit="Mb/s" tone="accent" />
              <Knob value={memPct} label="MEM" unit="%" tone={memPct > 80 ? 'warning' : 'accent'} />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 8, marginTop: 16 }}>
              <MiniMetric
                label="UPTIME"
                value={health.data ? formatUptime(health.data.uptime) : '—'}
              />
              {/* TEMP: not exposed by /v1/health. Stub. */}
              <MiniMetric label="TEMP" value="—" />
            </div>
          </div>
        </div>

        {/* Row 2: four stat tiles */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 12 }}>
          <StatCard
            title="CAMERAS"
            value={`${recordingCount}`}
            unit={`/ ${cameraTotal}`}
            tone="accent"
            sub="recording now"
            icon="cctv"
            onClick={() => go('cameras')}
          />
          <StatCard
            title="STORAGE"
            value={storage.used.toFixed(1)}
            unit="GB"
            tone="accent"
            sub={`${storagePct.toFixed(1)}% of 2 TB · retention ${storage.retained}D`}
            icon="hard-drive"
            progress={storagePct / 100}
            onClick={() => go('storage')}
          />
          <StatCard
            title="EVENTS · 24H"
            value="127"
            unit=""
            tone="accent"
            sub="34 motion · 93 detection"
            icon="zap"
            onClick={() => go('logs')}
          />
          <StatCard
            title="BANDWIDTH"
            value={bw.toFixed(1)}
            unit="Mb/s"
            tone="accent"
            sub={
              health.data
                ? `↓ ${(health.data.bandwidth.rx_bps * 8 / 1e6).toFixed(1)} ↑ ${(health.data.bandwidth.tx_bps * 8 / 1e6).toFixed(1)} Mb/s`
                : 'priming…'
            }
            icon="arrow-down-up"
            onClick={() => go('network')}
          />
        </div>

        {/* Row 3: cameras + events */}
        <div style={{ display: 'grid', gridTemplateColumns: '1.3fr 1fr', gap: 12 }}>
          <CamerasPanel cameras={state.cameras} go={go} />
          <EventsPanel events={recent.data?.items ?? []} go={go} />
        </div>

        {/* Row 4: checks + ingress */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.2fr', gap: 12 }}>
          <ChecksPanel state={state} go={go} />
          <IngressTimeline />
        </div>
      </div>
    </div>
  );
}

function MiniMetric({ label, value }: { label: string; value: string }) {
  return (
    <div
      style={{
        padding: 10,
        background: 'var(--bg-tertiary)',
        border: '1px solid var(--border)',
        borderRadius: 4,
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
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
        {label}
      </span>
      <span
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 12,
          color: '#F97316',
          letterSpacing: 0.5,
        }}
      >
        {value}
      </span>
    </div>
  );
}

interface StatCardProps {
  title: string;
  value: string | number;
  unit?: string;
  tone: 'accent' | 'success' | 'danger' | 'warning';
  sub: string;
  icon: IconName;
  progress?: number;
  onClick?: () => void;
}

function StatCard({ title, value, unit, tone, sub, icon, progress, onClick }: StatCardProps) {
  const colors = { accent: '#F97316', success: '#22C55E', danger: '#EF4444', warning: '#EAB308' };
  const c = colors[tone];
  return (
    <button
      onClick={onClick}
      style={{
        textAlign: 'left',
        cursor: onClick ? 'pointer' : 'default',
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 16,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        transition: 'border-color 150ms var(--ease-out)',
      }}
      onMouseEnter={(e) => onClick && (e.currentTarget.style.borderColor = 'rgba(249,115,22,0.27)')}
      onMouseLeave={(e) => onClick && (e.currentTarget.style.borderColor = 'var(--border)')}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            letterSpacing: 1,
            color: 'var(--text-muted)',
            textTransform: 'uppercase',
          }}
        >
          {title}
        </span>
        <Icon name={icon} style={{ width: 14, height: 14, color: 'var(--text-muted)' }} />
      </div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
        <span
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 28,
            color: c,
            fontVariantNumeric: 'tabular-nums',
            lineHeight: 1,
          }}
        >
          {value}
        </span>
        {unit && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)' }}>
            {unit}
          </span>
        )}
      </div>
      {progress !== undefined && <Progress value={progress * 100} tone={tone} />}
      <span
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 10,
          color: 'var(--text-muted)',
          textTransform: 'uppercase',
          letterSpacing: 0.5,
        }}
      >
        {sub}
      </span>
    </button>
  );
}

function CamerasPanel({ cameras, go }: { cameras: UICamera[]; go: (r: Route) => void }) {
  return (
    <div
      style={{
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 16,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <SectionHeader
        right={
          <Btn kind="ghost" size="sm" onClick={() => go('cameras')}>
            MANAGE
          </Btn>
        }
      >
        CAMERAS
      </SectionHeader>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8 }}>
        {cameras.slice(0, 6).map((c) => (
          <CameraMini key={c.id} c={c} />
        ))}
      </div>
    </div>
  );
}

function CameraMini({ c }: { c: UICamera }) {
  return (
    <div
      style={{
        padding: 10,
        background: 'var(--bg-tertiary)',
        border: '1px solid var(--border)',
        borderRadius: 4,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        position: 'relative',
        minHeight: 76,
      }}
    >
      <Brackets />
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 0.5 }}>
          {c.id}
        </span>
        <StatusBadge kind={c.status} size="sm" />
      </div>
      <span
        style={{
          fontFamily: 'var(--font-sans)',
          fontSize: 12,
          fontWeight: 500,
          color: 'var(--text-primary)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {c.name}
      </span>
      <span
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 9,
          color: 'var(--text-muted)',
          letterSpacing: 0.5,
        }}
      >
        {c.resolution} · {c.fps} FPS
      </span>
    </div>
  );
}

function EventsPanel({ events, go }: { events: ApiEvent[]; go: (r: Route) => void }) {
  // Map canonical EventSeverity (info/warning/error) onto the badge
  // kinds exposed by StatusBadge. Recorder also has a "debug"
  // logging convention but only info/warning/error are surfaced as
  // canonical Event severities per ADR 0009.
  function severityBadge(s: ApiEvent['severity']): BadgeKind {
    if (s === 'error') return 'error';
    if (s === 'warning') return 'warn';
    return 'info';
  }

  return (
    <div
      style={{
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 16,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <SectionHeader
        right={
          <Btn kind="ghost" size="sm" onClick={() => go('logs')}>
            OPEN LOGS
          </Btn>
        }
      >
        RECENT EVENTS
      </SectionHeader>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {events.length === 0 && (
          <div
            style={{
              padding: '20px 8px',
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              color: 'var(--text-muted)',
              letterSpacing: 1,
              textAlign: 'center',
            }}
          >
            NO EVENTS YET
          </div>
        )}
        {events.map((e) => (
          <div
            key={e.id}
            style={{
              display: 'grid',
              gridTemplateColumns: '62px 52px 1fr',
              gap: 10,
              padding: '6px 8px',
              alignItems: 'center',
              borderRadius: 3,
            }}
          >
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
              {formatTime(e.occurred_at)}
            </span>
            <StatusBadge kind={severityBadge(e.severity)} size="sm" />
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                color: 'var(--text-primary)',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {e.message}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function ChecksPanel({ state, go }: { state: AppState; go: (r: Route) => void }) {
  type CheckStatus = true | false | 'warn';
  const checks: { k: string; ok: CheckStatus; v: string }[] = [
    { k: 'Management server', ok: state.paired, v: state.paired ? 'Paired, heartbeat 2s' : 'Unpaired' },
    { k: 'Storage array', ok: true, v: 'RAID1, 2 disks healthy' },
    { k: 'Network uplink', ok: true, v: '1 Gbps, no packet loss' },
    { k: 'NTP time sync', ok: true, v: 'Drift 3 ms' },
    {
      k: 'Cameras online',
      ok: true,
      v: `${state.cameras.filter((c) => c.status === 'online').length} / ${state.cameras.length}`,
    },
    { k: 'Firmware', ok: 'warn', v: 'Update available: 3.1.4' },
  ];

  function badgeFor(ok: CheckStatus): { kind: BadgeKind; label: string } {
    if (ok === true) return { kind: 'online', label: 'OK' };
    if (ok === 'warn') return { kind: 'degraded', label: 'WARN' };
    return { kind: 'error', label: 'FAIL' };
  }

  return (
    <div
      style={{
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 16,
      }}
    >
      <SectionHeader
        right={
          <Btn kind="ghost" size="sm" onClick={() => go('diagnostics')}>
            RUN TESTS
          </Btn>
        }
      >
        HEALTH CHECKS
      </SectionHeader>
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        {checks.map((c, i) => {
          const b = badgeFor(c.ok);
          return (
            <div
              key={c.k}
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr auto',
                gap: 10,
                padding: '8px 0',
                borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                alignItems: 'center',
              }}
            >
              <div>
                <div style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-primary)' }}>{c.k}</div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    marginTop: 2,
                  }}
                >
                  {c.v}
                </div>
              </div>
              <StatusBadge kind={b.kind} label={b.label} size="sm" />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function IngressTimeline() {
  // 80 bars of random ingress bandwidth over the last 4 hours.
  const bars = useMemo(() => Array.from({ length: 80 }, () => 0.3 + Math.random() * 0.7), []);
  return (
    <div
      style={{
        background: 'var(--bg-secondary)',
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 16,
        display: 'flex',
        flexDirection: 'column',
        gap: 12,
      }}
    >
      <SectionHeader
        right={
          <Segmented
            size="sm"
            options={['1H', '4H', '24H', '7D'] as const}
            value="4H"
            onChange={() => {}}
          />
        }
      >
        INGRESS BANDWIDTH
      </SectionHeader>
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          gap: 2,
          height: 80,
          padding: '0 4px',
        }}
      >
        {bars.map((h, i) => (
          <div
            key={i}
            style={{
              flex: 1,
              height: `${h * 100}%`,
              background: 'linear-gradient(180deg, #F97316, rgba(249,115,22,0.3))',
              opacity: 0.75,
            }}
          />
        ))}
      </div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          fontFamily: 'var(--font-mono)',
          fontSize: 9,
          color: 'var(--text-muted)',
          letterSpacing: 1,
        }}
      >
        <span>-4H</span>
        <span>-3H</span>
        <span>-2H</span>
        <span>-1H</span>
        <span>NOW</span>
      </div>
    </div>
  );
}

