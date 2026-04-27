// Top-level app shell. Commit 1 ships a placeholder while the route
// surface and shell components land in subsequent commits.
//
// Commit 2 lands TopBar / IconRail / Toast / SetupWizard / Overview.
// Commit 3 lands Cameras (list, drawer, discover, manual-add wizard).
// Commit 4 lands Pairing / Storage / Network / Logs / Diagnostics / Settings.
// Commit 5 wires the embed into the recorder Go binary.

import { Brackets, SectionHeader } from './components/primitives';

export function App() {
  return (
    <div
      style={{
        flex: 1,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 40,
      }}
    >
      <div
        style={{
          position: 'relative',
          padding: 32,
          maxWidth: 520,
          background: 'var(--bg-secondary)',
          border: '1px solid var(--border)',
          borderRadius: 8,
        }}
      >
        <Brackets />
        <SectionHeader>RAIKADA RECORDING SERVER</SectionHeader>
        <h1
          style={{
            fontFamily: 'var(--font-sans)',
            fontSize: 22,
            fontWeight: 600,
            color: 'var(--text-primary)',
            margin: '4px 0 8px',
          }}
        >
          Configuration UI scaffold
        </h1>
        <p
          style={{
            fontFamily: 'var(--font-sans)',
            fontSize: 13,
            color: 'var(--text-secondary)',
            lineHeight: 1.6,
            margin: 0,
          }}
        >
          Foundation only — design tokens, icon set, and shared primitives are in
          place. The shell, routes, and embed wiring land in subsequent commits.
        </p>
      </div>
    </div>
  );
}
