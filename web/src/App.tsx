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
import { Policies } from './routes/Policies';
import { Pairing } from './routes/Pairing';
import { Storage } from './routes/Storage';
import { Network } from './routes/Network';
import { Logs } from './routes/Logs';
import { Diagnostics } from './routes/Diagnostics';
import { Settings } from './routes/Settings';
import { INITIAL_CAMERAS } from './lib/mockdata';
import { fetchIdentity } from './lib/api';
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

  // Periodic identity poll so the SPA reflects backend pairing-state
  // transitions the operator didn't trigger from this tab — most
  // notably MS-initiated unpair (the recorder's CRL poller detects
  // its own cert was revoked, runs ClearIssuedIdentity, and
  // /v1/recorder/identity flips to paired:false). Without this
  // poll the operator would see a stale "paired" state until a
  // manual refresh.
  //
  // 30s is a deliberate trade: fast enough that a remote unpair
  // surfaces within roughly a minute (CRL poll interval + identity
  // poll interval), slow enough that an idle browser tab isn't
  // burning recorder CPU on a request the operator usually doesn't
  // care about.
  useEffect(() => {
    let cancelled = false;
    const tick = async () => {
      try {
        const id = await fetchIdentity();
        if (cancelled) return;
        setState((prev) => {
          // Backend says unpaired but local state still thinks
          // paired: MS-initiated unpair (or another tab unpaired)
          // while this tab was open.
          if (prev.paired && !id.paired) {
            queueMicrotask(() =>
              addToast({
                kind: 'warning',
                title: 'UNPAIRED BY MANAGEMENT SERVER',
                body: 'The MS revoked this recorder. You can re-pair from the Pairing page.',
                icon: 'unlink',
              }),
            );
            return { ...prev, paired: false, managementServer: null };
          }
          // Backend says paired but local doesn't — rare (the
          // pairing flow updates local state directly), but covers
          // the case where another tab paired this recorder, or
          // the page loads with a recorder that was already paired.
          if (!prev.paired && id.paired) {
            return {
              ...prev,
              paired: true,
              managementServer: prev.managementServer ?? {
                host: 'Paired',
                ip: '',
                mac: '',
                cameras: 0,
                ver: '',
                trust: 'SIGNED',
              },
            };
          }
          return prev;
        });
      } catch {
        // Network blips are expected; next tick will retry.
      }
    };
    void tick();
    const interval = window.setInterval(tick, 30 * 1000);
    return () => {
      cancelled = true;
      window.clearInterval(interval);
    };
  }, [addToast]);

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
      case 'policies':
        return <Policies addToast={addToast} />;
      case 'pairing':
        return <Pairing state={state} setState={setState} addToast={addToast} />;
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

