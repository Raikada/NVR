// Login — recorder-local operator authentication. Pre-pairing auth
// slice 2026-05-06.
//
// Renders pre-auth (App.tsx routes here when no valid token is in
// localStorage). On successful POST /v1/recorder/login, the response
// JWT + expires_at + username are persisted via lib/api setStoredToken,
// onLogin() is called, and App.tsx routes to PasswordChange (if
// must_change_password) or the operator UI.
//
// Visual idiom intentionally mirrors management/web/src/routes/Login.tsx:
// centered card, design-system primitives (mono uppercase labels,
// dark inputs with orange focus, primary-orange submit). The recorder
// SPA doesn't ship the same primitives module, so the relevant styling
// is inlined here against the existing CSS variables.

import { useState } from 'react';
import type { CSSProperties, FormEvent } from 'react';
import { loginRecorder, setStoredToken } from '../lib/api';

interface Props {
  onLogin: (mustChangePassword: boolean) => void;
}

export function Login({ onLogin }: Props) {
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    setSubmitting(true);
    try {
      const res = await loginRecorder(username.trim(), password);
      setStoredToken(res.token, res.expires_at, res.username);
      onLogin(res.must_change_password);
    } catch (e) {
      const msg =
        (e as { body?: { error?: string }; message?: string })?.body?.error ??
        (e as Error)?.message ??
        'Login failed';
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
        position: 'relative',
      }}
    >
      <div
        style={{
          width: '100%',
          maxWidth: 400,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: 24,
        }}
      >
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: 12,
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
            Raikada Recorder
          </div>
          <div
            style={{
              fontSize: 12,
              color: 'var(--text-secondary, #999)',
            }}
          >
            Sign in to continue
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
          <FieldLabel label="Username">
            <FieldInput
              type="text"
              value={username}
              onChange={setUsername}
              autoComplete="username"
              autoFocus
            />
          </FieldLabel>
          <FieldLabel label="Password">
            <FieldInput
              type="password"
              value={password}
              onChange={setPassword}
              autoComplete="current-password"
            />
          </FieldLabel>

          {err && <ErrorPanel message={err} />}

          <button
            type="submit"
            disabled={submitting || !username || !password}
            style={{
              height: 38,
              marginTop: 4,
              border: '1px solid rgba(249,115,22,0.7)',
              background:
                submitting || !username || !password
                  ? 'rgba(249,115,22,0.4)'
                  : 'rgba(249,115,22,1)',
              color: '#000',
              borderRadius: 4,
              fontFamily: 'var(--font-mono, ui-monospace, monospace)',
              fontSize: 12,
              fontWeight: 600,
              letterSpacing: 1.5,
              textTransform: 'uppercase',
              cursor: submitting || !username || !password ? 'not-allowed' : 'pointer',
            }}
          >
            {submitting ? 'Signing in…' : 'Sign in'}
          </button>

          <div
            style={{
              fontSize: 11,
              color: 'var(--text-muted, #666)',
              lineHeight: 1.5,
              marginTop: 4,
            }}
          >
            First-time setup? The bootstrap password is printed to the recorder's
            startup logs and saved to{' '}
            <code style={{ fontFamily: 'var(--font-mono, ui-monospace, monospace)' }}>
              identity/initial-admin-password.txt
            </code>{' '}
            on the recorder host.
          </div>
        </form>
      </div>
    </div>
  );
}

function FieldLabel({ label, children }: { label: string; children: React.ReactNode }) {
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

function FieldInput({
  type = 'text',
  value,
  onChange,
  autoComplete,
  autoFocus,
}: {
  type?: string;
  value: string;
  onChange: (v: string) => void;
  autoComplete?: string;
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
      autoComplete={autoComplete}
      autoFocus={autoFocus}
      style={baseStyle}
    />
  );
}

function ErrorPanel({ message }: { message: string }) {
  return (
    <div
      style={{
        padding: '10px 12px',
        background: 'rgba(239,68,68,0.1)',
        border: '1px solid rgba(239,68,68,0.27)',
        borderRadius: 4,
        fontSize: 12,
        color: '#EF4444',
        lineHeight: 1.5,
      }}
    >
      {message}
    </div>
  );
}
