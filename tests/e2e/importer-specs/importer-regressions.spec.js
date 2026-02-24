import { test, expect } from '@playwright/test';
import http from 'node:http';

async function startMockVM(options = {}) {
  const {
    metricsBody = 'flag{name="retentionPeriod", value="7d", is_set="true"} 1\n',
    importPostStatus = 204,
    importPostBody = '',
    tsdbStatus = 404,
    tsdbBody = '{"status":"success","data":{"retentionTime":"7d"}}',
  } = options;

  const server = http.createServer((req, res) => {
    const url = new URL(req.url || '/', 'http://localhost');
    const pathname = url.pathname;
    if (pathname === '/api/v1/import' || pathname.endsWith('/api/v1/import')) {
      if (req.method === 'HEAD') {
        res.writeHead(204);
        res.end();
        return;
      }
      if (req.method === 'POST') {
        res.writeHead(importPostStatus, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end(importPostBody);
        return;
      }
      res.writeHead(405);
      res.end();
      return;
    }
    if (pathname === '/api/v1/status/tsdb') {
      res.writeHead(tsdbStatus, { 'Content-Type': 'application/json' });
      res.end(tsdbBody);
      return;
    }
    if (pathname === '/metrics') {
      res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end(metricsBody);
      return;
    }
    if (pathname === '/api/v1/series' || pathname.endsWith('/api/v1/series')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end('{"status":"success","data":[{"__name__":"demo"}]}');
      return;
    }
    if (pathname === '/api/v1/query' || pathname.endsWith('/api/v1/query')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end('{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"demo"},"value":[1710000000,"1"]}]}}');
      return;
    }
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('not found');
  });

  await new Promise((resolve, reject) => {
    server.listen(0, '127.0.0.1', err => {
      if (err) {
        reject(err);
        return;
      }
      resolve();
    });
  });

  const address = server.address();
  if (!address || typeof address === 'string') {
    throw new Error('failed to get mock VM address');
  }

  return {
    baseURL: `http://127.0.0.1:${address.port}`,
    async close() {
      await new Promise(resolve => server.close(resolve));
    },
  };
}

function buildJsonlBundle({ lines = 1, extraLabels = 0 }) {
  const out = [];
  const ts = Date.now() - 60_000;
  for (let index = 0; index < lines; index++) {
    const metric = {
      __name__: 'demo_metric',
      job: 'e2e',
      instance: `inst-${index}`,
    };
    for (let labelIndex = 0; labelIndex < extraLabels; labelIndex++) {
      metric[`label_${labelIndex}`] = `v_${labelIndex}`;
    }
    out.push(JSON.stringify({ metric, values: [1], timestamps: [ts + index] }));
  }
  return Buffer.from(`${out.join('\n')}\n`, 'utf8');
}

async function connectImporter(page, vmURL, tenantID) {
  await page.fill('#vmUrl', vmURL);
  await page.check('#enableTenant');
  await page.fill('#tenantId', tenantID);
  await page.click('#testConnectionBtn');
  await expect(page.locator('#connectionResult')).toContainText('Connection successful', { timeout: 15000 });
}

async function getRecentProfiles(page) {
  return page.evaluate(async () => {
    const response = await fetch('/api/profiles/recent');
    const payload = await response.json();
    return Array.isArray(payload.profiles) ? payload.profiles : [];
  });
}

async function purgeRecentProfiles(page) {
  await page.evaluate(async () => {
    const response = await fetch('/api/profiles/recent');
    const payload = await response.json();
    const profiles = Array.isArray(payload.profiles) ? payload.profiles : [];
    for (const profile of profiles) {
      if (!profile.id) {
        continue;
      }
      await fetch(`/api/profiles/recent/${encodeURIComponent(profile.id)}`, { method: 'DELETE' });
    }
  });
}

test.describe('VMImporter regressions', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await purgeRecentProfiles(page);
  });

  test('saves recent profile even when import fails mid-flow', async ({ page }) => {
    const mock = await startMockVM({
      importPostStatus: 502,
      importPostBody: 'bad gateway from mock',
    });
    try {
      const tenantID = `${Date.now()}`;
      await connectImporter(page, mock.baseURL, tenantID);

      await page.setInputFiles('#bundleFile', {
        name: 'failed-import.jsonl',
        mimeType: 'application/jsonl',
        buffer: buildJsonlBundle({ lines: 20, extraLabels: 1 }),
      });
      await expect(page.locator('#analysisDetails')).toBeVisible({ timeout: 15000 });
      await expect(page.locator('#startImportBtn')).toBeEnabled({ timeout: 15000 });
      await page.click('#startImportBtn');

      await expect(page.locator('#statusPanel')).toContainText('502', { timeout: 15000 });
      const profiles = await getRecentProfiles(page);
      const found = profiles.some(profile => profile.endpoint === mock.baseURL && profile.tenant_id === tenantID);
      expect(found).toBeTruthy();
    } finally {
      await mock.close();
    }
  });

  test('updates recent profiles dropdown immediately after successful connection test', async ({ page }) => {
    const mock = await startMockVM();
    try {
      const tenantID = `${Date.now()}`;
      const host = new URL(mock.baseURL).host;
      await connectImporter(page, mock.baseURL, tenantID);

      await expect.poll(async () => page.evaluate(() => {
        const options = Array.from(document.querySelectorAll('#recentProfilesSelect option'));
        return options.map(option => option.textContent || '').join('\n');
      }), { timeout: 15000 }).toContain(host);

      await expect.poll(async () => page.evaluate(() => {
        const options = Array.from(document.querySelectorAll('#recentProfilesSelect option'));
        return options.map(option => option.textContent || '').join('\n');
      }), { timeout: 15000 }).toContain(`tenant ${tenantID}`);
    } finally {
      await mock.close();
    }
  });

  test('uses default maxLabelsPerTimeseries=40 and allows override tuning', async ({ page }) => {
    const mock = await startMockVM({
      metricsBody: 'flag{name="retentionPeriod", value="7d", is_set="true"} 1\n',
    });
    try {
      await connectImporter(page, mock.baseURL, '1042');
      await expect(page.locator('#maxLabelsOverride')).toHaveValue('40');

      await page.setInputFiles('#bundleFile', {
        name: 'high-labels.jsonl',
        mimeType: 'application/jsonl',
        buffer: buildJsonlBundle({ lines: 1, extraLabels: 50 }),
      });

      const details = page.locator('#analysisDetails');
      await expect(details).toBeVisible({ timeout: 15000 });
      await expect(details).toContainText('target maxLabelsPerTimeseries=40 (manual override)');
      await expect(details).toContainText('max seen=');
      await expect(page.locator('#labelManagerSummary')).toContainText('total labels:');
      const labelCheckboxCount = await page.locator('#labelManagerRows input[data-drop-label]').count();
      expect(labelCheckboxCount).toBeGreaterThan(50);
      await expect(page.locator('#maxLabelsRisk')).toContainText('Potential drops detected');
      await expect(page.locator('#maxLabelsRisk')).toContainText('Need at least');

      await page.fill('#maxLabelsOverride', '20');
      await page.click('#fullPreflightBtn');

      await expect(details).toContainText('target maxLabelsPerTimeseries=20 (manual override)', { timeout: 15000 });
      await expect(page.locator('#maxLabelsRisk')).toContainText('Potential drops detected');
      await expect(page.locator('#maxLabelsRisk')).toContainText('Need at least');
      await expect(page.locator('#startImportBtn')).toBeEnabled();
    } finally {
      await mock.close();
    }
  });

  test('default preflight samples 2000 lines and full collection scans all lines', async ({ page }) => {
    const mock = await startMockVM();
    try {
      await connectImporter(page, mock.baseURL, '2042');

      await page.setInputFiles('#bundleFile', {
        name: 'sample-vs-full.jsonl',
        mimeType: 'application/jsonl',
        buffer: buildJsonlBundle({ lines: 2105, extraLabels: 0 }),
      });

      const details = page.locator('#analysisDetails');
      await expect(details).toBeVisible({ timeout: 20000 });
      await expect(details).toContainText('sample (2000 lines scanned');
      await expect(details).toContainText('truncated');

      const fullButton = page.locator('#fullPreflightBtn');
      await expect(fullButton).toBeEnabled();
      await fullButton.click();

      await expect(details).toContainText('full collection (2105 lines scanned)', { timeout: 20000 });
      await expect(details).toContainText('Labels:');
    } finally {
      await mock.close();
    }
  });

  test('shows polling debug error with Retry Status and reveals Resume on retried status check', async ({ page }) => {
    const mock = await startMockVM();
    let pollingMode = 'errors';
    try {
      await connectImporter(page, mock.baseURL, '3042');

      await page.route('**/api/upload', route => {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            job_id: 'job-debug',
            job: {
              id: 'job-debug',
              state: 'running',
              stage: 'importing',
              percent: 5,
              chunks_completed: 1,
              chunks_total: 10,
              updated_at: new Date().toISOString(),
            },
          }),
        });
      });

      await page.route('**/api/import/status**', route => {
        if (pollingMode === 'errors') {
          route.fulfill({
            status: 503,
            contentType: 'text/plain; charset=utf-8',
            body: 'temporary outage',
          });
          return;
        }
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            id: 'job-debug',
            state: 'failed',
            stage: 'failed',
            resume_ready: true,
            error: 'remote responded 502',
            chunks_completed: 12,
            chunks_total: 100,
            updated_at: new Date().toISOString(),
          }),
        });
      });

      await page.setInputFiles('#bundleFile', {
        name: 'polling-debug.jsonl',
        mimeType: 'application/jsonl',
        buffer: buildJsonlBundle({ lines: 10, extraLabels: 1 }),
      });
      await expect(page.locator('#analysisDetails')).toBeVisible({ timeout: 15000 });
      await expect(page.locator('#startImportBtn')).toBeEnabled({ timeout: 15000 });
      await page.click('#startImportBtn');

      await expect(page.locator('#statusPanel')).toContainText('Lost connection to import job', { timeout: 30000 });
      await expect(page.locator('#retryStatusBtn')).toBeVisible({ timeout: 5000 });
      await expect(page.locator('#importProgressEtaDetail')).toContainText('Polling errors:', { timeout: 5000 });

      pollingMode = 'failed-with-resume';
      await page.click('#retryStatusBtn');

      await expect(page.locator('#statusPanel')).toContainText('resume from the saved offset', { timeout: 10000 });
      await expect(page.locator('#resumeImportBtn')).toBeVisible({ timeout: 10000 });
      await expect(page.locator('#importProgressEtaDetail')).toContainText('Job job-debug', { timeout: 10000 });
    } finally {
      await mock.close();
    }
  });
});
