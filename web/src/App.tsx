// Top-level app shell: TopBar + IconRail + active route + SetupWizard
// overlay + Toast stack. Route resolution is hash-based per the design
// (window.location.hash → Route enum).
//
// Commit 2 ships shell + Overview + SetupWizard. Subsequent commits
// land Cameras (3), the remaining five routes (4), and the Go embed
// wiring (5). Routes not yet ported render a placeholder card.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { TopBar } from './components/TopBar';
import { IconRail } from './components/IconRail';
import { ToastStack } from './components/Toast';
import { SetupWizard } from './components/SetupWizard';
import { Overview } from './routes/Overview';
import { Cameras } from './routes/Cameras';
import { Events } from './routes/Events';
import { Policies } from './routes/Policies';
import { Storage } from './routes/Storage';
import { Network } from './routes/Network';
import { Logs } from './routes/Logs';
import { Diagnostics } from './routes/Diagnostics';
import { Settings } from './routes/Settings';
import { Users } from './routes/Users';
import { Notifications } from './routes/Notifications';
import { Login } from './routes/Login';
import { PasswordChange } from './routes/PasswordChange';
import { INITIAL_CAMERAS } from './lib/mockdata';
import {
  clearStoredToken,
  getMe,
  getSetupStatus,
  getStoredToken,
  getStoredUsername,
  setOnUnauthorized,
} from './lib/api';
import type { AppState, MeResponse, Route, Toast, ToastInput } from './lib/types';
import { isRoute } from './lib/types';

function readHashRoute(): Route {
  const h = window.location.hash.slice(1);
  return isRoute(h) ? h : 'overview';
}

// AuthState — what the App is rendering with respect to the recorder-
// local JWT.
//   - "loading":           initial render before localStorage was inspected.
//   - "anonymous":         no valid token; render <Login>.
//   - "must_change_password": token valid but the user is on the
//                          bootstrap initial password; render <PasswordChange>.
//   - "authenticated":     normal SPA.
type AuthState = 'loading' | 'anonymous' | 'must_change_password' | 'authenticated';

