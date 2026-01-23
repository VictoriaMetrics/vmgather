// @ts-check
const { test, expect } = require('@playwright/test');

const VM_SINGLE_NOAUTH_URL =
  process.env.VM_SINGLE_NOAUTH_URL || 'http://localhost:18428';

function mockValidate(page) {
  return page.route('/api/validate', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        is_victoria_metrics: true,
        version: 'v1.95.0',
        components: 3,
        vm_components: ['vmselect', 'vmstorage'],
      }),
    });
  });
}

test.describe('Custom mode flows', () => {
  test('selector-based flow uses job filters', async ({ page }) => {
    let exportPayload;

    await mockValidate(page);
    await page.route('/api/validate-query', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          valid: true,
          result_count: 3,
          has_job: true,
          has_instance: true,
          sample_labels: ['job', 'instance'],
          query_type: 'selector',
        }),
      });
    });

    await page.route('/api/discover-selector', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          jobs: [
            { job: 'job-a', instance_count: 2, metrics_count_estimate: 120 },
            { job: 'job-b', instance_count: 1, metrics_count_estimate: 45 },
          ],
        }),
      });
    });

    await page.route('/api/sample', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          samples: [
            {
              name: 'vm_app_version',
              labels: { job: 'job-a', instance: 'vm-1', env: 'test' },
            },
          ],
        }),
      });
    });

    await page.route('/api/export/start', async route => {
      exportPayload = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          job_id: 'job-123',
          state: 'running',
          total_batches: 1,
          batch_window_seconds: 60,
          staging_path: '/tmp/custom-selector.partial.jsonl',
          obfuscation_enabled: false,
        }),
      });
    });

    await page.route(/\/api\/export\/status.*/, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          job_id: 'job-123',
          state: 'completed',
          total_batches: 1,
          completed_batches: 1,
          progress: 1,
          metrics_processed: 165,
          result: {
            export_id: 'exp-1',
            archive_path: '/tmp/custom-selector.zip',
            archive_size: 2048,
            metrics_count: 165,
            sha256: 'abc',
          },
        }),
      });
    });

    await page.goto('/');

    await page.locator('.mode-slider').click();
    await expect(page.locator('body')).toHaveClass(/mode-custom/);
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();

    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])');
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.step.active[data-step="4"]');
    await page.evaluate(() => { window.connectionValid = true; });

    await page.locator('#customQueryInput').fill('{job=~"job-.*"}');
    await page.evaluate(() => { if (window.validateCustomQuery) window.validateCustomQuery(); });
    await expect(page.locator('#customQueryStatus')).toContainText('[OK]');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.waitForSelector('.step.active[data-step="5"]');

    await page.waitForSelector('.selector-job-item');
    await page.locator('.selector-job-item input[data-job="job-b"]').uncheck();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.waitForSelector('.step.active[data-step="6"]');

    await page.locator('#stagingDir').fill('/tmp/custom-selector');
    await page.locator('#dropLabelsSummary').click();
    await page.locator('.drop-label-checkbox[data-label="env"]').check();
    await page.waitForSelector('#startExportBtn:enabled');
    await page.evaluate(() => window.exportMetrics && window.exportMetrics(document.getElementById('startExportBtn')));

    const exportErrorSelector = await page.evaluate(() => window.__lastExportError);
    expect(exportErrorSelector).toBeFalsy();
    await expect.poll(() => exportPayload).toBeTruthy();
    expect(exportPayload.mode).toBe('custom');
    expect(exportPayload.query_type).toBe('selector');
    expect(exportPayload.query).toBe('{job=~"job-.*"}');
    expect(exportPayload.jobs).toEqual(['job-a']);
    expect(exportPayload.obfuscation.drop_labels).toContain('env');
  });

  test('metricsql flow skips target selection', async ({ page }) => {
    let exportPayload;

    await mockValidate(page);
    await page.route('/api/validate-query', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          valid: true,
          result_count: 1,
          query_type: 'metricsql',
        }),
      });
    });

    await page.route('/api/sample', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          samples: [
            {
              labels: { job: 'vmstorage', instance: 'vm-1' },
            },
          ],
        }),
      });
    });

    await page.route('/api/export/start', async route => {
      exportPayload = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          job_id: 'job-456',
          state: 'running',
          total_batches: 1,
          batch_window_seconds: 60,
          staging_path: '/tmp/custom-query.partial.jsonl',
          obfuscation_enabled: false,
        }),
      });
    });

    await page.route(/\/api\/export\/status.*/, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          job_id: 'job-456',
          state: 'completed',
          total_batches: 1,
          completed_batches: 1,
          progress: 1,
          metrics_processed: 12,
          result: {
            export_id: 'exp-2',
            archive_path: '/tmp/custom-query.zip',
            archive_size: 1024,
            metrics_count: 12,
            sha256: 'def',
          },
        }),
      });
    });

    await page.goto('/');
    await page.locator('.mode-slider').click();
    await expect(page.locator('body')).toHaveClass(/mode-custom/);
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();

    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])');
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.step.active[data-step="4"]');
    await page.evaluate(() => { window.connectionValid = true; });

    await page.locator('#customQueryInput').fill('rate(vm_rows_inserted_total[5m])');
    await page.evaluate(() => { if (window.validateCustomQuery) window.validateCustomQuery(); });
    await expect(page.locator('#customQueryStatus')).toContainText('[OK]');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.waitForSelector('.step.active[data-step="6"]');

    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');
    await expect(page.locator('.metric-note')).toContainText('Metric name removed by aggregation');
    await page.locator('#stagingDir').fill('/tmp/custom-query');
    await page.waitForSelector('#startExportBtn:enabled');
    await page.evaluate(() => window.exportMetrics && window.exportMetrics(document.getElementById('startExportBtn')));

    const exportErrorMetricsql = await page.evaluate(() => window.__lastExportError);
    expect(exportErrorMetricsql).toBeFalsy();
    await expect.poll(() => exportPayload).toBeTruthy();
    expect(exportPayload.mode).toBe('custom');
    expect(exportPayload.query_type).toBe('metricsql');
    expect(exportPayload.query).toBe('rate(vm_rows_inserted_total[5m])');
    expect(exportPayload.jobs).toEqual([]);
  });
});
