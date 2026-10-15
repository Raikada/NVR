// Operator console shell — auth gates (Login / forced password change),
// then the console chrome: TopBar, IconRail, active screen, AlertsPanel
// slide-out, FooterStrip. Hash-routed (#live, #playback, …).
import { useCallback, useEffect, useState } from 'react';
import { Login } from './routes/Login';
import { PasswordChange } from './routes/PasswordChange';
import { ConsoleProvider, useConsole } from './lib/console';
import { ConsoleTopBar, FooterStrip, useChromeStats } from './console/Shell';
import { IconRail } from './console/IconRail';
import { isConsoleRoute, type ConsoleRoute } from './console/routes';
import { useTheme } from './console/theme';
import { LiveView } from './console/LiveView';
import { Playback } from './console/Playback';
import { Search } from './console/Search';
import { AlertsFeed, AlertsPanel } from './console/Alerts';
import { Devices } from './console/Devices';
import { Storage } from './console/Storage';
import { Users } from './console/Users';
import { CameraPanel, type Tour } from './console/CameraPanel';
import { Icon } from './components/Icon';
import {
  clearStoredToken, getMe, getStoredToken, getStoredUsername,
  setOnUnauthorized, startSessionRefresh,
} from './lib/api';
import type { MeResponse } from './lib/types';

function readHashRoute(): ConsoleRoute {
  const h = window.location.hash.slice(1);
  return isConsoleRoute(h) ? h : 'live';
}

type AuthState = 'anonymous' | 'must_change_password' | 'authenticated';

export function App() {
  const [auth, setAuth] = useState<AuthState>(() => (getStoredToken() ? 'authenticated' : 'anonymous'));
  const [authUser, setAuthUser] = useState<string>(() => getStoredUsername() ?? 'admin');

  useEffect(() => {
    setOnUnauthorized(() => setAuth('anonymous'));
    return () => setOnUnauthorized(null);
  }, []);

  useEffect(() => {
    if (auth !== 'authenticated') return;
    return startSessionRefresh();
  }, [auth]);

  const handleLogin = useCallback((mustChangePassword: boolean) => {
    setAuthUser(getStoredUsername() ?? 'admin');
    setAuth(mustChangePassword ? 'must_change_password' : 'authenticated');
  }, []);

  const handleLogout = useCallback(() => {
    clearStoredToken();
    setAuth('anonymous');
  }, []);

  if (auth === 'anonymous') return <Login onLogin={handleLogin} />;
  if (auth === 'must_change_password') {
    return (
      <PasswordChange
        username={authUser}
        onChanged={() => setAuth('authenticated')}
        onLogout={handleLogout}
      />
    );
  }

  return (
    <ConsoleProvider>
      <Console username={authUser} onLogout={handleLogout} />
    </ConsoleProvider>
  );
}

// PlaybackTarget — camera + position a screen asked Playback to open at.
export interface PlaybackTarget {
  camId: string;
  at?: Date;
  key: number;
}

