// Users management page (admin-only). Lists users, supports add /
// edit / role change / password reset / soft-delete (is_active=false).

import { useEffect, useState } from 'react';
import { Btn, Card, Input, SectionHeader, StatusBadge } from '../components/primitives';
import { PageHeader } from '../components/PageHeader';
import {
  createUser,
  deleteUser,
  listUsers,
  resetUserPassword,
  setUserRole,
  updateUser,
} from '../lib/api';
import type { Role, ToastInput, User } from '../lib/types';

interface UsersProps {
  addToast: (t: ToastInput) => void;
}

export function Users({ addToast }: UsersProps) {
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<User | null>(null);
  const [resettingPw, setResettingPw] = useState<User | null>(null);

  async function refresh() {
    setLoading(true);
    setError(null);
    try {
      const items = await listUsers();
      setUsers(items);
    } catch (e) {
      setError((e as Error).message);
    }
    setLoading(false);
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function changeRole(u: User, role: Role) {
    try {
      await setUserRole(u.id, role);
      addToast({ kind: 'success', title: 'ROLE UPDATED', body: `${u.username} → ${role}`, icon: 'check-circle' });
      void refresh();
    } catch (e) {
      addToast({ kind: 'danger', title: 'ROLE CHANGE FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  async function softDelete(u: User) {
    if (!confirm(`Deactivate user "${u.username}"?`)) return;
    try {
      // Soft delete: prefer is_active=false via PATCH.
      await updateUser(u.id, { is_active: false });
      addToast({ kind: 'warning', title: 'USER DEACTIVATED', body: u.username, icon: 'user-x' });
      void refresh();
    } catch (e) {
      // Fall back to hard delete if PATCH not supported.
      try {
        await deleteUser(u.id);
        addToast({ kind: 'warning', title: 'USER DELETED', body: u.username, icon: 'trash-2' });
        void refresh();
      } catch (e2) {
        addToast({ kind: 'danger', title: 'DELETE FAILED', body: (e2 as Error).message, icon: 'x' });
      }
    }
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
        breadcrumb="RECORDING SERVER / USERS"
        title="Users"
        sub={error ?? (loading ? 'Loading users…' : `${users.length} users`)}
        right={
          <Btn kind="primary" icon="plus" onClick={() => setShowAdd(true)}>
            Add User
          </Btn>
        }
      />
      <div style={{ padding: 20 }}>
        <Card style={{ padding: 0 }}>
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader style={{ margin: 0 }}>USERS · {users.length}</SectionHeader>
          </div>
          <div
            style={{
              padding: '10px 16px',
              borderBottom: '1px solid var(--border)',
              display: 'grid',
              gridTemplateColumns: '1.5fr 1.5fr 0.8fr 1fr 1fr 220px',
              gap: 12,
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              color: 'var(--text-muted)',
              textTransform: 'uppercase',
            }}
          >
            <span>USERNAME</span>
            <span>EMAIL</span>
            <span>ROLE</span>
            <span>STATUS</span>
            <span>LAST LOGIN</span>
            <span style={{ textAlign: 'right' }}>ACTIONS</span>
          </div>
          {users.map((u) => (
            <div
              key={u.id}
              style={{
                padding: '12px 16px',
                borderBottom: '1px solid var(--border)',
                display: 'grid',
                gridTemplateColumns: '1.5fr 1.5fr 0.8fr 1fr 1fr 220px',
                gap: 12,
                alignItems: 'center',
                fontFamily: 'var(--font-mono)',
                fontSize: 12,
                color: 'var(--text-primary)',
              }}
            >
              <span>{u.username}</span>
              <span style={{ color: 'var(--text-secondary)' }}>{u.email || '—'}</span>
              <select
                value={u.role}
                onChange={(e) => changeRole(u, e.target.value as Role)}
                style={{
                  background: 'var(--bg-tertiary)',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  color: 'var(--text-primary)',
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11,
                  padding: '4px 6px',
                }}
              >
                <option value="admin">admin</option>
                <option value="viewer">viewer</option>
              </select>
              <StatusBadge kind={u.is_active ? 'online' : 'offline'} label={u.is_active ? 'ACTIVE' : 'INACTIVE'} size="sm" />
              <span style={{ color: 'var(--text-secondary)', fontSize: 10 }}>
                {u.last_login_at ? new Date(u.last_login_at).toLocaleString() : '—'}
              </span>
              <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                <Btn kind="ghost" size="sm" onClick={() => setEditing(u)}>
                  Edit
                </Btn>
                <Btn kind="ghost" size="sm" onClick={() => setResettingPw(u)}>
                  Reset PW
                </Btn>
                <Btn kind="danger" size="sm" onClick={() => softDelete(u)}>
                  Delete
                </Btn>
              </div>
            </div>
          ))}
        </Card>
      </div>

      {showAdd && (
        <AddUserModal
          onClose={() => setShowAdd(false)}
          onCreated={() => {
            setShowAdd(false);
            void refresh();
          }}
          addToast={addToast}
        />
      )}
      {editing && (
        <EditUserModal
          user={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void refresh();
          }}
          addToast={addToast}
        />
      )}
      {resettingPw && (
        <ResetPasswordModal
          user={resettingPw}
          onClose={() => setResettingPw(null)}
          onDone={() => setResettingPw(null)}
          addToast={addToast}
        />
      )}
    </div>
  );
}

function ModalShell({
  title,
  children,
  onClose,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 7000,
        background: 'rgba(0,0,0,0.7)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
      }}
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 480,
          maxWidth: '100%',
          background: 'var(--bg-secondary)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          padding: 20,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            letterSpacing: 2,
            color: '#F97316',
            textTransform: 'uppercase',
          }}
        >
          {title}
        </div>
        {children}
      </div>
    </div>
  );
}

