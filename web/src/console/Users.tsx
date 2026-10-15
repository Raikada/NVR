// Users & Permissions — users table (CRUD, five roles), role reference
// cards, audit log. All real: /v1/users, /v1/audit.
import { useEffect, useMemo, useState } from 'react';
import { Icon } from '../components/Icon';
import { Btn, FormRow, Modal, Panel, RoleBadge, Segmented, TxtInput, bareBtn, pageTitle, screenHdr } from '../components/ui';
import {
  ApiError, createUser, deleteUser, listAudit, listUsers, resetUserPassword,
  setUserRole, updateUser,
} from '../lib/api';
import type { AuditLogEntry } from '../lib/api';
import type { Role, User } from '../lib/types';
import { fmtAgo, fmtTime } from '../lib/format';

const ROLES: Array<{ name: Role; desc: string; perms: string[] }> = [
  { name: 'owner', desc: 'Full control of this recorder', perms: ['ALL'] },
  { name: 'admin', desc: 'Configure cameras, policies, users', perms: ['CAMERAS:RW', 'POLICIES:RW', 'USERS:RW', 'SYSTEM:RW'] },
  { name: 'operator', desc: 'Live, playback, export, PTZ, acknowledge', perms: ['CAMERAS:R', 'RECORDING:R', 'CLIPS:RW', 'PTZ:RW', 'EVENTS:ACK'] },
  { name: 'viewer', desc: 'Live view and playback only', perms: ['CAMERAS:R', 'RECORDING:R'] },
  { name: 'audit', desc: 'Read audit log + recordings', perms: ['AUDIT:R', 'RECORDING:R'] },
];