function Console({ username, onLogout }: { username: string; onLogout: () => void }) {
  const data = useConsole();
  const [theme, setTheme] = useTheme();
  const { health, volumes } = useChromeStats();
  const [route, setRoute] = useState<ConsoleRoute>(() => readHashRoute());
  const [camPanelOpen, setCamPanelOpen] = useState(true);
  const [alertsPanelOpen, setAlertsPanelOpen] = useState(false);
  const [focusCamId, setFocusCamId] = useState<string | null>(null);
  const [playbackTarget, setPlaybackTarget] = useState<PlaybackTarget | null>(null);
  const [tour, setTour] = useState<Tour | null>(null);
  const [claims, setClaims] = useState<MeResponse | null>(null);

  useEffect(() => {
    void getMe().then(setClaims).catch(() => {});
  }, []);

  const navigate = useCallback((r: ConsoleRoute) => {
    setRoute(r);
    window.location.hash = r;
    setFocusCamId(null);
    setTour(null);
  }, []);

  useEffect(() => {
    const h = () => {
      const v = window.location.hash.slice(1);
      if (isConsoleRoute(v)) setRoute(v);
    };
    window.addEventListener('hashchange', h);
    return () => window.removeEventListener('hashchange', h);
  }, []);

  const openPlayback = useCallback((camId: string, at?: Date) => {
    setPlaybackTarget({ camId, at, key: Date.now() });
    setRoute('playback');
    window.location.hash = 'playback';
    setFocusCamId(null);
    setAlertsPanelOpen(false);
  }, []);

  const openLive = useCallback((camId: string) => {
    setRoute('live');
    window.location.hash = 'live';
    setFocusCamId(camId);
  }, []);

  // Global shortcuts: / search, s alerts, c camera panel, 1/2/3 nav, esc back.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement).tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
      const k = e.key.toLowerCase();
      if (e.key === '/') { e.preventDefault(); navigate('search'); }
      else if (k === 's') setAlertsPanelOpen((o) => !o);
      else if (k === 'c') setCamPanelOpen((o) => !o);
      else if (e.key === 'Escape') { setFocusCamId(null); setAlertsPanelOpen(false); }
      else if (e.key === '1') navigate('live');
      else if (e.key === '2') navigate('playback');
      else if (e.key === '3') navigate('search');
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate]);

  const startTour = useCallback((t: Tour | null) => {
    if (!t) { setTour(null); setFocusCamId(null); return; }
    setRoute('live');
    window.location.hash = 'live';
    setTour(t);
  }, []);

  const sidePanel = route === 'live' || route === 'playback';

  const screen = (() => {
    switch (route) {
      case 'live':
        return (
          <LiveView
            focusId={focusCamId}
            setFocusId={setFocusCamId}
            onOpenPlayback={openPlayback}
            tour={tour}
            onStopTour={() => setTour(null)}
          />
        );
      case 'playback':
        return (
          <Playback
            key={playbackTarget?.key ?? 'pb'}
            camera={playbackTarget ? data.getCam(playbackTarget.camId) ?? null : null}
            initialAt={playbackTarget?.at}
          />
        );
      case 'search':
        return <Search onOpenPlayback={openPlayback} />;
      case 'devices':
        return <Devices onOpenLive={openLive} />;
      case 'storage':
        return <Storage />;
      case 'users':
        return <Users />;
      case 'health':
        return <Placeholder name="System Health" />;
      case 'alerts':
        return <AlertsFeed onOpenPlayback={openPlayback} />;
      case 'settings':
        return <Placeholder name="Settings" />;
    }
  })();

  void claims; // role-gating lands with the Users screen

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--bg-primary)' }}>
      <ConsoleTopBar
        username={username}
        theme={theme}
        setTheme={setTheme}
        health={health}
        onOpenHealth={() => navigate('health')}
        onOpenUsers={() => navigate('users')}
        onLogout={onLogout}
      />
      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        <IconRail
          active={route}
          onNavigate={navigate}
          onAlerts={() => setAlertsPanelOpen((o) => !o)}
          alertsCount={data.unreadCount}
        />
        {sidePanel && (
          <CameraPanel
            open={camPanelOpen}
            onClose={() => setCamPanelOpen(false)}
            onPick={(c) => { if (route === 'playback') openPlayback(c.id); else openLive(c.id); }}
            activeId={focusCamId || playbackTarget?.camId || null}
            onStartTour={startTour}
            activeTour={tour}
          />
        )}
        {sidePanel && !camPanelOpen && (
          <button
            onClick={() => setCamPanelOpen(true)}
            title="Show cameras (C)"
            style={{ width: 20, background: 'var(--bg-secondary)', borderRight: '1px solid var(--border)', borderTop: 'none', borderBottom: 'none', borderLeft: 'none', cursor: 'pointer', color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          >
            <Icon name="panel-left-open" size={12} />
          </button>
        )}
        {screen}
        <AlertsPanel
          open={alertsPanelOpen}
          onClose={() => setAlertsPanelOpen(false)}
          onOpenFull={() => { setAlertsPanelOpen(false); navigate('alerts'); }}
          onOpenPlayback={openPlayback}
        />
      </div>
      <FooterStrip
        health={health}
        volumes={volumes}
        onOpenHealth={() => navigate('health')}
        onOpenStorage={() => navigate('storage')}
      />
    </div>
  );
}

function Placeholder({ name }: { name: string }) {
  return (
    <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column', gap: 10 }}>
      <span style={{ fontFamily: 'var(--font-sans)', fontSize: 16, fontWeight: 600, color: 'var(--text-primary)' }}>{name}</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: 1, color: 'var(--text-muted)' }}>SCREEN UNDER CONSTRUCTION</span>
    </div>
  );
}
