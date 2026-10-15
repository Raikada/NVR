// Verifies the first-run setup wizard appears when setup_required=true
// and walks through to a populated dashboard. Assumes a fresh recorder
// instance; the helper script `scripts/cypress-reset.sh` (TODO Phase 8.6
// — for now operator runs manually) wipes the identity dir and restarts
// the binary before this spec runs.

describe('First-run setup wizard', () => {
  it('shows wizard when setup_required is true', () => {
    cy.apiGet('/v1/system/setup-status').then((r) => {
      expect(r.status).to.eq(200);
      const body = r.body as { setup_required: boolean };
      if (!body.setup_required) {
        cy.log('Recorder already set up; this spec requires a fresh state. Skipping.');
        return;
      }
      cy.visit('/');
      cy.findByText(/initial admin password|first-run|setup wizard/i, { timeout: 10000 }).should('exist');
    });
  });

  it('completes the wizard end-to-end', function () {
    cy.apiGet('/v1/system/setup-status').then((r) => {
      const body = r.body as { setup_required: boolean };
      if (!body.setup_required) {
        this.skip();
      }
    });
    cy.visit('/');
    // Step 1: enter the printed initial password
    cy.findByLabelText(/initial password/i).type(Cypress.env('INITIAL_ADMIN_PASSWORD') ?? Cypress.env('ADMIN_PASSWORD'));
    cy.findByRole('button', { name: /continue|next/i }).click();
    // Step 2: change password
    const newPassword = Cypress.env('ADMIN_PASSWORD');
    cy.findByLabelText(/new password/i).type(newPassword);
    cy.findByLabelText(/confirm password/i).type(newPassword);
    cy.findByRole('button', { name: /continue|next/i }).click();
    // Step 3: site name + timezone
    cy.findByLabelText(/site name/i).clear().type('E2E Test Site');
    cy.findByRole('button', { name: /continue|next/i }).click();
    // Step 4: skip optional TLS + SMTP
    cy.findByRole('button', { name: /skip|next/i }).click();
    cy.findByRole('button', { name: /skip|finish|done/i }).click();
    // Should land on the dashboard
    cy.url().should('not.include', '/setup');
    cy.findByText(/overview|dashboard/i, { timeout: 10000 }).should('exist');
  });
});
