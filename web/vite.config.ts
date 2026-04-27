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
    // Sourcemaps off for the embed bundle — they roughly triple the
    // recorder binary size and the recorder ships as a single static
    // appliance, not a public-facing web property where you'd open
    // devtools against the prod build. Run `npm run dev` (Vite dev
    // server) for a sourcemap-friendly debug experience instead.
    sourcemap: false,
    target: 'es2022',
  },
});
