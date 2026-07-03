// cy.loginAsAdmin() — POSTs to /v1/auth/login with the bootstrap admin's
// password (read from a fixture or env var) and stores the JWT.
declare global {
  namespace Cypress {
    interface Chainable {
      loginAsAdmin(): Chainable<void>;
      apiPost(path: string, body: unknown): Chainable<Cypress.Response<unknown>>;
      apiGet(path: string): Chainable<Cypress.Response<unknown>>;
    }
  }
}

Cypress.Commands.add('loginAsAdmin', () => {
  const password = Cypress.env('ADMIN_PASSWORD');
  if (!password) throw new Error('Set CYPRESS_ADMIN_PASSWORD env var to run E2E');
  cy.request({
    method: 'POST',
    url: '/v1/auth/login',
    body: { username: 'admin', password },
    failOnStatusCode: false,
  }).then((r) => {
    expect(r.status).to.eq(200);
    cy.window().then((w) => {
      w.localStorage.setItem('raikada_token', (r.body as { access_token: string }).access_token);
    });
  });
});

Cypress.Commands.add('apiGet', (path: string) => {
  return cy.window().then((w) => {
    const token = w.localStorage.getItem('raikada_token');
    return cy.request({
      method: 'GET',
      url: path,
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      failOnStatusCode: false,
    });
  });
});

Cypress.Commands.add('apiPost', (path: string, body: unknown) => {
  return cy.window().then((w) => {
    const token = w.localStorage.getItem('raikada_token');
    return cy.request({
      method: 'POST',
      url: path,
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      body: body as Cypress.RequestBody,
      failOnStatusCode: false,
    });
  });
});

export {};
