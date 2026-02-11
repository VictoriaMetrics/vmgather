// @ts-check
const fs = require('fs');
const path = require('path');
const { defineConfig, devices } = require('@playwright/test');

function loadEnvFileIfExists(filePath) {
  if (!filePath || !fs.existsSync(filePath)) {
    return;
  }
  const content = fs.readFileSync(filePath, 'utf8');
  for (const rawLine of content.split('\n')) {
    let line = rawLine.trim();
    if (!line || line.startsWith('#')) {
      continue;
    }
    if (line.startsWith('export ')) {
      line = line.slice('export '.length).trim();
    }
    const idx = line.indexOf('=');
    if (idx === -1) {
      continue;
    }
    const key = line.slice(0, idx).trim();
    let value = line.slice(idx + 1).trim();
    value = value.replace(/^['"]|['"]$/g, '');
    if (!key || Object.prototype.hasOwnProperty.call(process.env, key)) {
      continue;
    }
    process.env[key] = value;
  }
}

/**
 * vmgather E2E Test Configuration
 * @see https://playwright.dev/docs/test-configuration
 */
loadEnvFileIfExists(
  process.env.VMGATHER_ENV_FILE ||
    path.resolve(__dirname, '../../local-test-env/.env.dynamic')
);
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
    // Default to a fresh server per run to avoid stale binaries / state affecting E2E determinism.
    // Opt-in reuse via `PW_REUSE_EXISTING_SERVER=1`.
    reuseExistingServer: process.env.PW_REUSE_EXISTING_SERVER === '1',
    // Observed: with a 120s timeout, Playwright may terminate the webServer mid-run, leading to
    // intermittent `net::ERR_CONNECTION_REFUSED` failures for later specs. Set this to comfortably
    // exceed the longest expected local E2E run time.
    timeout: 10 * 60 * 1000,
  },
});
