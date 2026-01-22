import { test, expect } from '@playwright/test';

const VM_SINGLE_NOAUTH_URL =
  process.env.VM_SINGLE_NOAUTH_URL || 'http://localhost:18428';

test.describe('Obfuscation - Default Settings', () => {

  test.beforeEach(async ({ page }) => {
    // Mock all required APIs
    await page.route('/api/validate', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          valid: true,
          is_victoria_metrics: true
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
              jobs: ['vmstorage-prod'],
              instance_count: 3,
              metrics_count_estimate: 1000
            }
          ]
        })
      });
    });
  });

  test('should have obfuscation disabled by default', async ({ page }) => {
    // Navigate to step 5 (Obfuscation)
    await page.goto('/');

    // Step 1: Welcome
    await page.locator('.step.active button:has-text("Next")').click();

    // Step 2: Time Range
    await page.locator('.step.active button:has-text("Next")').click();

    // Step 3: Connection
    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();

    // Step 4: Components
    await page.waitForSelector('.component-item');
    await page.locator('.component-header input[type="checkbox"]').first().click();
    await page.locator('.step.active button:has-text("Next")').click();

    // Step 5: Obfuscation
    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');

    const obfCheckbox = page.locator('#enableObfuscation');

    // Verify obfuscation is NOT checked by default
    await expect(obfCheckbox).not.toBeChecked();

    // Verify options are hidden
    const optionsContainer = page.locator('#obfuscationOptions');
    await expect(optionsContainer).not.toBeVisible();
  });

  test('should enable instance and job obfuscation when enabling obfuscation', async ({ page }) => {
    // Navigate to step 5
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.component-item');
    await page.locator('.component-header input[type="checkbox"]').first().click();
    await page.locator('.step.active button:has-text("Next")').click();

    // Wait for obfuscation step
    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');

    // Enable obfuscation
    await page.locator('#enableObfuscation').click();

    // Verify options are now visible
    await expect(page.locator('#obfuscationOptions')).toBeVisible();

    // Verify instance and job are checked by default
    await expect(page.locator('[data-label="instance"]')).toBeChecked();
    await expect(page.locator('[data-label="job"]')).toBeChecked();
  });

  test('should hide options when disabling obfuscation', async ({ page }) => {
    // Navigate to step 5
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.component-item');
    await page.locator('.component-header input[type="checkbox"]').first().click();
    await page.locator('.step.active button:has-text("Next")').click();

    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');

    // Enable obfuscation
    await page.locator('#enableObfuscation').click();
    await expect(page.locator('#obfuscationOptions')).toBeVisible();

    // Disable obfuscation
    await page.locator('#enableObfuscation').click();

    // Verify options are hidden again
    await expect(page.locator('#obfuscationOptions')).not.toBeVisible();
  });

  test('should show correct text "VictoriaMetrics team" not "support team"', async ({ page }) => {
    // Navigate to step 5
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.component-item');
    await page.locator('.component-header input[type="checkbox"]').first().click();
    await page.locator('.step.active button:has-text("Next")').click();

    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');

    // Verify correct text
    const infoBox = await page.locator('.step.active .info-box').textContent();
    expect(infoBox).toContain('VictoriaMetrics team');
    expect(infoBox).not.toContain('support team');
  });

  test('should show correct text in welcome step', async ({ page }) => {
    await page.goto('/');

    // Verify welcome step has correct text
    const welcomeInfoBox = await page.locator('.step.active .info-box').textContent();
    expect(welcomeInfoBox).toContain('VictoriaMetrics team');
    expect(welcomeInfoBox).not.toContain('support team');
  });
});


