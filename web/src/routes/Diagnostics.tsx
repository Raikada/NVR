// Diagnostics route — 5-test suite (ping / iperf3 / RTSP probe / NTP
// drift / SMART) streamed into a terminal console. Faithful port of
// the design's DiagnosticsRoute.

import { useState } from 'react';
import { Btn, Card, SectionHeader, StatusBadge } from '../components/primitives';
import type { StatusBadgeKind } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import type { AppState, ToastInput } from '../lib/types';

interface DiagnosticsProps {
  state: AppState;
  addToast: (t: ToastInput) => void;
}

type TestStatus = 'idle' | 'running' | 'ok' | 'warn' | 'error';

interface TestRow {
  label: string;
  status: TestStatus;
  val: string;
}

interface ConsoleLine {
  k: 'cmd' | 'out' | 'space';
  v: string;
}

interface TestStep {
  k: string;
  cmd: string;
  lines: string[];
  val: string;
  final?: TestStatus;
}

function sleep(ms: number) {
  return new Promise<void>((r) => window.setTimeout(r, ms));
}

export function Diagnostics({ state, addToast }: DiagnosticsProps) {
  const [running, setRunning] = useState(false);
  const [lines, setLines] = useState<ConsoleLine[]>([]);
  const [tests, setTests] = useState<Record<string, TestRow>>({
    ping: { label: 'Ping management server', status: 'idle', val: '' },
    bandwidth: { label: 'Bandwidth to MS (iperf3)', status: 'idle', val: '' },
    rtspProbe: { label: 'RTSP probe all cameras', status: 'idle', val: '' },
    ntpDrift: { label: 'NTP drift check', status: 'idle', val: '' },
    diskHealth: { label: 'Storage S.M.A.R.T.', status: 'idle', val: '' },
  });

  async function run() {
    setRunning(true);
    setLines([]);
    const order: TestStep[] = [
      {
        k: 'ping',
        cmd: 'ping -c 4 ' + (state.managementServer?.host || '10.0.1.21'),
        lines: [
          'PING 10.0.1.21 56(84) bytes of data.',
          '64 bytes from 10.0.1.21: icmp_seq=1 ttl=64 time=12.3 ms',
          '64 bytes from 10.0.1.21: icmp_seq=2 ttl=64 time=11.8 ms',
          '64 bytes from 10.0.1.21: icmp_seq=3 ttl=64 time=12.1 ms',
          '64 bytes from 10.0.1.21: icmp_seq=4 ttl=64 time=12.0 ms',
          'rtt min/avg/max/mdev = 11.8/12.05/12.3/0.18 ms',
        ],
        val: 'avg 12 ms · 0% loss',
      },
      {
        k: 'bandwidth',
        cmd: 'iperf3 -c ms-prod-01.local -t 5',
        lines: [
          'Connecting to host ms-prod-01.local, port 5201',
          '[  5]   0.00-1.00   sec   112 MBytes   938 Mbits/sec',
          '[  5]   1.00-2.00   sec   113 MBytes   947 Mbits/sec',
          '[  5]   2.00-3.00   sec   112 MBytes   941 Mbits/sec',
          '[  5]   3.00-4.00   sec   113 MBytes   948 Mbits/sec',
          '[  5]   4.00-5.00   sec   112 MBytes   940 Mbits/sec',
          '[SUM]   0.00-5.00   sec   562 MBytes   943 Mbits/sec  sender',
        ],
        val: '943 Mb/s',
      },
      {
        k: 'rtspProbe',
        cmd: 'raikada rtsp-probe --all',
        lines: [
          'CAM-01  rtsp://10.0.1.41:554  OK  h264 1920x1080 @ 30fps',
          'CAM-02  rtsp://10.0.1.42:554  OK  h264 1920x1080 @ 30fps',
          'CAM-03  rtsp://10.0.1.43:554  OK  h264 1920x1080 @ 25fps',
          'CAM-04  rtsp://10.0.1.44:554  UNREACHABLE (timeout)',
          'CAM-05  rtsp://10.0.1.45:554  DEGRADED (packet loss 2.4%)',
          'CAM-06  rtsp://10.0.1.46:554  OK  h264 2688x1520 @ 20fps',
        ],
        val: '5/6 OK',
        final: 'warn',
      },
      {
        k: 'ntpDrift',
        cmd: 'chronyc tracking',
        lines: [
          'Reference ID    : A29FC801 (pool.ntp.org)',
          'Stratum         : 2',
          'System time     : 0.000003014 seconds slow',
          'Last offset     : +0.000003127 seconds',
          'Frequency       : 5.287 ppm slow',
        ],
        val: 'drift 3 ms',
      },
      {
        k: 'diskHealth',
        cmd: 'smartctl -H /dev/sda /dev/sdb',
        lines: [
          '/dev/sda: PASSED  (Seagate Ironwolf 2TB · 4,218 hours)',
          '/dev/sdb: PASSED  (Seagate Ironwolf 2TB · 4,218 hours)',
        ],
        val: 'both PASSED',
      },
    ];
    for (const step of order) {
      setTests((t) => ({ ...t, [step.k]: { ...t[step.k], status: 'running' } }));
      setLines((l) => [...l, { k: 'cmd', v: '$ ' + step.cmd }]);
      await sleep(400);
      for (const ln of step.lines) {
        setLines((l) => [...l, { k: 'out', v: ln }]);
        await sleep(180);
      }
      setTests((t) => ({
        ...t,
        [step.k]: { ...t[step.k], status: step.final || 'ok', val: step.val },
      }));
      setLines((l) => [...l, { k: 'space', v: '' }]);
    }
    setRunning(false);
    addToast({
      kind: 'success',
      title: 'DIAGNOSTICS COMPLETE',
      body: 'All tests finished',
      icon: 'check-circle',
    });
  }

  function statusBadge(s: TestStatus) {
    const map: Record<TestStatus, { kind: StatusBadgeKind; label: string }> = {
      idle: { kind: 'idle', label: 'IDLE' },
      running: { kind: 'info', label: 'RUNNING' },
      ok: { kind: 'online', label: 'PASS' },
      warn: { kind: 'degraded', label: 'WARN' },
      error: { kind: 'error', label: 'ERROR' },
    };
    return map[s];
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
        breadcrumb="RECORDING SERVER / DIAGNOSTICS"
        title="Diagnostics"
        sub="Run end-to-end checks of the recorder's network, storage, and camera links"
        right={
          <Btn kind="primary" icon={running ? 'pause' : 'play'} disabled={running} onClick={run}>
            {running ? 'Running…' : 'Run All Tests'}
          </Btn>
        }
      />
      <div
        style={{
          padding: 20,
          display: 'grid',
          gridTemplateColumns: '1fr 1.3fr',
          gap: 16,
          minHeight: 500,
        }}
      >
        <Card style={{ padding: 0 }}>
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader style={{ margin: 0 }}>TEST SUITE</SectionHeader>
          </div>
          {Object.entries(tests).map(([k, t], i) => {
            const b = statusBadge(t.status);
            return (
              <div
                key={k}
                style={{
                  padding: '12px 16px',
                  display: 'grid',
                  gridTemplateColumns: '1fr auto',
                  gap: 12,
                  alignItems: 'center',
                  borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                }}
              >
                <div>
                  <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                    {t.label}
                  </div>
                  {t.val && (
                    <div
                      style={{
                        fontFamily: 'var(--font-mono)',
                        fontSize: 10,
                        color: 'var(--text-muted)',
                        marginTop: 3,
                        letterSpacing: 0.5,
                      }}
                    >
                      {t.val}
                    </div>
                  )}
                </div>
                <StatusBadge kind={b.kind} label={b.label} size="sm" />
              </div>
            );
          })}
        </Card>

        <Card style={{ padding: 0, background: 'var(--bg-primary)', display: 'flex', flexDirection: 'column' }}>
          <div
            style={{
              padding: '14px 16px',
              borderBottom: '1px solid var(--border)',
              background: 'var(--bg-secondary)',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <SectionHeader style={{ margin: 0 }}>CONSOLE</SectionHeader>
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 9,
                color: 'var(--text-muted)',
                letterSpacing: 1,
              }}
            >
              raikada@
              {state.paired
                ? state.managementServer?.host?.split('.')[0] || 'rec01'
                : 'rec01'}{' '}
              ~
            </span>
          </div>
          <div
            style={{
              flex: 1,
              overflow: 'auto',
              padding: 12,
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              lineHeight: 1.55,
            }}
          >
            {lines.length === 0 && (
              <span style={{ color: 'var(--text-muted)' }}># Idle — click Run All Tests to begin</span>
            )}
            {lines.map((l, i) => (
              <div
                key={i}
                style={{
                  color:
                    l.k === 'cmd'
                      ? '#F97316'
                      : l.k === 'space'
                        ? 'transparent'
                        : 'var(--text-primary)',
                  whiteSpace: 'pre',
                }}
              >
                {l.v || '\u00a0'}
              </div>
            ))}
            {running && (
              <span style={{ color: '#F97316', animation: 'blink 1s steps(2) infinite' }}>▊</span>
            )}
          </div>
        </Card>
      </div>
    </div>
  );
}
