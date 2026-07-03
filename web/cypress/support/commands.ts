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

// --- Live E2E helpers (drive the real recorder) ---------------------

// apiToken logs in via the API and yields a bearer token. Password
// comes from CYPRESS_ADMIN_PASSWORD.
Cypress.Commands.add('apiToken', () => {
  const password = Cypress.env('ADMIN_PASSWORD');
  if (!password) throw new Error('Set CYPRESS_ADMIN_PASSWORD to run live E2E');
  return cy
    .request({ method: 'POST', url: '/v1/auth/login', body: { username: 'admin', password } })
    .then((r) => {
      expect(r.status, 'login').to.eq(200);
      return (r.body as { access_token: string }).access_token;
    });
});

// api issues an authed request that does NOT fail the test on non-2xx,
// so specs can assert specific status codes.
Cypress.Commands.add(
  'api',
  (method: string, path: string, token: string, body?: unknown) =>
    cy.request({
      method,
      url: path,
      headers: { Authorization: `Bearer ${token}` },
      body: body as Cypress.RequestBody,
      failOnStatusCode: false,
    }),
);

// uiLogin drives the real login form (used by render specs).
Cypress.Commands.add('uiLogin', () => {
  cy.visit('/');
  cy.get('input').first().clear().type('admin');
  cy.get('input[type="password"]').type(Cypress.env('ADMIN_PASSWORD'), { log: false });
  cy.get('button').contains(/sign in/i).click();
  cy.contains(/sign in to continue/i, { timeout: 15000 }).should('not.exist');
});

declare global {
  namespace Cypress {
    interface Chainable {
      apiToken(): Chainable<string>;
      api(method: string, path: string, token: string, body?: unknown): Chainable<Cypress.Response<unknown>>;
      uiLogin(): Chainable<void>;
    }
  }
}
