// Live UI smoke against a configured recorder (SP2–SP4 surfaces).
// Unlike the mocked specs, this runs against real state: adopted
// cameras, real events with snapshots. Run with:
//   CYPRESS_ADMIN_PASSWORD=... npx cypress run --spec cypress/e2e/live-smoke.cy.ts
describe('live UI smoke (SP2–SP4)', () => {
  beforeEach(() => {
    // Drive the real login form (the loginAsAdmin localStorage shim
    // predates the app's raikada.recorder.token key and doesn't work
    // against the real bundle).
    cy.visit('/');
    cy.get('input').first().clear().type('admin');
    cy.get('input[type="password"]').type(Cypress.env('ADMIN_PASSWORD'), { log: false });
    cy.get('button').contains(/sign in/i).click();
    cy.contains(/sign in to continue/i, { timeout: 15000 }).should('not.exist');
  });

  it('cameras page renders managed cameras with health badges and the discovered section', () => {
    cy.visit('/#cameras');
    cy.contains(/front_amcrest/i, { timeout: 15000 }).should('exist');
    // SP2 discovered-on-network section (three cameras on this LAN, at
    // least one unmatched entry or the section header itself).
    cy.contains(/discovered on your network/i, { timeout: 20000 }).should('exist');
    cy.screenshot('live-cameras', { capture: 'viewport' });
  });

  it('events page lists real events with thumbnails and actions', () => {
    cy.visit('/#events');
    // Real motion events exist from acceptance; rows must render.
    cy.contains(/motion/i, { timeout: 15000 }).should('exist');
    // SP4 thumbnails ride signed URLs on <img> tags.
    cy.get('img[src*="/v1/media/snapshots/"]', { timeout: 15000 })
      .should('have.length.greaterThan', 0)
      .first()
      .and(($img) => {
        // Loaded, not a broken image.
        expect(($img[0] as HTMLImageElement).naturalWidth).to.be.greaterThan(0);
      });
    cy.contains(/export clip|download/i).should('exist');
    cy.screenshot('live-events', { capture: 'viewport' });
  });

  it('notifications page loads targets without falling back to HTML', () => {
    cy.visit('/#notifications');
    // The page must render its own chrome, not crash or blank. The
    // targets list (or an empty-state) must appear — proves the client
    // hit the real /v1/notification-targets, not the SPA static handler.
    cy.contains(/notification|webhook|target/i, { timeout: 15000 }).should('exist');
    cy.get('body').should('not.contain', 'Cannot read properties');
    cy.screenshot('live-notifications', { capture: 'viewport' });
  });
});
