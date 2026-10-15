describe('hls auth through the proxy', () => {
  it('loads master + child playlists without 401', () => {
    const password = Cypress.env('ADMIN_PASSWORD');
    const log: Array<{ url: string; auth: boolean; code: number }> = [];
    cy.intercept('GET', '/hls/**', (req) => {
      const auth = !!req.headers['authorization'];
      req.continue((res) => {
        log.push({ url: req.url.replace(/^https?:\/\/[^/]+/, '').split('?')[0], auth, code: res.statusCode });
      });
    });
    cy.visit('/');
    cy.get('input').first().clear().type('admin');
    cy.get('input[type="password"]').clear().type(password);
    cy.get('button').contains(/sign in/i).click();
    cy.contains('RAIKADA', { timeout: 10000 }).should('be.visible');
    cy.visit('/#live');
    cy.contains('Live View').should('be.visible');
    cy.wait(10000);
    cy.then(() => {
      const bad = log.filter((e) => e.code === 401);
      // eslint-disable-next-line no-console
      bad.forEach((e) => console.log(`401 ${e.url} authHeader=${e.auth}`));
      // eslint-disable-next-line no-console
      console.log(`total /hls reqs: ${log.length}, 401s: ${bad.length}, child reqs: ${log.filter((e)=>e.url.includes('_stream')).length}`);
      expect(bad, `401s: ${bad.map((e)=>e.url+'(auth='+e.auth+')').join(' ')}`).to.have.length(0);
    });
  });
});
