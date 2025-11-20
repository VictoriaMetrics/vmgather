const { test, expect } = require('@playwright/test');
const fs = require('fs');
const os = require('os');
const path = require('path');

const STAGING_DIR = path.join(os.tmpdir(), 'vmexporter-e2e');
const UNWRITABLE_DIR = path.join(os.tmpdir(), 'vmexporter-readonly');
const CREATE_TARGET_DIR = path.join(STAGING_DIR, 'nested');

test.beforeAll(() => {
  fs.mkdirSync(STAGING_DIR, { recursive: true, mode: 0o755 });
  fs.mkdirSync(UNWRITABLE_DIR, { recursive: true });
  fs.chmodSync(UNWRITABLE_DIR, 0o500);
  fs.rmSync(CREATE_TARGET_DIR, { recursive: true, force: true });
});

test.afterAll(() => {
  try {
    fs.rmSync(STAGING_DIR, { recursive: true, force: true });
    if (fs.existsSync(UNWRITABLE_DIR)) {
      fs.chmodSync(UNWRITABLE_DIR, 0o755);
      fs.rmSync(UNWRITABLE_DIR, { recursive: true, force: true });
    }
  } catch (err) {
    console.warn('cleanup failed', err);
  }
});

test.beforeEach(async ({ page }) => {
  fs.rmSync(CREATE_TARGET_DIR, { recursive: true, force: true });
  await page.route('**/api/fs/check', async route => {
    const body = route.request().postDataJSON();
    const ensure = Boolean(body.ensure);
    let response;
    if (body.path === UNWRITABLE_DIR) {
      response = {
        ok: false,
        abs_path: body.path,
        exists: true,
        can_create: false,
        message: 'permission denied',
      };
    } else if (!fs.existsSync(body.path) && !ensure) {
      response = {
        ok: false,
        abs_path: body.path,
        exists: false,
        can_create: true,
        message: 'Directory does not exist',
      };
    } else {
      if (ensure && !fs.existsSync(body.path)) {
        fs.mkdirSync(body.path, { recursive: true, mode: 0o755 });
      }
      response = {
        ok: true,
        abs_path: body.path,
        exists: true,
        can_create: true,
      };
    }
    page._lastStagingResponse = response;
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(response),
    });
  });

  await page.route('**/api/fs/list', async route => {
    const url = new URL(route.request().url());
    const dirPath = url.searchParams.get('path') || STAGING_DIR;
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        path: dirPath,
        parent: path.dirname(dirPath),
        exists: fs.existsSync(dirPath),
        entries: [],
      }),
    });
  });

  await page.route('**/api/validate', route => {
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        success: true,
        valid: true,
        is_victoria_metrics: true,
        vm_components: ['vmsingle'],
        components: 1,
        version: 'v1.95.0',
      }),
    });
  });

  await page.route('**/api/discover', route => {
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        components: [
          {
            component: 'vmsingle',
            jobs: ['vmjob'],
            instance_count: 1,
            metrics_count_estimate: 100,
            job_metrics: { vmjob: 100 },
          },
        ],
      }),
    });
  });

  await page.route('**/api/sample', route => {
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        samples: [
          {
            name: 'go_mem',
            labels: {
              instance: '777.777.1.1:8428',
              job: 'vm_component_vmjob_1',
            },
          },
        ],
      }),
    });
  });
});

async function goToObfuscationStep(page) {
  await page.goto('http://localhost:8080');
  await page.waitForLoadState('networkidle');
  await page.locator('button.btn-primary:has-text("Next")').click();
  await page.waitForTimeout(200);
  const stepAfterClick = await page.evaluate(() => document.querySelector('.step.active')?.getAttribute('data-step') || null);
  if (stepAfterClick !== '2') {
    await page.evaluate(() => window.nextStep && window.nextStep());
  }
  await page.waitForSelector('.step[data-step="2"].active');
  await page.locator('.step.active button.btn-primary:has-text("Next")').click();
  await page.locator('#vmUrl').fill('http://localhost:8428');
  await page.locator('#testConnectionBtn').click();
  await page.waitForSelector('#step3Next:enabled');
  await page.locator('#step3Next').click();
  await page.waitForSelector('.component-item input[type="checkbox"]');
  await page.locator('.component-item input[type="checkbox"]').first().check();
  await page.locator('.step.active button.btn-primary:has-text("Next")').click();
  await page.waitForSelector('#enableObfuscation');
  await page.fill('#stagingDir', STAGING_DIR);
  await page.locator('#stagingDir').blur();
  await page.evaluate(() => window.validateStagingDir && window.validateStagingDir(true));
  await waitForHintText(page, 'Ready');
  await page.selectOption('#metricStep', '60');
}

