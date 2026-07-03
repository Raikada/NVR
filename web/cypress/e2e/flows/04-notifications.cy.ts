// Flow: notification targets + subscriptions + webhook delivery (SP1).
describe('flow: notifications', () => {
  let token: string;
  beforeEach(() => {
    cy.apiToken().then((t) => (token = t));
  });

  it('creates a webhook target, a subscription, fires a test delivery, then cleans up', () => {
    let targetId = '';
    let subId = '';

    cy.api('POST', '/v1/notification-targets', token, {
      kind: 'webhook',
      name: `e2e-target-${Date.now()}`,
      webhook_url: 'http://127.0.0.1:8899/hook',
      webhook_secret: 'e2e-secret',
      enabled: true,
    }).then((r) => {
      expect(r.status, 'create target').to.eq(201);
      const body = r.body as { id: string; webhook_secret_set: boolean };
      targetId = body.id;
      expect(body.webhook_secret_set, 'secret stored').to.eq(true);
      // The plaintext secret must never come back.
      expect(JSON.stringify(r.body)).to.not.contain('e2e-secret');
    });

    cy.then(() =>
      cy.api('POST', '/v1/notification-subscriptions', token, {
        target_id: targetId,
        event_type_id: 'motion',
      }),
    ).then((r) => {
      expect(r.status, 'create subscription').to.eq(201);
      subId = (r.body as { id: string }).id;
    });

    // Test delivery. The local sink (127.0.0.1:8899) may or may not be
    // running; either a 200 or a connection-error status is a valid
    // "the dispatch machinery ran" outcome — what must NOT happen is a
    // 4xx/5xx from our own API.
    cy.then(() => cy.api('POST', `/v1/notification-targets/${targetId}/test`, token, {})).then((r) => {
      expect(r.status, 'test endpoint reachable').to.eq(200);
      // status field is the downstream HTTP code (200 if sink is up, 0 on connect error).
      expect(r.body).to.have.property('status');
    });

    // Subscription appears in the list.
    cy.then(() => cy.api('GET', '/v1/notification-subscriptions', token)).then((r) => {
      const ids = (r.body as { items: Array<{ id: string }> }).items.map((s) => s.id);
      expect(ids).to.include(subId);
    });

    // Cleanup.
    cy.then(() => cy.api('DELETE', `/v1/notification-subscriptions/${subId}`, token)).then((r) =>
      expect(r.status).to.be.oneOf([200, 204]),
    );
    cy.then(() => cy.api('DELETE', `/v1/notification-targets/${targetId}`, token)).then((r) =>
      expect(r.status).to.be.oneOf([200, 204]),
    );
  });
});