export function Users() {
  const [tab, setTab] = useState('users');
  const [users, setUsers] = useState<User[]>([]);
  const [audit, setAudit] = useState<AuditLogEntry[]>([]);
  const [adding, setAdding] = useState(false);
  const [menuFor, setMenuFor] = useState<User | null>(null);
  const [note, setNote] = useState('');

  const load = () => {
    void listUsers().then(setUsers).catch(() => {});
  };
  useEffect(load, []);
  useEffect(() => {
    if (tab !== 'audit') return;
    void listAudit({ perPage: 100 }).then(setAudit).catch(() => {});
  }, [tab]);

  const roleCounts = useMemo(() => {
    const m: Record<string, number> = {};
    for (const u of users) m[u.role] = (m[u.role] ?? 0) + 1;
    return m;
  }, [users]);

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: 'var(--bg-primary)', overflowY: 'auto' }}>
      <header style={screenHdr}>
        <span style={pageTitle}>Users & Permissions</span>
        <Btn kind="primary" onClick={() => setAdding(true)}><Icon name="user-plus" size={12} />Add user</Btn>
      </header>

      <div style={{ padding: '14px 20px', borderBottom: '1px solid var(--border)' }}>
        <Segmented
          options={[{ value: 'users', label: 'USERS' }, { value: 'roles', label: 'ROLES' }, { value: 'audit', label: 'AUDIT LOG' }]}
          value={tab}
          onChange={setTab}
        />
      </div>

      <div style={{ flex: 1, padding: 20 }}>
        {note && (
          <div style={{ marginBottom: 12, padding: 10, background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>{note}</div>
        )}
        {tab === 'users' && (
          <Panel title={`USERS · ${users.length}`}>
            <div style={{ display: 'grid', gridTemplateColumns: '40px 1.5fr 1.5fr 110px 130px 60px', padding: '8px 14px', borderBottom: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>
              <span></span><span>NAME</span><span>USERNAME</span><span>ROLE</span><span>LAST LOGIN</span><span></span>
            </div>
            {users.map((u) => (
              <div key={u.id} style={{ display: 'grid', gridTemplateColumns: '40px 1.5fr 1.5fr 110px 130px 60px', padding: '12px 14px', borderBottom: '1px solid var(--border)', alignItems: 'center', opacity: u.is_active ? 1 : 0.5 }}>
                <span style={{ width: 28, height: 28, borderRadius: '50%', background: 'linear-gradient(135deg,var(--accent),color-mix(in srgb, var(--accent) 55%, #000))', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 600, color: 'var(--on-accent)' }}>
                  {(u.display_name || u.username).slice(0, 2).toUpperCase()}
                </span>
                <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)' }}>
                  {u.display_name || u.username}{!u.is_active && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', marginLeft: 8 }}>DISABLED</span>}
                </span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>{u.username}</span>
                <RoleBadge role={u.role} />
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
                  {u.last_login_at ? fmtAgo((Date.now() - new Date(u.last_login_at).getTime()) / 1000) : 'Never'}
                </span>
                <button onClick={() => setMenuFor(u)} style={{ width: 24, height: 24, justifySelf: 'end', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', borderRadius: 3, cursor: 'pointer', color: 'var(--text-muted)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
                  <Icon name="more-horizontal" size={11} />
                </button>
              </div>
            ))}
          </Panel>
        )}

        {tab === 'roles' && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 12 }}>
            {ROLES.map((r) => (
              <div key={r.name} style={{ background: 'var(--bg-secondary)', border: '1px solid var(--border)', borderRadius: 6, padding: 14 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                  <RoleBadge role={r.name} />
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                    {roleCounts[r.name] ?? 0} USER{(roleCounts[r.name] ?? 0) !== 1 ? 'S' : ''}
                  </span>
                </div>
                <div style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', marginBottom: 10, lineHeight: 1.5 }}>{r.desc}</div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                  {r.perms.map((p) => (
                    <span key={p} style={{ padding: '2px 6px', background: 'var(--bg-tertiary)', border: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 9, letterSpacing: '.5px', color: 'var(--text-secondary)' }}>{p}</span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}

        {tab === 'audit' && (
          <Panel title="AUDIT LOG">
            {audit.map((a) => (
              <div key={a.id} style={{ display: 'grid', gridTemplateColumns: '90px 1.2fr 1.4fr 2fr 80px', padding: '10px 14px', borderBottom: '1px solid var(--border)', fontFamily: 'var(--font-mono)', fontSize: 11, alignItems: 'center' }}>
                <span style={{ color: 'var(--text-muted)' }}>{fmtTime(new Date(a.occurred_at)).slice(0, 5)}</span>
                <span style={{ color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{a.actor_kind === 'system' ? 'system' : a.actor_id.slice(0, 12)}</span>
                <span style={{ color: 'var(--accent)' }}>{a.action}</span>
                <span style={{ color: 'var(--text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{a.resource_kind}{a.resource_id ? ` · ${a.resource_id.slice(0, 20)}` : ''}</span>
                <span style={{ color: a.outcome === 'success' ? 'var(--success)' : 'var(--danger)', textTransform: 'uppercase', fontSize: 9, letterSpacing: 1, textAlign: 'right' }}>{a.outcome}</span>
              </div>
            ))}
            {audit.length === 0 && (
              <div style={{ padding: 20, fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-muted)' }}>No audit entries.</div>
            )}
          </Panel>
        )}
      </div>

      {adding && (
        <AddUserModal
          onClose={() => setAdding(false)}
          onAdded={() => { setAdding(false); load(); }}
        />
      )}
      {menuFor && (
        <UserActions
          user={menuFor}
          onClose={() => setMenuFor(null)}
          onChanged={(msg) => { setNote(msg); setMenuFor(null); load(); }}
        />
      )}
    </div>
  );
}

function AddUserModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [username, setUsername] = useState('');
  const [display, setDisplay] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('viewer');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const submit = async () => {
    setBusy(true);
    setErr('');
    try {
      await createUser({ username: username.trim(), password, role, display_name: display.trim() || undefined });
      onAdded();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'create failed');
      setBusy(false);
    }
  };
  return (
    <Modal
      title="Add user"
      width={480}
      onClose={onClose}
      footer={
        <>
          <Btn kind="ghost" onClick={onClose}>Cancel</Btn>
          <Btn kind="primary" disabled={busy || !username.trim() || password.length < 12} onClick={() => void submit()}>
            {busy ? 'Creating…' : 'Create user'}
          </Btn>
        </>
      }
    >
      <FormRow label="Username"><TxtInput value={username} onChange={setUsername} placeholder="e.g. tanaka" /></FormRow>
      <FormRow label="Display name"><TxtInput value={display} onChange={setDisplay} placeholder="L. Tanaka" /></FormRow>
      <FormRow label="Password"><TxtInput type="password" value={password} onChange={setPassword} placeholder="Min. 12 characters" /></FormRow>
      <FormRow label="Role">
        <Segmented
          options={['admin', 'operator', 'viewer', 'audit'].map((r) => ({ value: r, label: r.toUpperCase() }))}
          value={role}
          onChange={(v) => setRole(v as Role)}
        />
      </FormRow>
      <span style={{ fontFamily: 'var(--font-sans)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
        Share the password out-of-band. The user is prompted to change it on first sign-in.
      </span>
      {err && (
        <div style={{ padding: 10, background: 'var(--danger-7)', border: '1px solid var(--danger-27)', borderRadius: 4, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--danger)' }}>{err}</div>
      )}
    </Modal>
  );
}

function UserActions({ user, onClose, onChanged }: {
  user: User;
  onClose: () => void;
  onChanged: (msg: string) => void;
}) {
  const [role, setRole] = useState<Role>(user.role);
  const [busy, setBusy] = useState('');
  const run = (label: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(label);
    void fn().then(() => onChanged(done)).catch((e) => onChanged(e instanceof ApiError ? e.message : 'failed')).finally(() => setBusy(''));
  };
  return (
    <Modal
      title={`Manage · ${user.display_name || user.username}`}
      width={440}
      onClose={onClose}
      footer={<><Btn kind="ghost" onClick={onClose}>Close</Btn><span /></>}
    >
      <FormRow label="Role">
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <Segmented
            options={['admin', 'operator', 'viewer', 'audit'].map((r) => ({ value: r, label: r.toUpperCase() }))}
            value={role}
            onChange={(v) => setRole(v as Role)}
          />
          {role !== user.role && (
            <Btn kind="secondary" disabled={busy !== ''} onClick={() => run('role', () => setUserRole(user.id, role), `Role → ${role}`)}>Apply</Btn>
          )}
        </div>
      </FormRow>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <Btn
          kind="secondary"
          disabled={busy !== ''}
          onClick={() => {
            const pw = window.prompt('New password (min. 12 chars; user must change it on next sign-in)');
            if (!pw) return;
            run('pw', () => resetUserPassword(user.id, pw), 'Password reset');
          }}
        ><Icon name="key" size={12} />Reset password</Btn>
        <Btn
          kind="secondary"
          disabled={busy !== ''}
          onClick={() => run('toggle', () => updateUser(user.id, { is_active: !user.is_active }), user.is_active ? 'User disabled' : 'User enabled')}
        >{user.is_active ? 'Disable' : 'Enable'}</Btn>
        <Btn
          kind="danger"
          disabled={busy !== ''}
          onClick={() => {
            if (!window.confirm(`Delete ${user.username}? This cannot be undone.`)) return;
            run('del', () => deleteUser(user.id), 'User deleted');
          }}
        ><Icon name="trash-2" size={12} />Delete</Btn>
      </div>
      <span style={{ ...bareBtn as object, fontFamily: 'var(--font-sans)', fontSize: 11, color: 'var(--text-muted)' }}>
        Created {fmtTime(new Date(user.created_at))} · {user.must_change_password ? 'must change password on next sign-in' : 'password set'}
      </span>
    </Modal>
  );
}
