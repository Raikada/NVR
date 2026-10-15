# Live E2E flow suite (`web/cypress/e2e/flows/`)

End-to-end tests that drive the REAL recorder (API + rendered SPA), not
mocks. They exist because `tsc` green does not mean the client works —
two shipped route/shape bugs (getMe `/me`, notifications `/notifications/*`)
compiled clean and only crashed at runtime.

## Running

```bash
# Recorder must be up and set up; export the admin password.
cd web
CYPRESS_ADMIN_PASSWORD=<admin-pw> npx cypress run --spec 'cypress/e2e/flows/*.cy.ts'
```

For the notification test-delivery flow, a local webhook sink on
127.0.0.1:8899 gives a 200 (otherwise the test still passes — it only
asserts our own API returns 200, not the downstream).

## Coverage (27 tests, all passing 2026-07-03)

| Spec | Flow |
|---|---|
| 01-auth | wrong-password rejection, UI login→shell→sign-out, `/auth/me` role |
| 02-cameras | create → set creds (no plaintext leak) → health → delete cascade; `event_channel` patch + reject; discovery list + bad-ref adopt 404; managed cameras have capability reports |
| 03-events-clips | vendor events list + camera filter; signed snapshot serves JPEG + tampered sig 403; event→clip (idempotent) → poll ready → signed mp4 download; acknowledge |
| 04-notifications | webhook target (secret not echoed) → subscription → test delivery → list → cleanup |
| 05-users-rbac | create viewer → viewer login → can list, denied create (403) → delete |
| 06-system-audit | settings get/patch, retention sweep, policies list, audit records mutations + no secret leak |
| 07-pages-render | all 11 SPA routes render without an uncaught error or index.html fallthrough |

Not covered live (state/hardware constraints): first-run setup wizard
(instance already set up; covered by the mocked `setup-wizard.cy.ts`),
TLS cert upload (swaps live certs), doorbell-press / person
classification (need a human in frame).
