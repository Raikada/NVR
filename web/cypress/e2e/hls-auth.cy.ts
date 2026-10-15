// Focused repro: does the live-view HLS flow (master → child playlists →
// segments) stay authenticated through the proxy in a real browser?
// Captures every /hls/* response code. Run against a scratch recorder
// with a real (or publishing) camera named per CYPRESS_CAM.
describe('hls auth through the proxy', () => {
  it('loads master + child playlists without 401', () => {
    const password = Cypress.env('ADMIN_PASSWORD');
    const cam = Cypress.env('CAM') || 'rc';
    const seen: Record<string, number> = {};

    cy.intercept('GET', '/hls/**', (req) => {
      req.continue((res) => {
        seen[req.url.replace(/^https?:\/\/[^/]+/, '').split('?')[0]] = res.statusCode;
      });
    }).as('hls');

    cy.visit('/');
    cy.get('input').first().clear().type('admin');
    cy.get('input[type="password"]').clear().type(password);
    cy.get('button').contains(/sign in/i).click();
    cy.contains('RAIKADA', { timeout: 10000 }).should('be.visible');

    cy.visit('/#live');
    cy.contains('Live View').should('be.visible');
    // Give hls.js time to walk master → children → first segments.
    cy.wait(8000);

    cy.then(() => {
      // eslint-disable-next-line no-console
      console.table(seen);
      const url401 = Object.entries(seen).filter(([, code]) => code === 401).map(([u]) => u);
      const master = seen[`/hls/${cam}/index.m3u8`];
      expect(master, 'master playlist reachable').to.not.equal(undefined);
      expect(url401, `no /hls 401s; saw: ${url401.join(', ')}`).to.have.length(0);
    });
  });
});
