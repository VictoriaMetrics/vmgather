// @ts-check
const { test, expect } = require('@playwright/test');

test.describe('Smoke - VMGather shell', () => {
  test('landing wizard renders primary CTA', async ({ page }) => {
    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');

    const hero = page.locator('header h1');
    await expect(hero).toContainText(/VMGather/i);

    const cta = page.locator('.step.active button.btn-primary');
    await expect(cta).toBeVisible();
    await expect(cta).toHaveText(/Next|Get Started/i);
  });

  test('time range inputs accept manual values', async ({ page }) => {
    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');

    const nextButton = page.locator('.step.active button.btn-primary');
    await nextButton.click();

    const fromInput = page.locator('#timeFrom');
    const toInput = page.locator('#timeTo');
    await expect(fromInput).toBeVisible();
    await expect(toInput).toBeVisible();

    await fromInput.fill('2025-01-01T00:00');
    await toInput.fill('2025-01-01T01:00');

    await expect(fromInput).toHaveValue('2025-01-01T00:00');
    await expect(toInput).toHaveValue('2025-01-01T01:00');
  });
});
