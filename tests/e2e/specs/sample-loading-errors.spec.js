import { test, expect } from '@playwright/test';

const VM_SINGLE_NOAUTH_URL =
  process.env.VM_SINGLE_NOAUTH_URL || 'http://localhost:18428';

test.describe('Sample Loading - Error Handling', () => {

  test.beforeEach(async ({ page }) => {
    // Mock validation and discovery
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

  test('should display error message when sample loading fails', async ({ page }) => {
    // Mock sample endpoint to return error
    await page.route('/api/sample', async route => {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'Internal server error: VM connection timeout'
        })
      });
    });

    // Navigate to step 5
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').first().click();
    await page.locator('.step.active button:has-text("Next")').first().click();
    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.component-item');
    await page.locator('.component-header input[type="checkbox"]').first().click();
    await page.locator('.step.active button:has-text("Next")').first().click();

    // Step 5: Obfuscation - open advanced labels
    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');
    await page.locator('summary:has-text("Advanced: Obfuscate other labels")').click();

    // Wait for error message
    await page.waitForSelector('.error-message', { timeout: 5000 });

    // Verify error is displayed
    const errorText = await page.locator('.error-message').first().textContent();
    expect(errorText).toContain('Failed to load sample metrics');
    expect(errorText).toContain('VM connection timeout');

    // Verify retry button is present
    await expect(page.locator('button:has-text("Retry")')).toBeVisible();
  });

  test('should show loading spinner while loading samples', async ({ page }) => {
    // Mock sample endpoint with delay
    await page.route('/api/sample', async route => {
      await new Promise(resolve => setTimeout(resolve, 1000));
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          samples: [
            {
              name: 'vm_app_uptime_seconds',
              labels: {
                __name__: 'vm_app_uptime_seconds',
                job: 'vmstorage-prod',
                instance: '10.0.1.5:8482'
              },
              value: 86400
            }
          ],
          count: 1
        })
      });
    });

    // Navigate to step 5
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').first().click();
    await page.locator('.step.active button:has-text("Next")').first().click();
    await page.locator('.step.active #vmUrl').fill(VM_SINGLE_NOAUTH_URL);
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    await page.waitForSelector('.component-item');
    await page.locator('.component-header input[type="checkbox"]').first().click();
    await page.locator('.step.active button:has-text("Next")').click();

    await page.waitForSelector('.step.active h2:has-text("Obfuscation")');
    await page.evaluate(() => window.loadSampleMetrics && window.loadSampleMetrics());
    await page.locator('summary:has-text("Advanced: Obfuscate other labels")').click();

    // Verify loading spinner appears
    await expect(page.locator('text=Loading sample metrics')).toBeVisible();

    // Wait for loading to complete
    await page.waitForSelector('.label-item', { timeout: 5000 });
  });

  test('should handle network errors gracefully', async ({ page }) => {
    // Mock sample endpoint to fail with network error
    await page.route('/api/sample', async route => {
      await route.abort('failed');
    });

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
    await page.evaluate(() => window.loadSampleMetrics && window.loadSampleMetrics());
    await page.locator('summary:has-text("Advanced: Obfuscate other labels")').click();

    // Wait for error message
    await page.waitForSelector('.error-message', { timeout: 5000 });

    // Verify error message is shown
    const errorText = await page.locator('.error-message').first().textContent();
    expect(errorText).toContain('Failed to load sample metrics');
  });

  test('should allow retry after error', async ({ page }) => {
    let callCount = 0;

    // Mock sample endpoint to fail first time, succeed second time
    await page.route('/api/sample', async route => {
      callCount++;
    if (callCount <= 2) {
        await route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Temporary error' })
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            samples: [
              {
                name: 'vm_app_uptime_seconds',
                labels: { __name__: 'vm_app_uptime_seconds', job: 'test' },
                value: 100
              }
            ],
            count: 1
          })
        });
      }
    });

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
    await page.locator('summary:has-text("Advanced: Obfuscate other labels")').click();

    // Wait for error
    await page.waitForSelector('.error-message', { timeout: 5000 });

    // Click retry
    await page.locator('button:has-text("Retry")').click();

    // Wait for success - labels should appear
    await page.waitForSelector('.label-item', { timeout: 5000 });

    // Verify no error is shown
    await expect(page.locator('.error-message')).not.toBeVisible();
  });
});
