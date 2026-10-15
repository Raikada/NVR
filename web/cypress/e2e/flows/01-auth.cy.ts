// Flow: authentication. UI login/logout + API rejection semantics.
describe('flow: auth', () => {
  it('rejects a wrong password', () => {
    cy.request({
      method: 'POST',
      url: '/v1/auth/login',
      body: { username: 'admin', password: 'definitely-wrong' },
      failOnStatusCode: false,
    }).then((r) => {
      expect(r.status).to.be.oneOf([400, 401]);
    });
  });

  it('logs in through the UI and reaches the operator shell, then signs out', () => {
    cy.uiLogin();
    // Authed chrome: the sidebar / header must be present.
    cy.contains(/recording server/i, { timeout: 15000 }).should('exist');
    cy.contains(/sign out/i).should('exist');

    // /v1/auth/me returns the admin principal (regression guard for the
    // getMe /me→/auth/me bug).
    cy.apiToken().then((token) => {
      cy.api('GET', '/v1/auth/me', token).then((r) => {
        expect(r.status).to.eq(200);
        expect((r.body as { user: { role: string } }).user.role).to.eq('admin');
      });
    });

    cy.contains(/sign out/i).click();
    cy.contains(/sign in to continue/i, { timeout: 10000 }).should('exist');
  });
});
