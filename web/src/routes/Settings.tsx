// Settings route — identity / firmware update / system actions.
// Faithful port of the design's SettingsRoute.

import { useState } from 'react';
import { Btn, Card, Input, SectionHeader, Toggle } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import type { AppState, ToastInput } from '../lib/types';

interface SettingsProps {
  state: AppState;
  addToast: (t: ToastInput) => void;
}

export function Settings({ state, addToast }: SettingsProps) {
  const [autoUpdate, setAutoUpdate] = useState(true);
  const [telemetry, setTelemetry] = useState(true);
  const [hostname, setHostname] = useState(state.hostname);
  const [location, setLocation] = useState('Warehouse A — Rack 2');
  const [timezone, setTimezone] = useState('Europe/Amsterdam');

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
        breadcrumb="RECORDING SERVER / SETTINGS"
        title="Settings"
        sub="System-level controls for this recorder"
      />
      <div style={{ padding: 20, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <Card>
          <SectionHeader>IDENTITY</SectionHeader>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 8 }}>
            <Input label="HOSTNAME" value={hostname} onChange={setHostname} mono />
            <Input label="LOCATION" value={location} onChange={setLocation} />
            <Input label="TIMEZONE" value={timezone} onChange={setTimezone} mono />
          </div>
        </Card>
        <Card>
          <SectionHeader>FIRMWARE</SectionHeader>
          <div
            style={{
              padding: 10,
              background: 'var(--bg-tertiary)',
              border: '1px solid rgba(234,179,8,0.27)',
              borderRadius: 4,
              marginTop: 8,
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <div>
              <div
                style={{
                  fontFamily: 'var(--font-mono)',
                  fontSize: 10,
                  letterSpacing: 1,
                  color: '#EAB308',
                  textTransform: 'uppercase',
                }}
              >
                UPDATE AVAILABLE
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 13,
                  color: 'var(--text-primary)',
                  marginTop: 3,
                }}
              >
                3.1.4 — released 6 days ago
              </div>
            </div>
            <Btn
              kind="tactical"
              icon="download"
              onClick={() =>
                addToast({
                  kind: 'info',
                  title: 'UPDATE QUEUED',
                  body: 'Will apply at next maintenance window',
                  icon: 'download',
                })
              }
            >
              Install
            </Btn>
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              marginTop: 14,
              padding: '8px 0',
              borderTop: '1px solid var(--border)',
            }}
          >
            <div>
              <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                Auto-update
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 11,
                  color: 'var(--text-muted)',
                  marginTop: 2,
                }}
              >
                Apply security patches on 02:00 maintenance window
              </div>
            </div>
            <Toggle on={autoUpdate} onChange={setAutoUpdate} />
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              padding: '8px 0',
              borderTop: '1px solid var(--border)',
            }}
          >
            <div>
              <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                Telemetry to Raikada
              </div>
              <div
                style={{
                  fontFamily: 'var(--font-sans)',
                  fontSize: 11,
                  color: 'var(--text-muted)',
                  marginTop: 2,
                }}
              >
                Anonymous crash + performance reports
              </div>
            </div>
            <Toggle on={telemetry} onChange={setTelemetry} />
          </div>
        </Card>
        <Card style={{ gridColumn: '1/3' }}>
          <SectionHeader>SYSTEM ACTIONS</SectionHeader>
          <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
            <Btn
              kind="secondary"
              icon="rotate-ccw"
              onClick={() =>
                addToast({
                  kind: 'warning',
                  title: 'REBOOT QUEUED',
                  body: 'Recorder will restart in 10s',
                  icon: 'rotate-ccw',
                })
              }
            >
              Reboot
            </Btn>
            <Btn kind="secondary" icon="download-cloud">
              Backup Config
            </Btn>
            <Btn kind="secondary" icon="upload-cloud">
              Restore Config
            </Btn>
            <Btn kind="danger" icon="trash-2">
              Factory Reset
            </Btn>
          </div>
        </Card>
      </div>
    </div>
  );
}