export function App() {
  const [route, setRoute] = useState<Route>(() => readHashRoute());
  // Wizard visibility is gated on /v1/system/setup-status. Default
  // hidden so existing recorders don't suddenly see a wizard; we
  // flip it open after the first authenticated setup-status fetch
  // returns setup_required:true.
  const [showWizard, setShowWizard] = useState(false);

  // Auth gate (pre-pairing auth slice 2026-05-06). The recorder no
  // longer accepts anonymous /v1/* requests by default, so the SPA
  // surfaces a login screen until a recorder-local JWT is present in
  // localStorage. The default Login → SetupWizard → operator UI flow:
  //   1. App mounts, finds no token → render <Login>.
  //   2. Operator submits credentials → /v1/recorder/login returns JWT.
  //      If must_change_password=true → render <PasswordChange>.
  //   3. After password change → render normal SPA + first-run
  //      SetupWizard overlay so pairing can proceed.
  const [auth, setAuth] = useState<AuthState>(() => {
    const t = getStoredToken();
    return t ? 'authenticated' : 'anonymous';
  });
  const [authUser, setAuthUser] = useState<string>(() => getStoredUsername() ?? 'admin');
  const [claims, setClaims] = useState<MeResponse | null>(null);
  const isAdmin = claims?.user.role === 'admin';

  // Wire the api.ts 401 handler so an expired-token request bumps the
  // SPA back to the Login screen instead of cascading hard errors.
  useEffect(() => {
    setOnUnauthorized(() => {
      setAuth('anonymous');
    });
    return () => setOnUnauthorized(null);
  }, []);

  const handleLogin = useCallback((mustChangePassword: boolean) => {
    setAuthUser(getStoredUsername() ?? 'admin');
    setAuth(mustChangePassword ? 'must_change_password' : 'authenticated');
  }, []);

  const handlePasswordChanged = useCallback(() => {
    setAuth('authenticated');
  }, []);

  const handleLogout = useCallback(() => {
    clearStoredToken();
    setAuth('anonymous');
  }, []);

  const [state, setState] = useState<AppState>({
    hostname: 'rec-warehouse-a-01',
    serial: 'RKD-7B3A-2024',
    firmware: '3.1.2',
    ip: '10.0.1.31',
    gateway: '10.0.1.1',
    cameraCount: INITIAL_CAMERAS.length,
    recordingCount: INITIAL_CAMERAS.filter((c) => c.status === 'online').length,
    cameras: INITIAL_CAMERAS,
  });

  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const i = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(i);
  }, []);

  const [toasts, setToasts] = useState<Toast[]>([]);
  const addToast = useCallback((t: ToastInput) => {
    const id = 'tst' + Date.now() + Math.random().toString(36).slice(2, 6);
    setToasts((prev) => [...prev, { ...t, id }]);
    window.setTimeout(() => setToasts((prev) => prev.filter((x) => x.id !== id)), 4500);
  }, []);
  const removeToast = useCallback((id: string) => {
    setToasts((prev) => prev.filter((x) => x.id !== id));
  }, []);

  // Load /v1/me claims (role + permissions) once authenticated so
  // admin-gated routes (Users, Notifications) can reveal themselves.
  useEffect(() => {
    if (auth !== 'authenticated') return;
    let cancelled = false;
    void getMe()
      .then((m) => {
        if (cancelled) return;
        setClaims(m);
      })
      .catch(() => {
        // Endpoint absent on older builds; treat as non-admin.
      });
    return () => {
      cancelled = true;
    };
  }, [auth]);

  // Once authenticated, ask the recorder whether first-run setup is
  // still pending. setup-status returns {setup_required:true} until
  // the wizard's "Done" path persists setup_complete=true.
  useEffect(() => {
    if (auth !== 'authenticated') return;
    let cancelled = false;
    void getSetupStatus()
      .then((r) => {
        if (cancelled) return;
        if (r.setup_required) setShowWizard(true);
      })
      .catch(() => {
        // Endpoint absent on older builds; leave the wizard hidden.
      });
    return () => {
      cancelled = true;
    };
  }, [auth]);

  const go = useCallback((r: Route) => {
    setRoute(r);
    window.location.hash = r;
  }, []);

  useEffect(() => {
    const h = () => {
      const v = window.location.hash.slice(1);
      if (isRoute(v)) setRoute(v);
    };
    window.addEventListener('hashchange', h);
    return () => window.removeEventListener('hashchange', h);
  }, []);

  const recordingCount = state.cameras.filter((c) => c.status === 'online').length;
  const serverHeader = useMemo(
    () => ({ ...state, recordingCount, cameraCount: state.cameras.length }),
    [state, recordingCount],
  );

  const main = (() => {
    switch (route) {
      case 'overview':
        return (
          <Overview
            state={state}
            setState={setState}
            go={go}
            addToast={addToast}
            setShowWizard={setShowWizard}
          />
        );
      case 'cameras':
        return <Cameras state={state} setState={setState} addToast={addToast} />;
      case 'events':
        return <Events addToast={addToast} />;
      case 'policies':
        return <Policies addToast={addToast} />;
      case 'storage':
        return <Storage />;
      case 'network':
        return <Network state={state} />;
      case 'logs':
        return <Logs addToast={addToast} />;
      case 'diagnostics':
        return <Diagnostics state={state} addToast={addToast} />;
      case 'settings':
        return <Settings state={state} addToast={addToast} />;
      case 'users':
        return isAdmin ? <Users addToast={addToast} /> : <Overview state={state} setState={setState} go={go} addToast={addToast} setShowWizard={setShowWizard} />;
      case 'notifications':
        return isAdmin ? <Notifications addToast={addToast} /> : <Overview state={state} setState={setState} go={go} addToast={addToast} setShowWizard={setShowWizard} />;
    }
  })();

  // Auth gate renders the appropriate top-level screen before the
  // operator UI mounts. SetupWizard, identity poll, and toasts all
  // hang off the post-auth tree.
  if (auth === 'anonymous') {
    return <Login onLogin={handleLogin} />;
  }
  if (auth === 'must_change_password') {
    return (
      <PasswordChange
        username={authUser}
        onChanged={handlePasswordChanged}
        onLogout={handleLogout}
      />
    );
  }

  return (
    <>
      <TopBar
        server={serverHeader}
        now={now}
        username={authUser}
        onLogout={handleLogout}
      />
      <div className="shell">
        <IconRail route={route} go={go} isAdmin={isAdmin} />
        {main}
      </div>
      {showWizard && (
        <SetupWizard
          state={state}
          setState={setState}
          addToast={addToast}
          onDone={() => {
            setShowWizard(false);
            go('overview');
            addToast({
              kind: 'success',
              title: 'SETUP COMPLETE',
              body: 'Recorder is operational',
              icon: 'check-circle',
            });
          }}
          onDismiss={() => setShowWizard(false)}
        />
      )}
      <ToastStack toasts={toasts} remove={removeToast} />
    </>
  );
}

