// Faithful live-view check: NO request interception. Logs in, opens
// live view, and asserts a camera's <video> actually advances
// (readyState >= 2 and currentTime > 0) — i.e. HLS authenticated and
// decoded end-to-end exactly as a real browser would.
describe('live view plays', () => {
  it('a camera tile video advances', () => {
    const password = Cypress.env('ADMIN_PASSWORD');
    cy.visit('/');
    cy.get('input').first().clear().type('admin');
    cy.get('input[type="password"]').clear().type(password);
    cy.get('button').contains(/sign in/i).click();
    cy.contains('RAIKADA', { timeout: 10000 }).should('be.visible');
    cy.visit('/#live');
    cy.contains('Live View').should('be.visible');
    cy.get('video', { timeout: 20000 }).first().should(($v) => {
      const el = $v[0] as HTMLVideoElement;
      expect(el.readyState, 'video readyState').to.be.greaterThan(1);
      expect(el.currentTime, 'video currentTime').to.be.greaterThan(0);
    });
  });
});
