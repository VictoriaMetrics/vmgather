// @ts-check
const { defineConfig, devices } = require('@playwright/test');

const defaultBaseUrl = 'http://127.0.0.1:38081';
let baseURL = process.env.VMIMPORTER_URL || defaultBaseUrl;
let webAddr = process.env.VMIMPORTER_ADDR || '';
const defaultWebAddr = '127.0.0.1:38081';

try {
  const parsed = new URL(baseURL);
  if (!webAddr) {
    webAddr = parsed.host;
  }
} catch (_err) {
  baseURL = defaultBaseUrl;
  webAddr = defaultWebAddr;
}

function sanitizeWebAddr(addr) {
  if (typeof addr !== 'string') {
    return defaultWebAddr;
  }
  const trimmed = addr.trim();
  const hostPortPattern = /^(?:\[[0-9a-fA-F:.]+\]|[A-Za-z0-9.-]+):([0-9]{1,5})$/;
  const match = hostPortPattern.exec(trimmed);
  if (!match) {
    return defaultWebAddr;
  }
  const port = Number(match[1]);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return defaultWebAddr;
  }
  return trimmed;
}

const safeWebAddr = sanitizeWebAddr(webAddr || defaultWebAddr);
try {
  const parsed = new URL(baseURL);
  parsed.host = safeWebAddr;
  baseURL = parsed.toString().replace(/\/$/, '');
} catch (_err) {
  baseURL = `http://${safeWebAddr}`;
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
    command: `../../vmimporter -no-browser -addr ${safeWebAddr}`,
    url: baseURL,
    reuseExistingServer: process.env.PW_REUSE_EXISTING_SERVER === '1',
    timeout: 5 * 60 * 1000,
  },
});
