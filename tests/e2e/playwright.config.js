// @ts-check
const { defineConfig, devices } = require('@playwright/test');

/**
 * vmgather E2E Test Configuration
 * @see https://playwright.dev/docs/test-configuration
 */
const defaultBaseUrl = 'http://localhost:8080';
let baseURL = process.env.VMGATHER_URL || defaultBaseUrl;
let webAddr = process.env.VMGATHER_ADDR || '';
try {
  const parsed = new URL(baseURL);
  if (!webAddr) {
    webAddr = parsed.host;
  }
} catch (err) {
  baseURL = defaultBaseUrl;
  webAddr = 'localhost:8080';
}
module.exports = defineConfig({
  testDir: './specs',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [
    ['html', { outputFolder: 'playwright-report', open: 'never' }],
    ['json', { outputFile: 'test-results/results.json' }],
    ['list'],
  ],
  
  use: {
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  // Start local dev server before tests
  webServer: {
    command: `../../vmgather -no-browser -addr ${webAddr || 'localhost:8080'}`,
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120 * 1000,
  },
});

