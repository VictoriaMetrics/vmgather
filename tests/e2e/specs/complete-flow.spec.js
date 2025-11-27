// @ts-check
const { test, expect } = require('@playwright/test');

test.describe('Complete E2E Flow Tests', () => {
  test.beforeEach(async ({ page }) => {
    // Mock API endpoints
    await page.route('/api/validate', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          is_victoria_metrics: true,
          version: 'v1.93.0',
          components: 5,
          vm_components: ['vmselect', 'vmstorage']
        })
      });
    });

    await page.route('/api/discover', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          components: [
            {
              component: 'vmstorage',
              instance_count: 3,
              jobs: ['vmstorage-prod'],
              metrics_count_estimate: 100000
            },
            {
              component: 'vmselect',
              instance_count: 2,
              jobs: ['vmselect-prod'],
              metrics_count_estimate: 5000
            }
          ]
        })
      });
    });

    await page.route('/api/export/start', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ job_id: 'job-123' })
      });
    });

    await page.route('/api/export/status*', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          state: 'completed',
          progress: 1.0,
          result: {
            export_id: 'export-123',
            metrics_count: 1566,
            archive_size: 1024000,
            sha256: 'abc123def456',
            archive_path: '/tmp/export.zip'
          }
        })
      });
    });

    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');
  });

  test('should complete full wizard flow with mock data', async ({ page }) => {

    // ============ STEP 1: Welcome ============
    let activeStep = page.locator('.step.active');
    await expect(activeStep.locator('h2.step-title')).toContainText('Welcome to VMExporter');
    await expect(page.locator('.step-info')).toContainText('Step 1 of 6: Welcome');

    await activeStep.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    // ============ STEP 2: Time Range ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 2 of 6: Time Range');

    // Test preset button
    const preset1h = page.locator('button.preset-btn:has-text("Last 1h")');
    await preset1h.click();

    // Verify datetime inputs are filled and have correct type
    const timeFrom = page.locator('#timeFrom');
    const timeTo = page.locator('#timeTo');
    await expect(timeFrom).not.toHaveValue('');
    await expect(timeTo).not.toHaveValue('');
    await expect(timeFrom).toHaveAttribute('type', 'datetime-local');
    await expect(timeTo).toHaveAttribute('type', 'datetime-local');

    // Set custom datetime
    await timeFrom.fill('2025-01-01T00:00');
    await timeTo.fill('2025-01-01T01:00');

    await activeStep.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    // ============ STEP 3: VM Connection ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 3 of 6: VM Connection');

    // Verify help section exists and is open
    await expect(page.locator('details.help-section')).toBeVisible();
    await expect(page.locator('details.help-section')).toHaveAttribute('open', '');

    // Check for URL examples (Format 1: 4 + Format 2: 4 + Format 3: 3 = 11)
    const urlExamples = page.locator('.url-example');
    await expect(urlExamples).toHaveCount(11);

    // Fill VM URL
    await page.locator('#vmUrl').fill('http://localhost:8428');

    // Test authentication options
    await page.locator('#authType').selectOption('none');

    // Click Test Connection (now mocked)
    await page.locator('#testConnectionBtn').click();
    await expect(page.locator('#connectionResult')).toContainText('Connection Successful!');

    // Wait for Next button to be enabled
    await expect(page.locator('#step3Next')).toBeEnabled();

    await activeStep.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(500);

    // ============ STEP 4: Select Components ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 4 of 6: Select Components');

    // Wait for components to load (mocked)
    await expect(page.locator('#componentsList')).toBeVisible();
    await expect(page.locator('.component-item')).toHaveCount(2);

    // Select components
    await page.locator('.component-item input[type="checkbox"]').first().check();

    await activeStep.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    // ============ STEP 5: Obfuscation ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 5 of 6: Obfuscation');

    // Verify obfuscation is disabled by default
    const obfCheckbox = page.locator('#enableObfuscation');
    await expect(obfCheckbox).not.toBeChecked();

    // Enable obfuscation
    await obfCheckbox.check();

    // Verify obfuscation options visible after enabling
    await expect(page.locator('#obfuscationOptions')).toBeVisible();

    // Check default checkboxes
    await expect(page.locator('.obf-label-checkbox[data-label="instance"]')).toBeChecked();
    await expect(page.locator('.obf-label-checkbox[data-label="job"]')).toBeChecked();

    // Test disabling/enabling
    await obfCheckbox.uncheck();
    await expect(page.locator('#obfuscationOptions')).toBeHidden();
    await obfCheckbox.check();
    await expect(page.locator('#obfuscationOptions')).toBeVisible();

    const startExportBtn = page.locator('#startExportBtn');
    await expect(startExportBtn).toBeVisible();
    await expect(startExportBtn).toBeEnabled();
    await startExportBtn.click();

    // Wait for export polling + transition
    await page.waitForTimeout(500);

    // ============ STEP 6: Complete ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 6 of 6: Complete');
    await expect(activeStep.locator('h2.step-title')).toContainText('Export Complete');

    // Verify success box
    await expect(page.locator('.success-box')).toBeVisible();
    await expect(page.locator('.success-icon svg')).toBeVisible();

    // Verify export details (check that values are populated, not empty placeholders)
    const exportIdValue = page.locator('#exportId');
    await expect(exportIdValue).not.toHaveText(/^\s*-\s*$/, { timeout: 10000 });
    await expect(exportIdValue).toContainText('export-123');

    const metricsValue = page.locator('#metricsCount');
    await expect(metricsValue).not.toHaveText(/^\s*-\s*$/, { timeout: 10000 });
    await expect(metricsValue).not.toHaveText(/^\s*0\s*$/);

    // Verify Download button
    await expect(page.locator('button:has-text("Generate ZIP package")')).toBeVisible();
    await expect(page.locator('button:has-text("Start New Export")')).toBeVisible();
  });

  test('should validate time range on Step 2', async ({ page }) => {
  await page.locator('.step.active button:has-text("Next")').first().click();
    await page.waitForTimeout(300);

    // Clear inputs
    await page.locator('#timeFrom').clear();
    await page.locator('#timeTo').clear();

    let dialogShown = false;
    page.on('dialog', async dialog => {
      dialogShown = true;
      expect(dialog.message()).toContain('time');
      await dialog.accept();
    });

  await page.locator('.step.active button:has-text("Next")').first().click();
    await page.waitForTimeout(200);

    await expect(page.locator('.step-info')).toContainText('Step 2 of 6');
    expect(dialogShown).toBe(true);
  });

  test('should validate time range order on Step 2', async ({ page }) => {
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    // End time before start time
    await page.locator('#timeFrom').fill('2025-01-01T12:00');
    await page.locator('#timeTo').fill('2025-01-01T10:00');

    let dialogShown = false;
    page.on('dialog', async dialog => {
      dialogShown = true;
      expect(dialog.message()).toContain('before');
      await dialog.accept();
    });

    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(200);

    await expect(page.locator('.step-info')).toContainText('Step 2 of 6');
    expect(dialogShown).toBe(true);
  });

  test('should test all preset buttons', async ({ page }) => {
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    const presets = [
      { name: 'Last 15min', hours: 0.25 },
      { name: 'Last 1h', hours: 1 },
      { name: 'Last 3h', hours: 3 },
      { name: 'Last 6h', hours: 6 },
      { name: 'Last 12h', hours: 12 },
      { name: 'Last 24h', hours: 24 }
    ];

    for (const preset of presets) {
      const btn = page.locator(`button.preset-btn:has-text("${preset.name}")`);
      await btn.click();
      await page.waitForTimeout(100);

      await expect(btn).toHaveClass(/active/);

      const from = await page.locator('#timeFrom').inputValue();
      const to = await page.locator('#timeTo').inputValue();

      const fromDate = new Date(from);
      const toDate = new Date(to);
      const diffHours = (toDate - fromDate) / (1000 * 60 * 60);

      expect(Math.abs(diffHours - preset.hours)).toBeLessThan(0.1);
    }
  });

  test('should show help documentation for VM URL', async ({ page }) => {
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    await page.locator('#timeFrom').fill('2025-01-01T00:00');
    await page.locator('#timeTo').fill('2025-01-01T01:00');
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    // Verify help section exists (collapsed by default)
    await expect(page.locator('details.help-section')).toBeVisible();

    // Expand help section to verify documentation
    await page.locator('details.help-section summary').click();
    await expect(page.locator('.help-section').getByText('http://vmselect:8481').first()).toBeVisible();
    await expect(page.locator('.help-section').getByText('http://vmsingle:8428').first()).toBeVisible();
    await expect(page.getByText('From Grafana datasource:')).toBeVisible();
    await expect(page.getByText('How it works')).toBeVisible();
  });

  test('should verify obfuscation uses 777.777.x.x IP pool', async ({ page }) => {
    // Navigate to Step 5
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(200);

    await page.locator('#timeFrom').fill('2025-01-01T00:00');
    await page.locator('#timeTo').fill('2025-01-01T01:00');
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(200);

    // Provide valid URL so validation enables the button
    await page.locator('#vmUrl').fill('http://localhost:8428');
    await expect(page.locator('#testConnectionBtn')).toBeEnabled();

    // Click Test Connection (mocked)
    await page.locator('#testConnectionBtn').click();
    await expect(page.locator('#connectionResult')).toContainText('Connection Successful!');

    // Wait for Next button to be enabled
    await expect(page.locator('#step3Next')).toBeEnabled();

    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(500);

    // Wait for components to load (mocked)
    await expect(page.locator('#componentsList')).toBeVisible();
    await expect(page.locator('.component-item')).toHaveCount(2);

    // Select components
    await page.locator('.component-item input[type="checkbox"]').first().check();

    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(300);

    // Enable obfuscation to see options
    await page.locator('#enableObfuscation').check();
    await expect(page.locator('#obfuscationOptions')).toBeVisible();
    await expect(page.locator('.step.active').getByText(/777\.777/).first()).toBeVisible();
  });

  test('should verify progress bar updates correctly', async ({ page }) => {
    const progressBar = page.locator('.progress-fill');

    let width = await progressBar.evaluate(el => el.style.width);
    expect(width).toBe('0%');

    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    width = await progressBar.evaluate(el => el.style.width);
    expect(width).toBe('20%');
  });
});

test.describe('Visual Tests', () => {
  test('should have proper styling on welcome page', async ({ page }) => {
    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');

    // Check header background (should be white/surface now, not gradient)
    const header = page.locator('header');
    const headerBg = await header.evaluate(el => window.getComputedStyle(el).backgroundColor);
    // We expect a solid color (rgb), not a gradient image
    expect(headerBg).not.toContain('linear-gradient');

    // Check for SVG icon instead of emoji
    await expect(page.locator('header svg')).toBeVisible();

    // Check info box on active step
    const infoBox = page.locator('.step.active .info-box').first();
    await expect(infoBox).toBeVisible();
  });
});
