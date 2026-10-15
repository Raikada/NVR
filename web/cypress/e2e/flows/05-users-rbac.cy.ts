// Flow: user management + RBAC enforcement (SP1).
describe('flow: users + RBAC', () => {
  let token: string;
  beforeEach(() => {
    cy.apiToken().then((t) => (token = t));
  });

  it('creates a viewer, the viewer is denied a write, then removes the viewer', () => {
    const username = `viewer_${Date.now()}`;
    const password = 'Viewer-Pass-12345';
    let userId = '';

    cy.api('POST', '/v1/users', token, {
      username,
      password,
      role: 'viewer',
      display_name: 'E2E Viewer',
    }).then((r) => {
      expect(r.status, 'create user').to.eq(201);
      userId = (r.body as { id: string }).id;
    });

    // Log in as the viewer and confirm RBAC denies a camera create.
    cy.request({
      method: 'POST',
      url: '/v1/auth/login',
      body: { username, password },
      failOnStatusCode: false,
    }).then((lr) => {
      expect(lr.status, 'viewer login').to.eq(200);
      const viewerToken = (lr.body as { access_token: string }).access_token;

      cy.api('GET', '/v1/auth/me', viewerToken).then((me) => {
        expect((me.body as { user: { role: string } }).user.role).to.eq('viewer');
      });

      // Viewer may read cameras...
      cy.api('GET', '/v1/cameras', viewerToken).then((cr) => {
        expect(cr.status, 'viewer can list').to.eq(200);
      });
      // ...but not create one (RBAC fail-closed → 403).
      cy.api('POST', '/v1/cameras', viewerToken, {
        name: `viewer_should_not_${Date.now()}`,
        source_type: 'rtsp',
        source_url: 'rtsp://192.0.2.9/s',
      }).then((cr) => {
        expect(cr.status, 'viewer denied create').to.eq(403);
      });
    });

    cy.then(() => cy.api('DELETE', `/v1/users/${userId}`, token)).then((r) =>
      expect(r.status).to.be.oneOf([200, 204]),
    );
  });
});
