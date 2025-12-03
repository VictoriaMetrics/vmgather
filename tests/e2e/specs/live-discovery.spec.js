const { test, expect } = require('@playwright/test');

const LIVE_VM_URL = process.env.LIVE_VM_URL || 'http://localhost:18428';

test.describe('Live Discovery (real VM endpoint)', () => {
  test.skip(() => !process.env.LIVE_VM_URL, 'LIVE_VM_URL not set; skipping live discovery');

  test('should discover components on real VM endpoint', async ({ page }) => {
    await page.goto('http://localhost:8080');
    await page.getByRole('button', { name: 'Next' }).click(); // step 1 -> step 2
    await page.getByRole('button', { name: 'Next' }).click(); // step 2 -> step 3
    const urlInput = page.locator('#vmUrl');
    await urlInput.fill(LIVE_VM_URL);
    await page.getByRole('button', { name: 'Test Connection' }).click();
    await expect(page.getByText('Connection Successful', { exact: false }).first()).toBeVisible({ timeout: 15000 });
    await page.getByRole('button', { name: 'Next' }).click(); // to discovery step
    await expect(page.getByText('Discovery failed').first()).not.toBeVisible({ timeout: 15000 });
  });
});