test.describe('Export progress UI', () => {
  test('shows progress bar and completes batches', async ({ page }) => {
    await page.route('**/api/export/start', route => {
      const requestBody = route.request().postDataJSON();
      expect(requestBody.staging_dir).toBe(STAGING_DIR);
      expect(requestBody.metric_step_seconds).toBe(60);
      expect(requestBody.batching).toMatchObject({
        strategy: 'custom',
        custom_interval_secs: 60,
      });
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          job_id: 'job-progress-test',
          total_batches: 3,
          batch_window_seconds: 60,
          staging_path: STAGING_DIR,
        }),
      });
    });

    let pollCount = 0;
    await page.route('**/api/export/status**', route => {
      pollCount += 1;
      const done = pollCount > 3;
      const body = {
        job_id: 'job-progress-test',
        state: done ? 'completed' : 'running',
        total_batches: 3,
        completed_batches: done ? 3 : pollCount,
        progress: done ? 1 : pollCount / 3,
        metrics_processed: done ? 90000 : pollCount * 30000,
        batch_window_seconds: 60,
        average_batch_seconds: 28,
        last_batch_duration_seconds: 27,
        staging_path: STAGING_DIR,
      };
      if (done) {
        body.result = {
          export_id: 'job-progress-test',
          archive_path: '/tmp/export.zip',
          archive_size: 2048,
          metrics_count: 90000,
          sha256: 'sha256sum',
          obfuscation_applied: true,
          sample_data: [
            {
              name: 'go_mem',
              labels: {
                instance: '777.777.1.1:8428',
                job: 'vm_component_vmjob_1',
              },
            },
          ],
        };
      }
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      });
    });

    await goToObfuscationStep(page);
    const progressPanel = page.locator('#exportProgressPanel');
    await expect(progressPanel).toHaveClass(/hidden/);

    await page.waitForSelector('.step[data-step="5"].active .button-group button.btn-primary');
    await page.evaluate(() => {
      const btn = document.querySelector('.step[data-step=\"5\"].active .button-group button.btn-primary');
      if (btn && window.exportMetrics) {
        window.exportMetrics(btn);
      }
    });
    await expect(page.locator('#exportProgressPath')).toContainText(STAGING_DIR);
    await expect(page.locator('#exportProgressPercent')).toContainText('0%');
    await expect(page.locator('#exportBatchWindow')).toContainText('≈ 60s');

    await page.waitForSelector('.step[data-step="6"]');
    await expect(page.locator('#exportProgressPercent')).toContainText('100%');
    await expect(page.locator('#exportSpoilers')).toContainText('777.777.1.1:8428');
  });
});

test('shows validation error for unwritable staging directory', async ({ page }) => {
  await goToObfuscationStep(page);
  await page.fill('#stagingDir', UNWRITABLE_DIR);
  await page.locator('#stagingDir').blur();
  await page.evaluate(() => window.validateStagingDir && window.validateStagingDir(true));
  await waitForHintText(page, 'permission denied');
});

test('allows creating a missing staging directory', async ({ page }) => {
  await goToObfuscationStep(page);
  await page.fill('#stagingDir', CREATE_TARGET_DIR);
  await page.locator('#stagingDir').blur();
  await waitForHintText(page, 'Create directory');
  const createButton = page.locator('#createStagingDirBtn');
  await expect(createButton).toBeVisible();
  await createButton.click();
  await waitForHintText(page, 'Ready');
});

test('allows canceling an export job', async ({ page }) => {
  await page.route('**/api/export/start', route => {
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        job_id: 'job-cancel',
        total_batches: 5,
        batch_window_seconds: 60,
        staging_path: STAGING_DIR,
      }),
    });
  });

  let canceledState = false;
  await page.route('**/api/export/status**', route => {
    const body = canceledState
      ? {
          job_id: 'job-cancel',
          state: 'canceled',
          total_batches: 5,
          completed_batches: 2,
          progress: 0.4,
          metrics_processed: 20000,
          batch_window_seconds: 60,
          staging_path: STAGING_DIR,
        }
      : {
          job_id: 'job-cancel',
          state: 'running',
          total_batches: 5,
          completed_batches: 1,
          progress: 0.2,
          metrics_processed: 10000,
          batch_window_seconds: 60,
          staging_path: STAGING_DIR,
        };
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    });
  });

  let cancelCalled = false;
  await page.route('**/api/export/cancel', route => {
    cancelCalled = true;
    canceledState = true;
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ canceled: true, job_id: 'job-cancel' }),
    });
  });

  await goToObfuscationStep(page);
  await page.waitForSelector('.step[data-step="5"].active .button-group button.btn-primary');
  await page.evaluate(() => {
    const btn = document.querySelector('.step[data-step="5"].active .button-group button.btn-primary');
    if (btn && window.exportMetrics) {
      window.exportMetrics(btn);
    }
  });

  await page.waitForSelector('#exportProgressPanel:not(.hidden)');
  const cancelButton = page.locator('#cancelExportBtn');
  await expect(cancelButton).toBeVisible();
  await expect(cancelButton).toBeEnabled();
  await cancelButton.click();

  await expect.poll(() => cancelCalled).toBeTruthy();
  await expect(page.locator('#exportCancelNotice')).toContainText('Export canceled', { timeout: 10000 });
  const startButton = page.locator('.step[data-step="5"].active .button-group button.btn-primary');
  await expect(startButton).toBeEnabled();
});

async function waitForHintText(page, substring) {
  const hint = page.locator('#stagingDirHint');
  await expect(hint).toContainText(substring, { timeout: 10000 });
}
