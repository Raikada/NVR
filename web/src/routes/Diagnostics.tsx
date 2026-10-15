// Diagnostics route — runs the recorder's /v1/diagnostics/* test
// suite and streams results into a console panel.
//
// Tests are stdlib-based recorder-side handlers (TCP-handshake
// ping, SNTP query, RTSP probe across configured cameras) — they
// work the same on Linux, macOS, and Windows. Each test posts to
// its endpoint and renders the results inline.

import { useState } from 'react';
import { Btn, Card, SectionHeader, StatusBadge } from '../components/primitives';
import type { StatusBadgeKind } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import { diagPing, diagNTP, diagRTSPProbe } from '../lib/api';
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
  k: 'cmd' | 'out' | 'err' | 'space';
  v: string;
}

export function Diagnostics({ state, addToast }: DiagnosticsProps) {
  const [running, setRunning] = useState(false);
  const [lines, setLines] = useState<ConsoleLine[]>([]);
  const [tests, setTests] = useState<Record<string, TestRow>>({
    ping: { label: 'TCP ping (cloud / MS endpoint)', status: 'idle', val: '' },
    ntp: { label: 'NTP drift (pool.ntp.org)', status: 'idle', val: '' },
    rtspProbe: { label: 'RTSP probe (configured cameras)', status: 'idle', val: '' },
  });

  function pushLine(l: ConsoleLine) {
    setLines((prev) => [...prev, l]);
  }
  function setStatus(k: string, status: TestStatus, val: string) {
    setTests((prev) => ({ ...prev, [k]: { ...prev[k], status, val } }));
  }

  async function run() {
    setRunning(true);
    setLines([]);
    Object.keys(tests).forEach((k) => setStatus(k, 'idle', ''));

    // 1. TCP ping target — public DNS host for reachability signal.
    const pingTarget = '1.1.1.1';
    setStatus('ping', 'running', '');
    pushLine({ k: 'cmd', v: `$ tcp-ping ${pingTarget}` });
    try {
      const res = await diagPing(pingTarget, 4);
      for (const s of res.samples) {
        if (s.ok) {
          pushLine({ k: 'out', v: `  seq=${s.seq}  ${s.latency_ms} ms` });
        } else {
          pushLine({ k: 'err', v: `  seq=${s.seq}  FAIL — ${s.reason}` });
        }
      }
      pushLine({
        k: 'out',
        v: `  avg ${res.avg_ms.toFixed(1)} ms · loss ${res.loss_pct.toFixed(0)}%`,
      });
      setStatus(
        'ping',
        res.loss_pct === 0 ? 'ok' : res.loss_pct < 50 ? 'warn' : 'error',
        `avg ${res.avg_ms.toFixed(1)} ms · ${res.loss_pct.toFixed(0)}% loss`,
      );
    } catch (e) {
      pushLine({ k: 'err', v: `  error: ${(e as Error).message}` });
      setStatus('ping', 'error', (e as Error).message);
    }
    pushLine({ k: 'space', v: '' });

    // 2. NTP drift (SNTP query against pool.ntp.org).
    setStatus('ntp', 'running', '');
    pushLine({ k: 'cmd', v: `$ sntp-query pool.ntp.org` });
    try {
      const res = await diagNTP();
      if (res.ok) {
        pushLine({ k: 'out', v: `  offset ${res.offset_ms.toFixed(1)} ms` });
        setStatus(
          'ntp',
          Math.abs(res.offset_ms) < 100 ? 'ok' : 'warn',
          `offset ${res.offset_ms.toFixed(1)} ms`,
        );
      } else {
        pushLine({ k: 'err', v: `  FAIL — ${res.reason}` });
        setStatus('ntp', 'error', res.reason || 'failed');
      }
    } catch (e) {
      pushLine({ k: 'err', v: `  error: ${(e as Error).message}` });
      setStatus('ntp', 'error', (e as Error).message);
    }
    pushLine({ k: 'space', v: '' });

    // 3. RTSP probe across every configured camera.
    setStatus('rtspProbe', 'running', '');
    pushLine({ k: 'cmd', v: `$ rtsp-probe --all` });
    try {
      const res = await diagRTSPProbe();
      for (const r of res.results) {
        if (r.reachable) {
          pushLine({ k: 'out', v: `  ${r.path}  OK  ${r.latency_ms} ms — ${r.url}` });
        } else {
          pushLine({ k: 'err', v: `  ${r.path}  FAIL — ${r.reason ?? 'unreachable'} — ${r.url}` });
        }
      }
      const status: TestStatus =
        res.total === 0
          ? 'idle'
          : res.ok_count === res.total
            ? 'ok'
            : res.ok_count > 0
              ? 'warn'
              : 'error';
      setStatus(
        'rtspProbe',
        status,
        res.total === 0 ? 'no cameras configured' : `${res.ok_count}/${res.total} OK`,
      );
    } catch (e) {
      pushLine({ k: 'err', v: `  error: ${(e as Error).message}` });
      setStatus('rtspProbe', 'error', (e as Error).message);
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
        sub="Run end-to-end checks of the recorder's network and camera links"
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
              raikada@{state.hostname} ~
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
                      : l.k === 'err'
                        ? '#EF4444'
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
