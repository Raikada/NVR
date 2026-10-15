// Settings — system preferences, all wired to real endpoints:
// general (/v1/system/settings + /v1/me/prefs + theme), notifications
// (/v1/notification-targets, incl. quiet hours on subscriptions),
// network (read-only interfaces), updates (/v1/recorder/identity
// pending update + config backup), about (/v1/system/info).
import { useEffect, useState } from 'react';
import { Icon } from '../components/Icon';
import { Btn, FormRow, Modal, Panel, Segmented, Toggle, TxtInput, pageTitle, screenHdr } from '../components/ui';
import {
  ApiError, api, configBackupURL, createNotificationTarget, deleteNotificationTarget,
  fetchNetworkInfo, getStoredToken, getSystemInfo, getSystemSettings,
  patchSystemSettings, testNotificationTarget, updateNotificationTarget,
  listNotificationTargets,
  type NetworkInfo, type SystemSettings,
} from '../lib/api';
import type { NotificationTarget } from '../lib/types';
import { useConsole } from '../lib/console';
import type { Theme } from './theme';

interface PendingUpdate {
  version?: string;
  image_reference?: string;
  [k: string]: unknown;
}

interface RecorderIdentity {
  id: string;
  hostname: string;
  timezone: string;
  firmware_version: string;
  public_key_fingerprint: string;
  pending_software_update?: PendingUpdate;
  pending_software_update_polled_at?: string;
}

export function Settings({ theme, setTheme, onOpenOnboarding }: {
  theme: Theme;
  setTheme: (t: Theme) => void;
  onOpenOnboarding: () => void;
}) {
  const data = useConsole();
  const [section, setSection] = useState('general');
  const [settings, setSettings] = useState<SystemSettings>({});
  const [dirty, setDirty] = useState<SystemSettings>({});
  const [note, setNote] = useState('');

  useEffect(() => {
    void getSystemSettings().then(setSettings).catch(() => {});
  }, []);

  const set = (k: string, v: string) => setDirty((d) => ({ ...d, [k]: v }));
  const val = (k: string) => dirty[k] ?? settings[k] ?? '';
  const save = () => {
    if (Object.keys(dirty).length === 0) { setNote('Nothing to save.'); return; }
    void patchSystemSettings(dirty)
      .then((s) => { setSettings(s); setDirty({}); setNote('Saved.'); })
      .catch((e) => setNote(e instanceof ApiError ? e.message : 'save failed'));
  };

  const clock = typeof data.prefs['clock'] === 'string' ? (data.prefs['clock'] as string) : '24H';

  const nav: Array<[string, string, Parameters<typeof Icon>[0]['name']]> = [
    ['general', 'General', 'settings'],
    ['notifications', 'Notifications', 'bell'],
    ['network', 'Network', 'globe'],
    ['updates', 'Updates', 'download'],
    ['about', 'About', 'info'],
  ];

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)' }}>
      <header style={screenHdr}>
        <span style={pageTitle}>Settings</span>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {note && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{note}</span>}
          <Btn kind="ghost" onClick={onOpenOnboarding}><Icon name="sparkles" size={12} />Run setup again</Btn>
          <Btn kind="primary" onClick={save}>Save changes</Btn>
        </div>
      </header>
      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        <aside style={{ width: 240, borderRight: '1px solid var(--border)', background: 'var(--bg-secondary)', padding: 10, flexShrink: 0 }}>
          {nav.map(([id, l, icon]) => (
            <button key={id} onClick={() => setSection(id)} style={{ width: '100%', display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px', background: section === id ? 'var(--accent-13)' : 'transparent', border: `1px solid ${section === id ? 'var(--accent-27)' : 'transparent'}`, borderRadius: 4, cursor: 'pointer', textAlign: 'left', marginBottom: 2 }}>
              <Icon name={icon} size={14} color={section === id ? 'var(--accent)' : 'var(--text-secondary)'} />
              <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: section === id ? 'var(--accent)' : 'var(--text-primary)' }}>{l}</span>
            </button>
          ))}
        </aside>
        <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
          <div style={{ maxWidth: 720, display: 'flex', flexDirection: 'column', gap: 16 }}>
            {section === 'general' && (
              <>
                <Panel title="SYSTEM">
                  <Rows>
                    <FormRow label="Site name"><TxtInput value={val('site_name')} onChange={(v) => set('site_name', v)} placeholder="e.g. Home" /></FormRow>
                    <FormRow label="Time zone"><TxtInput value={val('timezone')} onChange={(v) => set('timezone', v)} placeholder="e.g. America/Chicago" /></FormRow>
                    <FormRow label="Clock">
                      <Segmented options={['12H', '24H']} value={clock} onChange={(v) => void data.savePrefs({ clock: v })} />
                    </FormRow>
                    <FormRow label="Theme">
                      <Segmented options={[{ value: 'dark', label: 'DARK' }, { value: 'light', label: 'LIGHT' }]} value={theme} onChange={(v) => setTheme(v as Theme)} />
                    </FormRow>
                  </Rows>
                </Panel>
                <Panel title="LIVE VIEW">
                  <Rows>
                    <FormRow label="Default grid">
                      <Segmented
                        options={['1x1', '2x2', '3x3', '4x4'].map((v) => ({ value: v, label: v.replace('x', '×') }))}
                        value={typeof data.prefs['default_grid'] === 'string' ? (data.prefs['default_grid'] as string) : '2x2'}
                        onChange={(v) => void data.savePrefs({ default_grid: v })}
                      />
                    </FormRow>
                  </Rows>
                </Panel>
              </>
            )}
            {section === 'notifications' && <NotificationsSection />}
            {section === 'network' && <NetworkSection />}
            {section === 'updates' && <UpdatesSection />}
            {section === 'about' && <AboutSection />}
          </div>
        </div>
      </div>
    </div>
  );
}

