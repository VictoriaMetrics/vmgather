const { test, expect } = require('@playwright/test');

async function navigateToStep3(page) {
  await page.goto('/');
  await page.waitForLoadState('networkidle');

  const step1 = page.locator('.step[data-step="1"]');
  await page.evaluate(() => window.rebuildStepSequence && window.rebuildStepSequence());
  await step1.locator('button[data-next="true"]').click();
  await page.evaluate(() => window.nextStep && window.nextStep());
  await page.evaluate(() => window.showStepByIndex && window.showStepByIndex(1, true));
  await page.waitForSelector('.step[data-step="2"].active');
  const step2 = page.locator('.step[data-step="2"].active');
  await step2.locator('button[data-next="true"]').click();
  await page.evaluate(() => window.nextStep && window.nextStep());
  await page.evaluate(() => window.showStepByIndex && window.showStepByIndex(2, true));

  return page.locator('.step[data-step="3"]');
}

test.describe('UI regressions', () => {
  test('Bug #2: help section is collapsed by default', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    await expect(step3.locator('.help-section')).toHaveJSProperty('open', false);
  });

  test('Help section can be opened and closed', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    const details = step3.locator('.help-section');
    await details.locator('summary').click();
    await expect(details).toHaveJSProperty('open', true);
    await details.locator('summary').click();
    await expect(details).toHaveJSProperty('open', false);
  });

  test('Step 3 Next stays disabled until connection test', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    const nextWrapper = step3.locator('#step3NextWrapper');
    const nextBtn = step3.locator('#step3Next');
    await expect(nextBtn).toBeDisabled();
    await nextWrapper.hover();
    await expect(step3.locator('#step3NextTooltip')).toBeVisible();
  });

  test('Bug #4: invalid URL disables Test Connection button', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    await step3.locator('#vmUrl').fill('https://this-aint-no\\\\invalid-url');
    await expect(step3.locator('#vmUrlHint')).toHaveText(/[FAIL]/);
    await expect(step3.locator('#testConnectionBtn')).toBeDisabled();
  });

  test('Cluster selection error references Step 5', async ({ page }) => {
    const step3 = await navigateToStep3(page);

    await step3.locator('#vmUrl').fill('http://localhost:8428');
    await page.route('/api/validate', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          is_victoria_metrics: true,
          version: 'v1.95.0',
          components: 1,
          vm_components: ['vmsingle'],
        }),
      });
    });
    await step3.locator('#testConnectionBtn').click();
    await page.waitForSelector('#step3Next:enabled');
    await page.locator('#step3Next').click();

    await page.waitForSelector('.step[data-step="5"].active');
    await page.route('/api/sample', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ samples: [] }),
      });
    });

    await page.evaluate(() => {
      window.autoSelectAllComponents = () => {};
    });
    await page.evaluate(() => window.loadSampleMetrics && window.loadSampleMetrics());

    await expect.poll(async () => {
      return await page.evaluate(() => window.__lastSampleError || '');
    }).toContain('Step 5');
  });

  test('Bug #4: Kubernetes service URL is accepted', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    await step3.locator('#vmUrl').fill('vmselect.monitoring.svc.cluster.local:8481');
    await expect(step3.locator('#vmUrlHint')).toHaveText(/[OK]/);
    await expect(step3.locator('#testConnectionBtn')).toBeEnabled();
  });

  test('Obfuscation summary and samples show sanitized data', async ({ page }) => {
    await page.route('**/api/validate', route => {
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          success: true,
          is_victoria_metrics: true,
          vm_components: ['vmselect'],
          components: 1,
          version: 'v1.95.1',
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
              component: 'vmselect',
              jobs: ['vmselect-0'],
              instance_count: 1,
              metrics_count_estimate: 42,
              job_metrics: { 'vmselect-0': 42 },
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
                instance: '777.777.1.1:8080',
                job: 'vm_component_vmselect_1',
              },
            },
          ],
          count: 1,
        }),
      });
    });

    await page.route('**/api/export/start', route => {
      const payload = route.request().postDataJSON();
      expect(payload.staging_dir).toBe('/tmp/ui-regression');
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          job_id: 'job-ui-regression',
          total_batches: 2,
          batch_window_seconds: 30,
          staging_path: '/tmp/ui-regression',
        }),
      });
    });

    let pollCount = 0;
    await page.route('**/api/export/status**', route => {
      pollCount += 1;
      const running = pollCount < 2;
      const body = {
        job_id: 'job-ui-regression',
        state: running ? 'running' : 'completed',
        total_batches: 2,
        completed_batches: running ? 1 : 2,
        progress: running ? 0.5 : 1,
        metrics_processed: running ? 50 : 100,
        batch_window_seconds: 30,
        average_batch_seconds: running ? 28 : 30,
        last_batch_duration_seconds: 28,
        staging_path: '/tmp/ui-regression',
      };
      if (!running) {
        body.result = {
          export_id: 'test',
          archive_path: '/tmp/export.zip',
          archive_size: 1024,
          metrics_count: 100,
          sha256: 'abc123',
          obfuscation_applied: true,
          sample_data: [
            {
              name: 'go_mem',
              labels: {
                instance: '777.777.1.1:8080',
                job: 'vm_component_vmselect_1',
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

    const step3 = await navigateToStep3(page);
    await step3.locator('#vmUrl').fill('http://vmselect:8481');
    await step3.locator('#testConnectionBtn').click();
    await expect(page.locator('#step3Next')).toBeEnabled();
    await page.locator('#step3Next').click();

    const step4 = page.locator('.step[data-step="5"]');
    await expect(step4.locator('.component-item')).toHaveCount(1);
    await step4.locator('.component-item').click();
    await step4.locator('button.btn-primary').click();

    const step5 = page.locator('.step[data-step="6"]');
    await expect(step5.locator('#selectionSummary')).toContainText('42 series');
    await expect(step5.locator('#samplePreview')).toContainText('777.777.1.1');
    await step5.locator('#stagingDir').fill('/tmp/ui-regression');
    await step5.locator('#metricStep').selectOption('60');

    await step5.locator('#startExportBtn').click();
    await page.evaluate(() => window.exportMetrics && window.exportMetrics(document.getElementById('startExportBtn')));
    await page.waitForSelector('.step[data-step="7"].active');
    await expect(page.locator('#exportSpoilers')).toContainText('777.777.1.1:8080');
  });
});
