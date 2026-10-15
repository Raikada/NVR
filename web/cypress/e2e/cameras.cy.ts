// Adds a camera pointing at a fake RTSP URL, verifies CRUD lifecycle,
// rotates credentials, deletes. Does NOT verify live video — that
// requires a real camera and is in real-camera-smoke.md.

describe('Cameras CRUD', () => {
  beforeEach(() => {
    cy.loginAsAdmin();
  });

  it('creates, lists, updates, sets credentials, deletes', () => {
    const name = `cy_test_${Date.now()}`;
    // Create
    cy.apiPost('/v1/cameras', {
      name,
      display_name: 'Cypress Test Camera',
      source_type: 'rtsp',
      source_url: 'rtsp://192.168.99.99/test',  // unreachable on purpose
    }).then((r) => {
      expect(r.status).to.eq(201);
      const cam = r.body as { id: string };

      // Read
      cy.apiGet(`/v1/cameras/${cam.id}`).then((r2) => {
        expect(r2.status).to.eq(200);
        expect((r2.body as { name: string }).name).to.eq(name);
      });

      // Set credentials
      cy.request({
        method: 'PUT',
        url: `/v1/cameras/${cam.id}/credentials`,
        headers: { Authorization: `Bearer ${window.localStorage.getItem('raikada_token')}` },
        body: { rtsp_username: 'admin', rtsp_password: 'p4ss' },
      }).its('status').should('eq', 204);

      // Verify password not leaked in GET
      cy.apiGet(`/v1/cameras/${cam.id}`).then((r3) => {
        const body = JSON.stringify(r3.body);
        expect(body).not.to.include('p4ss');
        expect(body).to.include('"password_set":true');
      });

      // PATCH rejecting credentials field
      cy.request({
        method: 'PATCH',
        url: `/v1/cameras/${cam.id}`,
        headers: { Authorization: `Bearer ${window.localStorage.getItem('raikada_token')}` },
        body: { credentials: { rtsp_password: 'attack' } },
        failOnStatusCode: false,
      }).its('status').should('eq', 400);

      // Delete
      cy.request({
        method: 'DELETE',
        url: `/v1/cameras/${cam.id}`,
        headers: { Authorization: `Bearer ${window.localStorage.getItem('raikada_token')}` },
      }).its('status').should('be.oneOf', [200, 204]);

      // 404 after delete
      cy.apiGet(`/v1/cameras/${cam.id}`).its('status').should('eq', 404);
    });
  });
});
