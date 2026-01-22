const { test, expect } = require('@playwright/test');

test.describe('VMSelect standalone - real environment', () => {
  test.skip(process.env.E2E_REAL !== '1', 'Real environment required (E2E_REAL=1)');

  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    const step1 = page.locator('.step.active[data-step="1"]');
    await step1.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);

    const step2 = page.locator('.step.active[data-step="2"]');
    await step2.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
  });

  test('Base vmselect URL should return a helpful hint', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    const vmselectUrl = process.env.VMSELECT_STANDALONE_URL || 'http://localhost:8491';

    await step3.locator('#vmUrl').fill(vmselectUrl);
    await step3.locator('#authType').selectOption('none');

    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);

    const stepsContainer = page.locator('#validationSteps');
    await expect(stepsContainer).toBeVisible();
    await expect(stepsContainer).toContainText('select/');
    await expect(stepsContainer).toContainText('prometheus');
  });

  test('Standalone vmselect with /select/0/prometheus should validate', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    const vmselectUrl = process.env.VMSELECT_STANDALONE_SELECT_TENANT_0 || 'http://localhost:8491/select/0/prometheus';

    await step3.locator('#vmUrl').fill(vmselectUrl);
    await step3.locator('#authType').selectOption('none');

    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);

    const stepsContainer = page.locator('#validationSteps');
    await expect(stepsContainer).toBeVisible();
    await expect(stepsContainer).toContainText('Connection Successful');
  });
});
