const { test, expect } = require('@playwright/test');

async function navigateToStep3(page) {
  await page.goto('http://localhost:8080');
  await page.waitForLoadState('networkidle');

  const step1 = page.locator('.step[data-step="1"]');
  await step1.locator('button.btn-primary').click();
  const step2 = page.locator('.step[data-step="2"]');
  await step2.locator('button.btn-primary').click();

  return page.locator('.step[data-step="3"]');
}

test.describe('UI regressions', () => {
  test('Bug #2: help section is collapsed by default', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    await expect(step3.locator('.help-section')).toHaveJSProperty('open', false);
  });

  test('Bug #4: invalid URL disables Test Connection button', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    await step3.locator('#vmUrl').fill('https://this-aint-no\\\\invalid-url');
    await expect(step3.locator('#vmUrlHint')).toHaveText(/❌/);
    await expect(step3.locator('#testConnectionBtn')).toBeDisabled();
  });

  test('Bug #4: Kubernetes service URL is accepted', async ({ page }) => {
    const step3 = await navigateToStep3(page);
    await step3.locator('#vmUrl').fill('vmselect.monitoring.svc.cluster.local:8481');
    await expect(step3.locator('#vmUrlHint')).toHaveText(/✅/);
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

    await page.route('**/api/export', route => {
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
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
        }),
      });
    });

    const step3 = await navigateToStep3(page);
    await step3.locator('#vmUrl').fill('http://vmselect:8481');
    await step3.locator('#testConnectionBtn').click();
    await expect(page.locator('#step3Next')).toBeEnabled();
    await page.locator('#step3Next').click();

    const step4 = page.locator('.step[data-step="4"]');
    await expect(step4.locator('.component-item')).toHaveCount(1);
    await step4.locator('.component-item').click();
    await step4.locator('button.btn-primary').click();

    const step5 = page.locator('.step[data-step="5"]');
    await expect(step5.locator('#selectionSummary')).toContainText('42 series');
    await expect(step5.locator('#samplePreview')).toContainText('777.777.1.1');

    await step5.locator('button.btn-primary:has-text("Start Export")').click();

    const step6 = page.locator('.step[data-step="6"]');
    await expect(step6.locator('#exportSpoilers')).toContainText('777.777.1.1:8080');
  });
});
