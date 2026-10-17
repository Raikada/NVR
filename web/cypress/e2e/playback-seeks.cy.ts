// Playback engine check (MSE): login → #playback → recorded footage
// actually decodes and advances, and a Go-to seek lands and resumes
// quickly. No request interception — this drives the embedded bundle
// against the real recorder + playback proxy end-to-end, exactly as a
// browser would. Needs a camera with ≥90s of recorded footage (the
// e2e flow suite's ffmpeg test publisher qualifies).
describe('playback engine', () => {
  const pad = (n: number) => String(n).padStart(2, '0');

  it('plays recorded footage and recovers from a seek fast', () => {
    const password = Cypress.env('ADMIN_PASSWORD');
    cy.visit('/');
    cy.get('input').first().clear().type('admin');
    cy.get('input[type="password"]').clear().type(password);
    cy.get('button').contains(/sign in/i).click();
    cy.contains('RAIKADA', { timeout: 10000 }).should('be.visible');

    cy.visit('/#playback');
    cy.contains('Playback').should('be.visible');
    // Pick the live test camera explicitly (the dev store carries
    // other offline cameras) — first match is the camera-panel row.
    cy.contains('testcam2', { timeout: 10000 }).click();

    // Opening position (45min ago) predates the test footage, so this
    // also exercises gap auto-skip into the first recorded range. The
    // engine video must reach decodable state and advance.
    cy.get('video', { timeout: 30000 }).first().should(($v) => {
      const el = $v[0] as HTMLVideoElement;
      expect(el.readyState, 'readyState').to.be.greaterThan(1);
    });
    cy.get('video').first().then(($v) => {
      const t0 = ($v[0] as HTMLVideoElement).currentTime;
      cy.wait(3500);
      cy.get('video').first().should(($v2) => {
        const el = $v2[0] as HTMLVideoElement;
        expect(el.currentTime, 'currentTime advances').to.be.greaterThan(t0 + 1.5);
      });
    });

    // Seek via the Go-to modal to ~1 minute ago, then the video must be
    // decodable and advancing again within seconds.
    const target = new Date(Date.now() - 60_000);
    cy.get('body').type('g');
    cy.contains('Go to time').should('be.visible');
    cy.get('input[type="date"]')
      .clear()
      .type(`${target.getFullYear()}-${pad(target.getMonth() + 1)}-${pad(target.getDate())}`);
    cy.get('input[type="time"]').clear().type(`${pad(target.getHours())}:${pad(target.getMinutes())}`);
    cy.get('button').contains(/^Go$/).click();

    cy.get('video', { timeout: 15000 }).first().should(($v) => {
      const el = $v[0] as HTMLVideoElement;
      expect(el.readyState, 'post-seek readyState').to.be.greaterThan(1);
    });
    cy.get('video').first().then(($v) => {
      const t0 = ($v[0] as HTMLVideoElement).currentTime;
      cy.wait(3500);
      cy.get('video').first().should(($v2) => {
        const el = $v2[0] as HTMLVideoElement;
        expect(el.currentTime, 'post-seek currentTime advances').to.be.greaterThan(t0 + 1.5);
      });
    });
  });
});
