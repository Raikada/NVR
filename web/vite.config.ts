import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vite config for the recorder configuration SPA.
//
// Build output goes to dist/ and is consumed by the Go embed in
// internal/web/. Run `make web` from the recorder root to rebuild.
//
// Dev mode (npm run dev) serves the SPA on :5173 and proxies /v1
// to the recorder's API server (default :9997 per mediamtx.yml). Run
// the recorder separately during dev work.
export default defineConfig({
  plugins: [react()],
  base: './',
  server: {
    port: 5173,
    proxy: {
      '/v1': {
        target: 'http://localhost:9997',
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    target: 'es2022',
    // The Go embed copies the entire dist/ tree, so no special
    // chunking. Default Vite output is fine.
  },
});
