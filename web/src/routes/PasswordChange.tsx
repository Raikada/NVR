// PasswordChange — forced rotation flow for the bootstrap admin's
// auto-generated initial password. Reached after a Login when the
// returned must_change_password flag is true. Unreachable otherwise.
//
// On successful POST /v1/recorder/local-users/me/password the
// returned JWT replaces the stored token and onChanged() is called,
// returning the App to the operator UI.

import { useState } from 'react';
import type { CSSProperties, FormEvent } from 'react';
import { changeRecorderPassword, setStoredToken } from '../lib/api';

interface Props {
  username: string;
  onChanged: () => void;
  onLogout: () => void;
}

export function PasswordChange({ username, onChanged, onLogout }: Props) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    if (next.length < 8) {
      setErr('New password must be at least 8 characters.');
      return;
    }
    if (next !== confirm) {
      setErr('Passwords do not match.');
      return;
    }
    setSubmitting(true);
    try {
      const res = await changeRecorderPassword(current, next);
      setStoredToken(res.token, res.expires_at, res.username);
      onChanged();
    } catch (e) {
      const msg =
        (e as { body?: { error?: string }; message?: string })?.body?.error ??
        (e as Error)?.message ??
        'Password change failed';
      setErr(msg);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        background: 'var(--bg-primary, #0a0a0a)',
        color: 'var(--text-primary, #f5f5f5)',
        fontFamily: 'var(--font-sans, system-ui, sans-serif)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
      }}
    >
      <div
        style={{
          width: '100%',
          maxWidth: 440,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: 20,
        }}
      >
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: 8,
          }}
        >
          <div
            style={{
              fontFamily: 'var(--font-mono, ui-monospace, monospace)',
              fontSize: 12,
              letterSpacing: 2,
              textTransform: 'uppercase',
              fontWeight: 600,
            }}
          >
            Set a new password
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-secondary, #999)' }}>
            Signed in as <strong>{username}</strong>
          </div>
        </div>

        <form
          onSubmit={handleSubmit}
          style={{
            width: '100%',
            background: 'var(--bg-secondary, #141414)',
            border: '1px solid var(--border, #2a2a2a)',
            borderRadius: 4,
            padding: 28,
            display: 'flex',
            flexDirection: 'column',
            gap: 14,
          }}
        >
          <div
            style={{
              padding: '10px 12px',
              background: 'rgba(249,115,22,0.08)',
              border: '1px solid rgba(249,115,22,0.27)',
              borderRadius: 4,
              fontSize: 12,
              lineHeight: 1.5,
              color: 'var(--text-primary, #f5f5f5)',
            }}
          >
            Your account is using the bootstrap initial password. Set a new password
            to continue. Minimum 8 characters.
          </div>

          <Field label="Current password">
            <Input type="password" value={current} onChange={setCurrent} autoFocus />
          </Field>
          <Field label="New password">
            <Input type="password" value={next} onChange={setNext} />
          </Field>
          <Field label="Confirm new password">
            <Input type="password" value={confirm} onChange={setConfirm} />
          </Field>

          {err && (
            <div
              style={{
                padding: '10px 12px',
                background: 'rgba(239,68,68,0.1)',
                border: '1px solid rgba(239,68,68,0.27)',
                borderRadius: 4,
                fontSize: 12,
                color: '#EF4444',
              }}
            >
              {err}
            </div>
          )}

          <button
            type="submit"
            disabled={submitting || !current || !next || !confirm}
            style={{
              height: 38,
              marginTop: 4,
              border: '1px solid rgba(249,115,22,0.7)',
              background:
                submitting || !current || !next || !confirm
                  ? 'rgba(249,115,22,0.4)'
                  : 'rgba(249,115,22,1)',
              color: '#000',
              borderRadius: 4,
              fontFamily: 'var(--font-mono, ui-monospace, monospace)',
              fontSize: 12,
              fontWeight: 600,
              letterSpacing: 1.5,
              textTransform: 'uppercase',
              cursor:
                submitting || !current || !next || !confirm ? 'not-allowed' : 'pointer',
            }}
          >
            {submitting ? 'Updating…' : 'Set password'}
          </button>

          <button
            type="button"
            onClick={onLogout}
            style={{
              background: 'transparent',
              border: 'none',
              color: 'var(--text-muted, #666)',
              fontSize: 11,
              fontFamily: 'var(--font-mono, ui-monospace, monospace)',
              cursor: 'pointer',
              textTransform: 'uppercase',
              letterSpacing: 1,
              padding: '4px 8px',
            }}
          >
            Sign out
          </button>
        </form>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span
        style={{
          fontFamily: 'var(--font-mono, ui-monospace, monospace)',
          fontSize: 10,
          letterSpacing: 1,
          color: 'var(--text-secondary, #999)',
          textTransform: 'uppercase',
        }}
      >
        {label}
      </span>
      {children}
    </label>
  );
}

function Input({
  type = 'text',
  value,
  onChange,
  autoFocus,
}: {
  type?: string;
  value: string;
  onChange: (v: string) => void;
  autoFocus?: boolean;
}) {
  const [focused, setFocused] = useState(false);
  const baseStyle: CSSProperties = {
    fontFamily: 'var(--font-sans, system-ui, sans-serif)',
    fontSize: 13,
    height: 36,
    padding: '0 12px',
    background: 'var(--bg-tertiary, #1a1a1a)',
    border: focused
      ? '1px solid rgba(249,115,22,0.55)'
      : '1px solid var(--border, #2a2a2a)',
    boxShadow: focused ? '0 0 0 2px rgba(249,115,22,0.13)' : 'none',
    borderRadius: 4,
    color: 'var(--text-primary, #f5f5f5)',
    outline: 'none',
    transition: 'border 150ms ease-out, box-shadow 150ms ease-out',
  };
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      autoFocus={autoFocus}
      style={baseStyle}
    />
  );
}