function Rows({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 14 }}>{children}</div>;
}

/* ---------- notifications ---------- */

function NotificationsSection() {
  const [targets, setTargets] = useState<NotificationTarget[]>([]);
  const [adding, setAdding] = useState(false);
  const [note, setNote] = useState('');
  const load = () => { void listNotificationTargets().then(setTargets).catch(() => {}); };
  useEffect(load, []);

  return (
    <>
      <Panel title="CHANNELS" right={<Btn kind="tactical" onClick={() => setAdding(true)}>+ NEW</Btn>}>
        {targets.map((t, i) => (
          <div key={t.id} style={{ padding: '12px 14px', borderBottom: i < targets.length - 1 ? '1px solid var(--border)' : 'none', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)' }}>{t.name}</div>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {t.kind.toUpperCase()} · {t.webhook_url || t.email_address || '—'}
              </div>
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
              <Btn kind="ghost" onClick={() => {
                void testNotificationTarget(t.id)
                  .then((r) => setNote(r.ok ? `Test delivered · ${t.name}` : `Test failed · ${r.error ?? r.status}`))
                  .catch(() => setNote('Test failed'));
              }}>Test</Btn>
              <Toggle on={t.enabled} onChange={(v) => {
                void updateNotificationTarget(t.id, { enabled: v }).then(load).catch(() => {});
              }} />
              <button
                onClick={() => { if (window.confirm(`Delete channel ${t.name}?`)) void deleteNotificationTarget(t.id).then(load); }}
                style={{ width: 24, height: 24, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, cursor: 'pointer', color: 'var(--text-muted)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}
              ><Icon name="trash-2" size={11} /></button>
            </div>
          </div>
        ))}
        {targets.length === 0 && (
          <div style={{ padding: 20, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>
            No channels yet. Add a webhook (Home Assistant, Slack, n8n) or an email target.
          </div>
        )}
      </Panel>
      {note && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{note}</span>}
      {adding && <AddTargetModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); load(); }} />}
    </>
  );
}

function AddTargetModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [kind, setKind] = useState<'webhook' | 'email'>('webhook');
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [email, setEmail] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    setErr('');
    try {
      await createNotificationTarget({
        kind, name: name.trim(), enabled: true,
        ...(kind === 'webhook' ? { webhook_url: url, ...(secret ? { webhook_secret: secret } : null) } : { email_address: email }),
      });
      onAdded();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'create failed');
      setBusy(false);
    }
  };
  return (
    <Modal
      title="New notification channel"
      width={480}
      onClose={onClose}
      footer={
        <>
          <Btn kind="ghost" onClick={onClose}>Cancel</Btn>
          <Btn kind="primary" disabled={busy || !name.trim() || (kind === 'webhook' ? !url : !email)} onClick={() => void submit()}>
            {busy ? 'Creating…' : 'Create'}
          </Btn>
        </>
      }
    >
      <FormRow label="Kind"><Segmented options={[{ value: 'webhook', label: 'WEBHOOK' }, { value: 'email', label: 'EMAIL' }]} value={kind} onChange={(v) => setKind(v as 'webhook' | 'email')} /></FormRow>
      <FormRow label="Name"><TxtInput value={name} onChange={setName} placeholder="e.g. Home Assistant" /></FormRow>
      {kind === 'webhook' ? (
        <>
          <FormRow label="URL"><TxtInput value={url} onChange={setUrl} placeholder="https://…/webhook" /></FormRow>
          <FormRow label="HMAC secret"><TxtInput value={secret} onChange={setSecret} placeholder="optional" /></FormRow>
        </>
      ) : (
        <FormRow label="Email"><TxtInput value={email} onChange={setEmail} placeholder="you@example.com" /></FormRow>
      )}
      {err && <div style={{ padding: 10, background: 'var(--danger-7)', border: '1px solid var(--danger-27)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--danger)' }}>{err}</div>}
    </Modal>
  );
}

