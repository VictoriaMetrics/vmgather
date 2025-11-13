// @ts-check
const { test, expect } = require('@playwright/test');

test.describe('Navigation Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');
  });

  test('should navigate from Step 1 (Welcome) to Step 2 (Time Range)', async ({ page }) => {
    // Verify we're on Step 1
    const step1 = page.locator('.step.active[data-step="1"]');
    await expect(step1.locator('h2.step-title')).toContainText('Welcome to VMExporter');
    await expect(page.locator('.step-info')).toContainText('Step 1 of 6: Welcome');

    // Find and click Next button in active step
    const nextButton = step1.locator('button.btn-primary:has-text("Next")');
    await expect(nextButton).toBeVisible();
    await expect(nextButton).toBeEnabled();
    
    await nextButton.click();
    await page.waitForTimeout(300);
    
    // Verify we're now on Step 2
    const step2 = page.locator('.step.active[data-step="2"]');
    await expect(step2.locator('h2.step-title')).toContainText('Select Time Range');
    await expect(page.locator('.step-info')).toContainText('Step 2 of 6: Time Range');
    
    // Verify Step 1 is no longer active
    await expect(page.locator('.step[data-step="1"]')).not.toHaveClass(/active/);
  });

  test('should have working Back button on Step 2', async ({ page }) => {
    // Navigate to Step 2
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    
    // Verify we're on Step 2
    await expect(page.locator('.step-info')).toContainText('Step 2 of 6');
    
    // Click Back button
    const backButton = page.locator('.step.active button.btn-secondary:has-text("Back")');
    await backButton.click();
    await page.waitForTimeout(300);
    
    // Verify we're back on Step 1
    await expect(page.locator('.step-info')).toContainText('Step 1 of 6: Welcome');
    await expect(page.locator('.step.active h2.step-title')).toContainText('Welcome to VMExporter');
  });

  test('should have disabled Back button on Step 1', async ({ page }) => {
    const backButton = page.locator('.step.active button.btn-secondary:has-text("Back")');
    await expect(backButton).toBeDisabled();
  });

  test('should update progress bar when navigating', async ({ page }) => {
    const progressBar = page.locator('.progress-fill');
    
    // Check initial progress (0%)
    let width = await progressBar.evaluate(el => el.style.width);
    expect(width).toBe('0%');
    
    // Navigate to Step 2
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    
    // Progress should be 20%
    width = await progressBar.evaluate(el => el.style.width);
    expect(width).toBe('20%');
  });

  test('should navigate through all steps without errors', async ({ page }) => {
    // Step 1 → Step 2
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    await expect(page.locator('.step-info')).toContainText('Step 2 of 6');
    
    // Step 2 → Step 3 (time range required)
    await page.locator('#timeFrom').fill('2025-01-01T00:00');
    await page.locator('#timeTo').fill('2025-01-01T01:00');
    
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    await expect(page.locator('.step-info')).toContainText('Step 3 of 6');
  });

  test('should not navigate from Step 2 without valid time range', async ({ page }) => {
    // Navigate to Step 2
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    
    // Clear datetime inputs
    await page.locator('#timeFrom').clear();
    await page.locator('#timeTo').clear();
    
    // Listen for alert
    let dialogShown = false;
    page.on('dialog', async dialog => {
      dialogShown = true;
      expect(dialog.message()).toContain('time');
      await dialog.accept();
    });
    
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(200);
    
    // Should still be on Step 2
    await expect(page.locator('.step-info')).toContainText('Step 2 of 6');
    expect(dialogShown).toBe(true);
  });

  test('preset buttons should work correctly', async ({ page }) => {
    // Navigate to Step 2
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    
    // Click Last 3h preset
    const preset3h = page.locator('button.preset-btn:has-text("Last 3h")');
    await preset3h.click();
    
    // Check that active class is set
    await expect(preset3h).toHaveClass(/active/);
    
    // Check that timeFrom and timeTo are filled
    const timeFrom = await page.locator('#timeFrom').inputValue();
    const timeTo = await page.locator('#timeTo').inputValue();
    
    expect(timeFrom).not.toBe('');
    expect(timeTo).not.toBe('');
    
    // Verify time difference is approximately 3 hours
    const from = new Date(timeFrom);
    const to = new Date(timeTo);
    const diff = (to - from) / (1000 * 60 * 60); // hours
    expect(Math.abs(diff - 3)).toBeLessThan(0.1);
  });
});
