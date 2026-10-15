// Operator-console smoke against a live recorder. Run with:
//   CYPRESS_ADMIN_PASSWORD=... npx cypress run \
//     --config baseUrl=http://localhost:19997 \
//     --spec cypress/e2e/console-smoke.cy.ts
//
// Drives the real login form, then walks every console route and
// exercises the camera + event surfaces end-to-end via the UI's own
// data layer (no mocks).

function uiLogin() {
  const password = Cypress.env('ADMIN_PASSWORD');
  if (!password) throw new Error('Set CYPRESS_ADMIN_PASSWORD to run the console smoke');
  cy.visit('/');
  cy.get('input').first().clear().type('admin');
  cy.get('input[type="password"]').clear().type(password);
  cy.get('button[type="submit"], button').contains(/sign in/i).click();
  // TopBar brand renders once the console shell mounts.
  cy.contains('RAIKADA', { timeout: 10000 }).should('be.visible');
}

describe('operator console smoke', () => {
  const camName = `cy_console_${Date.now()}`;
  let camId = '';

  before(() => {
    // Seed one camera over the API so screens have real rows to render.
    const password = Cypress.env('ADMIN_PASSWORD');
    cy.request({ method: 'POST', url: '/v1/recorder/login', body: { username: 'admin', password } })
      .then((r) => {
        expect(r.status).to.eq(200);
        const token = (r.body as { token: string }).token;
        cy.request({
          method: 'POST', url: '/v1/cameras',
          headers: { Authorization: `Bearer ${token}` },
          body: { name: camName, source_type: 'rtsp', source_url: 'rtsp://192.0.2.50:554/none' },
        }).then((r2) => {
          expect(r2.status).to.eq(201);
          camId = (r2.body as { id: string }).id;
          cy.request({
            method: 'POST', url: `/v1/cameras/${camId}/bookmarks`,
            headers: { Authorization: `Bearer ${token}` },
            body: { note: 'console smoke' },
          }).its('status').should('eq', 201);
        });
      });
  });

  after(() => {
    const password = Cypress.env('ADMIN_PASSWORD');
    cy.request({ method: 'POST', url: '/v1/recorder/login', body: { username: 'admin', password } })
      .then((r) => {
        const token = (r.body as { token: string }).token;
        if (camId) {
          cy.request({
            method: 'DELETE', url: `/v1/cameras/${camId}`,
            headers: { Authorization: `Bearer ${token}` },
            failOnStatusCode: false,
          });
        }
      });
  });

  it('logs in and renders live view with the seeded camera', () => {
    uiLogin();
    cy.contains('Live View').should('be.visible');
    // Camera panel lists the seeded camera (offline is fine).
    cy.contains(camName, { timeout: 15000 }).should('be.visible');
    // Status pills present.
    cy.contains(/ONLINE|OFFLINE|DEGRADED/).should('exist');
  });

  it('walks every console route', () => {
    uiLogin();
    const routes: Array<[string, string]> = [
      ['#playback', 'Playback'],
      ['#search', 'Event Search'],
      ['#devices', 'Devices'],
      ['#storage', 'Storage & Retention'],
      ['#users', 'Users & Permissions'],
      ['#health', 'System Health'],
      ['#alerts', 'Alerts'],
      ['#settings', 'Settings'],
    ];
    for (const [hash, title] of routes) {
      cy.visit(`/${hash}`);
      cy.contains(title, { timeout: 10000 }).should('be.visible');
    }
  });

  it('shows the bookmark event in search and alerts', () => {
    uiLogin();
    cy.visit('/#search');
    cy.contains('Event Search').should('be.visible');
    cy.contains('BOOKMARK', { timeout: 15000 }).should('exist');
    cy.visit('/#alerts');
    cy.contains(/BOOKMARK/i, { timeout: 15000 }).should('exist');
  });

  it('devices screen lists the camera and opens the detail flyout', () => {
    uiLogin();
    cy.visit('/#devices');
    cy.contains(camName, { timeout: 15000 }).click();
    cy.contains('HARDWARE').should('be.visible');
    cy.contains('Reboot').should('be.visible');
  });

  it('storage shows tiers and users shows roles', () => {
    uiLogin();
    cy.visit('/#storage');
    cy.contains('RETENTION TIERS', { timeout: 10000 }).should('be.visible');
    cy.visit('/#users');
    cy.contains('admin', { timeout: 10000 }).should('be.visible');
  });
});
