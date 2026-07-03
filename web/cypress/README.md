# Cypress E2E Tests

Cypress specs are committed but not run as part of this phase. To run them: start the recorder with a fresh identity dir, set `CYPRESS_ADMIN_PASSWORD` from the printed initial password, then `cd web && npm run cy:run`.

## Specs

- `e2e/setup-wizard.cy.ts` — first-run wizard end-to-end. Requires a fresh recorder state (`setup_required=true`).
- `e2e/cameras.cy.ts` — camera CRUD + credential rotation lifecycle (does not verify live video; see `docs/superpowers/runbooks/real-camera-smoke.md` for that).
- `e2e/notifications.cy.ts` — webhook target signing + dispatcher wiring.

## Helpers

`cypress/support/commands.ts` provides:

- `cy.loginAsAdmin()` — logs in as `admin` using `CYPRESS_ADMIN_PASSWORD` and stores the JWT in `localStorage`.
- `cy.apiGet(path)` / `cy.apiPost(path, body)` — authenticated REST helpers.

## Environment variables

- `CYPRESS_ADMIN_PASSWORD` — bootstrap admin password (post-setup).
- `CYPRESS_INITIAL_ADMIN_PASSWORD` — printed initial admin password (only needed for the setup-wizard spec on a fresh instance).

## Reset between runs

There is no automated reset script in this phase. To re-run the setup-wizard spec, manually wipe the recorder's identity dir and restart the binary.
