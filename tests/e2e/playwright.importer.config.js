// @ts-check
const { defineConfig, devices } = require('@playwright/test');

const defaultBaseUrl = 'http://127.0.0.1:38081';
let baseURL = process.env.VMIMPORTER_URL || defaultBaseUrl;
let webAddr = process.env.VMIMPORTER_ADDR || '';

try {
  const parsed = new URL(baseURL);
  if (!webAddr) {
    webAddr = parsed.host;
  }
} catch (_err) {
  baseURL = defaultBaseUrl;
  webAddr = '127.0.0.1:38081';
}

module.exports = defineConfig({
  testDir: './importer-specs',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [
    ['html', { outputFolder: 'playwright-report-importer', open: 'never' }],
    ['json', { outputFile: 'test-results/importer-results.json' }],
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
  webServer: {
    command: `../../vmimporter -no-browser -addr ${webAddr || '127.0.0.1:38081'}`,
    url: baseURL,
    reuseExistingServer: process.env.PW_REUSE_EXISTING_SERVER === '1',
    timeout: 5 * 60 * 1000,
  },
});
