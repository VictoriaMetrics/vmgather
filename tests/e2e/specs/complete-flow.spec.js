// @ts-check
const { test, expect } = require('@playwright/test');

test.describe('Complete E2E Flow Tests', () => {
  test.beforeEach(async ({ page }) => {
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
    
    // Enable Next button for testing (skip actual connection test)
    await page.evaluate(() => {
      document.getElementById('step3Next').disabled = false;
      connectionValid = true; // Mark connection as valid
    });
    
    await activeStep.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(500);
    
    // ============ STEP 4: Select Components ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 4 of 6: Select Components');
    
    // Inject mock components
    await page.evaluate(() => {
      const list = document.getElementById('componentsList');
      list.style.display = 'block';
      list.innerHTML = `
        <div class="component-item selected">
          <div class="component-header">
            <input type="checkbox" data-component="vmstorage" checked>
            <strong>vmstorage</strong>
          </div>
          <div class="component-details">Jobs: vmstorage-prod | Instances: 3</div>
        </div>
        <div class="component-item selected">
          <div class="component-header">
            <input type="checkbox" data-component="vmselect" checked>
            <strong>vmselect</strong>
          </div>
          <div class="component-details">Jobs: vmselect-prod | Instances: 2</div>
        </div>
      `;
      document.getElementById('componentsLoading').style.display = 'none';
    });
    
    await page.waitForTimeout(200);
    
    // Verify components are visible and selected
    await expect(page.locator('.component-item')).toHaveCount(2);
    await expect(page.locator('.component-item input[type="checkbox"]:checked')).toHaveCount(2);
    
    await activeStep.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    
    // ============ STEP 5: Obfuscation ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 5 of 6: Obfuscation');
    
    // Verify obfuscation is enabled by default
    const obfCheckbox = page.locator('#enableObfuscation');
    await expect(obfCheckbox).toBeChecked();
    
    // Verify obfuscation options visible
    await expect(page.locator('#obfuscationOptions')).toBeVisible();
    
    // Check default checkboxes
    await expect(page.locator('.obf-label-checkbox[data-label="instance"]')).toBeChecked();
    await expect(page.locator('.obf-label-checkbox[data-label="job"]')).toBeChecked();
    
    // Test disabling/enabling
    await obfCheckbox.uncheck();
    await expect(page.locator('#obfuscationOptions')).toBeHidden();
    await obfCheckbox.check();
    await expect(page.locator('#obfuscationOptions')).toBeVisible();
    
    // Mock export
    await page.evaluate(() => {
      window.exportMetrics = async function(btn) {
        setTimeout(() => {
          document.getElementById('exportId').textContent = 'export-1699728000';
          document.getElementById('metricsCount').textContent = '1,566';
          document.getElementById('archiveSize').textContent = '1000.00';
          document.getElementById('archiveSha256').textContent = 'abc123def456';
          
          const steps = document.querySelectorAll('.step');
          steps[4].classList.remove('active');
          steps[5].classList.add('active');
          
          document.getElementById('progress').style.width = '100%';
          document.getElementById('stepInfo').textContent = 'Step 6 of 6: Complete';
        }, 300);
      };
    });
    
    await activeStep.locator('button.btn-primary:has-text("Start Export")').click();
    await page.waitForTimeout(800);
    
    // ============ STEP 6: Complete ============
    activeStep = page.locator('.step.active');
    await expect(page.locator('.step-info')).toContainText('Step 6 of 6: Complete');
    await expect(activeStep.locator('h2.step-title')).toContainText('Export Complete');
    
    // Verify success box
    await expect(page.locator('.success-box')).toBeVisible();
    await expect(page.locator('.success-icon')).toContainText('✅');
    
    // Verify export details (check that values are populated, not empty placeholders)
    const exportId = await page.locator('#exportId').textContent();
    const metricsCount = await page.locator('#metricsCount').textContent();
    expect(exportId).toBeTruthy();
    expect(exportId).not.toBe('---');
    expect(metricsCount).toBeTruthy();
    expect(metricsCount).not.toBe('---');
    
    // Verify Download button
    await expect(page.locator('button:has-text("Download Archive")')).toBeVisible();
    await expect(page.locator('button:has-text("Start New Export")')).toBeVisible();
  });

  test('should validate time range on Step 2', async ({ page }) => {
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
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
    
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
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
    
    // Verify help section
    await expect(page.locator('details.help-section')).toHaveAttribute('open', '');
    await expect(page.locator('.help-section').getByText('http://vmselect:8481').first()).toBeVisible();
    await expect(page.locator('.help-section').getByText('http://vmsingle:8428').first()).toBeVisible();
    await expect(page.getByText('From Grafana datasource:')).toBeVisible();
    await expect(page.getByText('Flexible URL Parser')).toBeVisible();
  });

  test('should verify obfuscation uses 777.777.x.x IP pool', async ({ page }) => {
    // Navigate to Step 5
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(200);
    
    await page.locator('#timeFrom').fill('2025-01-01T00:00');
    await page.locator('#timeTo').fill('2025-01-01T01:00');
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(200);
    
    await page.evaluate(() => {
      document.getElementById('step3Next').disabled = false;
      connectionValid = true; // Mark connection as valid
    });
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(500);
    
    await page.evaluate(() => {
      const list = document.getElementById('componentsList');
      list.style.display = 'block';
      list.innerHTML = '<div class="component-item"><div class="component-header"><input type="checkbox" checked data-component="test"><strong>test</strong></div></div>';
      document.getElementById('componentsLoading').style.display = 'none';
    });
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(300);
    
    // Check for 777.777.x.x mention on active step
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
    
    // Check header gradient
    const header = page.locator('header');
    const headerBg = await header.evaluate(el => window.getComputedStyle(el).background);
    expect(headerBg).toContain('linear-gradient');
    
    // Check rocket emoji
    await expect(page.locator('header h1')).toContainText('🚀');
    
    // Check info box on active step
    const infoBox = page.locator('.step.active .info-box').first();
    await expect(infoBox).toBeVisible();
  });
});
