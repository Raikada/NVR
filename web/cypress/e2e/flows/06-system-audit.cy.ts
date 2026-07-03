// Flow: system settings, retention sweep, recording policies, audit log.
describe('flow: system, policies, audit', () => {
  let token: string;
  beforeEach(() => {
    cy.apiToken().then((t) => (token = t));
  });

  it('reads and patches system settings', () => {
    cy.api('GET', '/v1/system/settings', token).then((r) => {
      expect(r.status).to.eq(200);
    });
    cy.api('PATCH', '/v1/system/settings', token, { site_name: 'E2E Site' }).then((r) => {
      expect(r.status).to.be.oneOf([200, 204]);
    });
    cy.api('GET', '/v1/system/settings', token).then((r) => {
      expect(JSON.stringify(r.body)).to.contain('E2E Site');
    });
  });

  it('runs a retention sweep', () => {
    cy.api('POST', '/v1/system/retention/sweep', token, {}).then((r) => {
      expect(r.status).to.be.oneOf([200, 202, 204]);
    });
  });

  it('lists recording policies including the seeded default', () => {
    cy.api('GET', '/v1/recording-policies', token).then((r) => {
      expect(r.status).to.eq(200);
      const items = (r.body as { items: Array<{ id: string; name: string }> }).items;
      expect(items.length, 'at least the default policy').to.be.greaterThan(0);
    });
  });

  it('audit log records mutations and leaks no secrets', () => {
    // Provoke an auditable mutation first.
    const name = `audit_probe_${Date.now()}`;
    let camId = '';
    cy.api('POST', '/v1/cameras', token, {
      name,
      source_type: 'rtsp',
      source_url: 'rtsp://192.0.2.77/s',
    }).then((r) => {
      camId = (r.body as { id: string }).id;
    });
    cy.then(() =>
      cy.api('PUT', `/v1/cameras/${camId}/credentials`, token, {
        rtsp_username: 'audituser',
        rtsp_password: 'audit-secret-xyz',
      }),
    );

    cy.then(() => cy.api('GET', '/v1/audit?limit=50', token)).then((r) => {
      expect(r.status).to.eq(200);
      const raw = JSON.stringify(r.body);
      expect(raw, 'no plaintext password in audit').to.not.contain('audit-secret-xyz');
      const items = (r.body as { items: Array<{ action: string }> }).items;
      const actions = items.map((a) => a.action);
      expect(actions, 'credential rotation audited').to.include('camera.credentials_rotated');
    });

    cy.then(() => cy.api('DELETE', `/v1/cameras/${camId}`, token));
  });
});
