// Flow: every SPA route renders without an uncaught runtime error.
// This is the guard against the route/shape-mismatch class that
// compiles clean but crashes at runtime (getMe /me, notifications/*).
const ROUTES: Array<{ hash: string; expect: RegExp }> = [
  { hash: 'overview', expect: /overview|recording server/i },
  { hash: 'cameras', expect: /cameras/i },
  { hash: 'events', expect: /events/i },
  { hash: 'policies', expect: /polic/i },
  { hash: 'storage', expect: /storage/i },
  { hash: 'network', expect: /network/i },
  { hash: 'logs', expect: /log/i },
  { hash: 'diagnostics', expect: /diagnostic/i },
  { hash: 'settings', expect: /setting/i },
  { hash: 'users', expect: /user/i },
  { hash: 'notifications', expect: /notification/i },
];

describe('flow: all pages render', () => {
  beforeEach(() => {
    cy.uiLogin();
    // Any uncaught exception from app code fails the test loudly.
    cy.on('uncaught:exception', (err) => {
      throw err;
    });
  });

  ROUTES.forEach((route) => {
    it(`#${route.hash} renders without crashing`, () => {
      cy.visit(`/#${route.hash}`);
      cy.contains(route.expect, { timeout: 15000 }).should('exist');
      // No React error boundary / crash text.
      cy.get('body').should('not.contain', 'Cannot read properties');
      cy.get('body').should('not.contain', 'Something went wrong');
      // The page did not silently fall through to raw index.html: the
      // operator shell chrome must be present.
      cy.contains(/sign out/i).should('exist');
    });
  });
});
