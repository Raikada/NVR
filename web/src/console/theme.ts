// Theme state — dark (default) / light, persisted to localStorage and
// applied via the <html data-theme> attribute the tokens.css variants
// key off. Applied pre-paint by the index.html bootstrap script.
import { useCallback, useEffect, useState } from 'react';

export type Theme = 'dark' | 'light';

export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setThemeState] = useState<Theme>(() => {
    try {
      return localStorage.getItem('rk.theme') === 'light' ? 'light' : 'dark';
    } catch {
      return 'dark';
    }
  });

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    try {
      localStorage.setItem('rk.theme', theme);
    } catch {
      /* ignore */
    }
  }, [theme]);

  const setTheme = useCallback((t: Theme) => setThemeState(t), []);
  return [theme, setTheme];
}