function AddUserModal({
  onClose,
  onCreated,
  addToast,
}: {
  onClose: () => void;
  onCreated: () => void;
  addToast: (t: ToastInput) => void;
}) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('viewer');
  const [email, setEmail] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    if (!username || !password) return;
    setSubmitting(true);
    try {
      await createUser({ username, password, role, email: email || undefined, display_name: displayName || undefined });
      addToast({ kind: 'success', title: 'USER CREATED', body: username, icon: 'user-plus' });
      onCreated();
    } catch (e) {
      addToast({ kind: 'danger', title: 'CREATE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSubmitting(false);
  }

  return (
    <ModalShell title="Add User" onClose={onClose}>
      <Input label="USERNAME" value={username} onChange={setUsername} mono />
      <Input label="PASSWORD" value={password} onChange={setPassword} mono />
      <Input label="DISPLAY NAME" value={displayName} onChange={setDisplayName} />
      <Input label="EMAIL" value={email} onChange={setEmail} />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>ROLE</span>
        <select
          value={role}
          onChange={(e) => setRole(e.target.value as Role)}
          style={{
            background: 'var(--bg-tertiary)',
            border: '1px solid var(--border)',
            borderRadius: 4,
            padding: '8px 10px',
            color: 'var(--text-primary)',
            fontFamily: 'var(--font-mono)',
            fontSize: 12,
          }}
        >
          <option value="admin">admin</option>
          <option value="viewer">viewer</option>
        </select>
      </div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 6 }}>
        <Btn kind="ghost" onClick={onClose}>
          Cancel
        </Btn>
        <Btn kind="primary" onClick={submit} disabled={submitting || !username || !password}>
          {submitting ? 'Creating…' : 'Create'}
        </Btn>
      </div>
    </ModalShell>
  );
}

function EditUserModal({
  user,
  onClose,
  onSaved,
  addToast,
}: {
  user: User;
  onClose: () => void;
  onSaved: () => void;
  addToast: (t: ToastInput) => void;
}) {
  const [displayName, setDisplayName] = useState(user.display_name ?? '');
  const [email, setEmail] = useState(user.email ?? '');
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    setSubmitting(true);
    try {
      await updateUser(user.id, { display_name: displayName, email });
      addToast({ kind: 'success', title: 'USER UPDATED', body: user.username, icon: 'check-circle' });
      onSaved();
    } catch (e) {
      addToast({ kind: 'danger', title: 'UPDATE FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSubmitting(false);
  }

  return (
    <ModalShell title={`Edit ${user.username}`} onClose={onClose}>
      <Input label="DISPLAY NAME" value={displayName} onChange={setDisplayName} />
      <Input label="EMAIL" value={email} onChange={setEmail} />
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 6 }}>
        <Btn kind="ghost" onClick={onClose}>
          Cancel
        </Btn>
        <Btn kind="primary" onClick={submit} disabled={submitting}>
          {submitting ? 'Saving…' : 'Save'}
        </Btn>
      </div>
    </ModalShell>
  );
}

function ResetPasswordModal({
  user,
  onClose,
  onDone,
  addToast,
}: {
  user: User;
  onClose: () => void;
  onDone: () => void;
  addToast: (t: ToastInput) => void;
}) {
  const [pw, setPw] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    if (!pw) return;
    setSubmitting(true);
    try {
      await resetUserPassword(user.id, pw);
      addToast({ kind: 'success', title: 'PASSWORD RESET', body: user.username, icon: 'key' });
      onDone();
    } catch (e) {
      addToast({ kind: 'danger', title: 'RESET FAILED', body: (e as Error).message, icon: 'x' });
    }
    setSubmitting(false);
  }

  return (
    <ModalShell title={`Reset password — ${user.username}`} onClose={onClose}>
      <Input label="NEW PASSWORD" value={pw} onChange={setPw} mono />
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 6 }}>
        <Btn kind="ghost" onClick={onClose}>
          Cancel
        </Btn>
        <Btn kind="primary" onClick={submit} disabled={submitting || !pw}>
          {submitting ? 'Saving…' : 'Reset'}
        </Btn>
      </div>
    </ModalShell>
  );
}
