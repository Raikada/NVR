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
import { PageHeader } from './components/PageHeader';
import { Brackets, SectionHeader } from './components/primitives';
import { Overview } from './routes/Overview';
import { Cameras } from './routes/Cameras';
import { INITIAL_CAMERAS } from './lib/mockdata';
import type { AppState, Route, Toast, ToastInput } from './lib/types';
import { isRoute } from './lib/types';

function readHashRoute(): Route {
  const h = window.location.hash.slice(1);
  return isRoute(h) ? h : 'overview';
}

export function App() {
  const [route, setRoute] = useState<Route>(() => readHashRoute());
  const [showWizard, setShowWizard] = useState(true);

  const [state, setState] = useState<AppState>({
    hostname: 'rec-warehouse-a-01',
    serial: 'RKD-7B3A-2024',
    firmware: '3.1.2',
    ip: '10.0.1.31',
    gateway: '10.0.1.1',
    cameraCount: INITIAL_CAMERAS.length,
    recordingCount: INITIAL_CAMERAS.filter((c) => c.status === 'online').length,
    paired: false,
    managementServer: null,
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
      case 'pairing':
      case 'storage':
      case 'network':
      case 'logs':
      case 'diagnostics':
      case 'settings':
        return <RoutePlaceholder route={route} />;
    }
  })();

  return (
    <>
      <TopBar server={serverHeader} paired={state.paired} now={now} />
      <div className="shell">
        <IconRail route={route} go={go} />
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

// Placeholder for routes that have not been ported yet. Removed as
// each route lands in subsequent commits.
function RoutePlaceholder({ route }: { route: Route }) {
  const titles: Record<Route, string> = {
    overview: 'Overview',
    cameras: 'Cameras',
    pairing: 'Management Server Pairing',
    storage: 'Storage',
    network: 'Network',
    logs: 'Logs',
    diagnostics: 'Diagnostics',
    settings: 'Settings',
  };
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
      <PageHeader breadcrumb={`RECORDING SERVER / ${route.toUpperCase()}`} title={titles[route]} />
      <div style={{ padding: 20 }}>
        <div
          style={{
            position: 'relative',
            padding: 24,
            background: 'var(--bg-secondary)',
            border: '1px solid var(--border)',
            borderRadius: 6,
            maxWidth: 520,
          }}
        >
          <Brackets />
          <SectionHeader>NOT YET PORTED</SectionHeader>
          <p
            style={{
              fontFamily: 'var(--font-sans)',
              fontSize: 13,
              color: 'var(--text-secondary)',
              lineHeight: 1.6,
              margin: 0,
            }}
          >
            The {titles[route]} route lands in a follow-up commit per the staged
            handoff plan. The shell, Overview, and Setup Wizard are wired today.
          </p>
        </div>
      </div>
    </div>
  );
}
