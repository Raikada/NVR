import { defineConfig } from 'cypress';

export default defineConfig({
  e2e: {
    baseUrl: 'https://localhost:9997',
    chromeWebSecurity: false,
    supportFile: 'cypress/support/e2e.ts',
    specPattern: 'cypress/e2e/**/*.cy.ts',
    setupNodeEvents(on, config) {
      return config;
    },
  },
});