/* ---------- network ---------- */

function NetworkSection() {
  const [net, setNet] = useState<NetworkInfo | null>(null);
  useEffect(() => { void fetchNetworkInfo().then(setNet).catch(() => {}); }, []);
  return (
    <>
      <Panel title="LOCAL ACCESS">
        <Rows>
          {net?.interfaces?.filter((i) => i.is_up && !i.is_loopback).map((i) => (
            <FormRow key={i.name} label={i.name}>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)' }}>
                {(i.addresses ?? []).map((a) => a.split('/')[0]).join(' · ') || '—'} · {i.hardware_addr}
              </span>
            </FormRow>
          ))}
          {!net && <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>Loading…</span>}
        </Rows>
      </Panel>
      <Panel title="REMOTE ACCESS">
        <div style={{ padding: 14, display: 'flex', gap: 12, alignItems: 'flex-start' }}>
          <Icon name="shield-check" size={16} color="var(--accent)" style={{ marginTop: 2 }} />
          <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-secondary)', lineHeight: 1.6 }}>
            <span style={{ color: 'var(--text-primary)', fontWeight: 500 }}>This NVR is LAN-only.</span>{' '}
            It never opens ports on your router. To view cameras away from home, connect to your network with a VPN
            (WireGuard, Tailscale) and open the console as usual.
          </div>
        </div>
      </Panel>
    </>
  );
}

/* ---------- updates ---------- */

function UpdatesSection() {
  const [identity, setIdentity] = useState<RecorderIdentity | null>(null);
  const [note, setNote] = useState('');
  useEffect(() => {
    void api.get<RecorderIdentity>('/recorder/identity').then(setIdentity).catch(() => {});
  }, []);
  const pending = identity?.pending_software_update;
  const downloadBackup = () => {
    const token = getStoredToken();
    void fetch(configBackupURL, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
      .then((r) => r.blob())
      .then((blob) => {
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = 'raikada-config-backup.json';
        a.click();
        URL.revokeObjectURL(a.href);
      })
      .catch(() => setNote('Backup download failed'));
  };
  return (
    <>
      <Panel title="SOFTWARE">
        <div style={{ padding: 14, display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--border)' }}>
          <div>
            <div style={{ fontFamily: 'var(--font-sans)', fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>
              {identity?.firmware_version || '—'}
              {pending && (
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--warning)', marginLeft: 8 }}>
                  {String(pending.version ?? 'UPDATE')} AVAILABLE
                </span>
              )}
            </div>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', marginTop: 4 }}>
              {identity?.pending_software_update_polled_at
                ? `CHECKED ${identity.pending_software_update_polled_at.slice(0, 16).replace('T', ' ')}`
                : 'UPDATE POLLING INACTIVE (LAN-ONLY MODE)'}
            </div>
          </div>
        </div>
        <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
            Updates are applied by pulling the new image on the host. Recording pauses for about 30 seconds.
          </span>
          <pre style={{ margin: 0, padding: 10, background: 'var(--bg-primary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', overflowX: 'auto' }}>
            ~/raikada/update.sh
          </pre>
        </div>
      </Panel>
      <Panel title="BACKUP & RESTORE">
        <div style={{ padding: 14, display: 'flex', gap: 8, alignItems: 'center' }}>
          <Btn kind="secondary" onClick={downloadBackup}><Icon name="download" size={12} />Export config</Btn>
          <span style={{ flex: 1, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>{note}</span>
        </div>
      </Panel>
    </>
  );
}

/* ---------- about ---------- */

function AboutSection() {
  const [info, setInfo] = useState<{ recorder_id: string; version: string } | null>(null);
  const [identity, setIdentity] = useState<RecorderIdentity | null>(null);
  useEffect(() => {
    void getSystemInfo().then(setInfo).catch(() => {});
    void api.get<RecorderIdentity>('/recorder/identity').then(setIdentity).catch(() => {});
  }, []);
  return (
    <Panel title="ABOUT">
      <div style={{ padding: 14, display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 16, rowGap: 8 }}>
        {([
          ['RECORDER ID', info?.recorder_id ?? '—'],
          ['VERSION', info?.version ?? '—'],
          ['HOSTNAME', identity?.hostname ?? '—'],
          ['TIMEZONE', identity?.timezone ?? '—'],
          ['KEY FINGERPRINT', identity?.public_key_fingerprint ?? '—'],
          ['DEPLOYMENT', 'Docker · LAN-only'],
          ['MEDIA ENGINE', 'MediaMTX lineage'],
          ['EVENTS', 'ONVIF + vendor channels · camera-side analytics'],
        ] as Array<[string, string]>).map(([k, v]) => (
          <span key={k} style={{ display: 'contents' }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>{k}</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{v}</span>
          </span>
        ))}
      </div>
    </Panel>
  );
}
