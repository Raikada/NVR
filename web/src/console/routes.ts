// Console route ids — hash-routed (#live, #playback, …).
export type ConsoleRoute =
  | 'live'
  | 'playback'
  | 'search'
  | 'devices'
  | 'storage'
  | 'users'
  | 'health'
  | 'alerts'
  | 'settings';

const ROUTES: ConsoleRoute[] = [
  'live', 'playback', 'search', 'devices', 'storage', 'users', 'health', 'alerts', 'settings',
];

export function isConsoleRoute(v: string): v is ConsoleRoute {
  return (ROUTES as string[]).includes(v);
}
