// Flow: camera lifecycle (SP1 CRUD + SP2 discovery/adopt/health/caps).
describe('flow: cameras', () => {
  let token: string;
  beforeEach(() => {
    cy.apiToken().then((t) => (token = t));
  });

  it('creates a camera, sets credentials with no plaintext leak, reads health, deletes with cascade', () => {
    const name = `e2e_cam_${Date.now()}`;
    let id = '';

    cy.api('POST', '/v1/cameras', token, {
      name,
      source_type: 'rtsp',
      source_url: 'rtsp://192.0.2.50:554/stream',
      enabled: true,
    }).then((r) => {
      expect(r.status, 'create').to.eq(201);
      id = (r.body as { id: string }).id;
      expect(id).to.not.be.empty;
    });

    // Credentials round-trip; response must never echo the plaintext.
    cy.then(() =>
      cy.api('PUT', `/v1/cameras/${id}/credentials`, token, {
        rtsp_username: 'alice',
        rtsp_password: 'sup3r-secret-pw',
      }),
    ).then((r) => {
      expect(r.status, 'set creds').to.eq(200);
      expect(JSON.stringify(r.body)).to.not.contain('sup3r-secret-pw');
      expect((r.body as { password_set: boolean }).password_set).to.eq(true);
    });

    // Health endpoint returns a state (unknown until the poller sees it).
    cy.then(() => cy.api('GET', `/v1/cameras/${id}/health`, token)).then((r) => {
      expect(r.status).to.eq(200);
      expect(r.body).to.have.property('rtsp_state');
    });

    // The camera GET must not leak plaintext either.
    cy.then(() => cy.api('GET', `/v1/cameras/${id}`, token)).then((r) => {
      expect(JSON.stringify(r.body)).to.not.contain('sup3r-secret-pw');
    });

    // Delete cascades; the row and its credentials are gone.
    cy.then(() => cy.api('DELETE', `/v1/cameras/${id}`, token)).then((r) => {
      expect(r.status).to.eq(200);
    });
    cy.then(() => cy.api('GET', `/v1/cameras/${id}`, token)).then((r) => {
      expect(r.status).to.eq(404);
    });
  });

  it('event_channel patches and persists', () => {
    const name = `e2e_ch_${Date.now()}`;
    let id = '';
    cy.api('POST', '/v1/cameras', token, {
      name,
      source_type: 'rtsp',
      source_url: 'rtsp://192.0.2.51:554/stream',
    }).then((r) => {
      id = (r.body as { id: string }).id;
    });
    cy.then(() => cy.api('PATCH', `/v1/cameras/${id}`, token, { event_channel: 'onvif' })).then((r) => {
      expect(r.status).to.eq(200);
    });
    cy.then(() => cy.api('GET', `/v1/cameras/${id}`, token)).then((r) => {
      expect((r.body as { event_channel: string }).event_channel).to.eq('onvif');
    });
    // Reject garbage.
    cy.then(() => cy.api('PATCH', `/v1/cameras/${id}`, token, { event_channel: 'pigeon' })).then((r) => {
      expect(r.status).to.eq(400);
    });
    cy.then(() => cy.api('DELETE', `/v1/cameras/${id}`, token));
  });

  it('discovery lists LAN cameras and rejects adopt with a bad ref (404)', () => {
    cy.api('GET', '/v1/discovery/cameras', token).then((r) => {
      expect(r.status).to.eq(200);
      const items = (r.body as { items: unknown[] }).items;
      expect(items.length, 'discovered cameras on the LAN').to.be.greaterThan(0);
    });
    cy.api('POST', '/v1/discovery/adopt', token, {
      endpoint_reference: 'urn:uuid:does-not-exist',
      name: 'ghost',
      rtsp_username: 'a',
      rtsp_password: 'b',
    }).then((r) => {
      expect(r.status).to.eq(404);
    });
  });

  it('has managed cameras with capability reports (from acceptance adopts)', () => {
    cy.api('GET', '/v1/cameras', token).then((r) => {
      expect(r.status).to.eq(200);
      const items = (r.body as { items: Array<{ id: string; name: string }> }).items;
      expect(items.length, 'managed cameras').to.be.greaterThan(0);
      const front = items.find((c) => c.name === 'front_amcrest');
      expect(front, 'front_amcrest present').to.exist;
      if (front) {
        cy.api('GET', `/v1/cameras/${front.id}/capabilities`, token).then((cr) => {
          expect(cr.status).to.eq(200);
          expect(cr.body).to.have.property('selected_profile_token');
        });
      }
    });
  });
});
