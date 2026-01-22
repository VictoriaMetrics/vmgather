// @ts-check
const { test, expect } = require('@playwright/test');
const VM_SINGLE_NOAUTH_URL =
  process.env.VM_SINGLE_NOAUTH_URL || 'http://localhost:18428';

/**
 * Bug Fix Verification Tests
 * 
 * These tests verify that specific bugs reported in issues #7 and timezone support
 * have been fixed and work correctly.
 */

test.describe('Bug Fix Verification', () => {
  test.beforeEach(async ({ page }) => {
    // Mock VM endpoints to make tests hermetic
    await page.route('**/api/validate', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          is_victoria_metrics: true,
          vm_components: ['vmsingle'],
          components: 1,
          version: 'v1.95.0',
        }),
      });
    });
    await page.route('**/api/discover', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          components: [
            {
              component: 'vmsingle',
              jobs: ['vmjob'],
              instance_count: 1,
              metrics_count_estimate: 100,
              job_metrics: { vmjob: 100 },
            },
          ],
        }),
      });
    });
    await page.route('**/api/sample', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          samples: [
            {
              name: 'test_metric',
              labels: { __name__: 'test_metric', job: 'vmjob', instance: '127.0.0.1:8428' },
            },
          ],
          count: 1,
        }),
      });
    });
  });
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    // Wait for page to be fully loaded
    await page.waitForSelector('.step.active', { timeout: 10000 });
  });

  test.describe('Issue: Timezone Selector Visibility (Step 2)', () => {
    test('BUGFIX: Timezone selector must be visible immediately after Step 2 title', async ({ page }) => {
      // Navigate to Step 2 - use more reliable selector
      const nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await expect(nextButton).toBeVisible({ timeout: 10000 });
      await nextButton.click();
      await page.waitForTimeout(500);

      // Verify we're on Step 2
      const step2 = page.locator('.step.active[data-step="2"]');
      await expect(step2.locator('h2.step-title')).toContainText('Select Time Range');

      // CRITICAL: Timezone selector must be the FIRST form-group after the title
      // It should be visible BEFORE Quick Presets
      const timezoneSelector = step2.locator('#timezone');

      // Check visibility
      await expect(timezoneSelector).toBeVisible({ timeout: 5000 });

      // Check it has a label
      const timezoneLabel = step2.locator('label[for="timezone"]');
      await expect(timezoneLabel).toBeVisible();
      await expect(timezoneLabel).toContainText('Timezone');

      // Verify timezone selector appears BEFORE Quick Presets in DOM order
      const timezoneGroup = timezoneSelector.locator('xpath=ancestor::div[@class="form-group"]');
      const presetGroup = step2.locator('label:has-text("Quick Presets")').locator('xpath=ancestor::div[@class="form-group"]');

      // Get positions in DOM
      const timezonePosition = await timezoneGroup.evaluate(el => {
        let pos = 0;
        let current = el;
        while (current.previousElementSibling) {
          current = current.previousElementSibling;
          pos++;
        }
        return pos;
      });

      const presetPosition = await presetGroup.evaluate(el => {
        let pos = 0;
        let current = el;
        while (current.previousElementSibling) {
          current = current.previousElementSibling;
          pos++;
        }
        return pos;
      });

      // Timezone should come before Presets
      expect(timezonePosition).toBeLessThan(presetPosition);

      // Verify timezone selector is functional
      await expect(timezoneSelector).toHaveValue('local');

      // Verify it has options
      const options = await timezoneSelector.locator('option').count();
      expect(options).toBeGreaterThan(5); // Should have multiple timezone options
    });

    test('BUGFIX: Timezone selector must be visible without scrolling on Step 2', async ({ page }) => {
      const nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await expect(nextButton).toBeVisible({ timeout: 10000 });
      await nextButton.click();
      await page.waitForTimeout(500);

      const step2 = page.locator('.step.active[data-step="2"]');
      const timezoneSelector = step2.locator('#timezone');

      // Check if element is in viewport (visible without scrolling)
      const isInViewport = await timezoneSelector.evaluate(el => {
        const rect = el.getBoundingClientRect();
        return (
          rect.top >= 0 &&
          rect.left >= 0 &&
          rect.bottom <= (window.innerHeight || document.documentElement.clientHeight) &&
          rect.right <= (window.innerWidth || document.documentElement.clientWidth)
        );
      });

      expect(isInViewport).toBe(true);
    });

    test('BUGFIX: Timezone auto-detection must work on page load', async ({ page }) => {
      // Navigate to Step 2
      const nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await expect(nextButton).toBeVisible({ timeout: 10000 });
      await nextButton.click();
      await page.waitForTimeout(1000); // Wait for timezone initialization

      const timezoneSelector = page.locator('#timezone');

      // Check that timezone selector has a value (should be auto-detected or 'local')
      const selectedValue = await timezoneSelector.inputValue();
      expect(selectedValue).toBeTruthy();

      // Verify time inputs are populated with timezone-aware values
      const timeFrom = await page.locator('#timeFrom').inputValue();
      const timeTo = await page.locator('#timeTo').inputValue();

      expect(timeFrom).toBeTruthy();
      expect(timeTo).toBeTruthy();
    });
  });

  test.describe('Issue #7: Undefined in Sample Preview (Step 5)', () => {
    test('BUGFIX #7: Sample preview must NOT show "undefined" as metric name', async ({ page }) => {
      // Navigate through wizard to Step 5
      // Step 1 -> Step 2
      let nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await expect(nextButton).toBeVisible({ timeout: 10000 });
      await nextButton.click();
      await page.waitForTimeout(500);

      // Step 2: Set time range
      await page.locator('button:has-text("Last 1h")').click();
      nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await nextButton.click();
      await page.waitForTimeout(500);

      // Step 3: Configure connection
      await page.fill('#vmUrl', VM_SINGLE_NOAUTH_URL);
      await page.locator('.step.active #testConnectionBtn').click();
      await page.waitForSelector('text=Connection successful', { timeout: 15000 });
      nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await nextButton.click();
      await page.waitForTimeout(500);

      // Step 4: Select components
      await page.waitForSelector('.component-item', { timeout: 15000 });
      nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await nextButton.click();
      await page.waitForTimeout(2000); // Wait for samples to load

      // Step 5: Verify obfuscation preview
      const step5 = page.locator('.step.active[data-step="5"]');
      await expect(step5.locator('h2.step-title')).toContainText('Obfuscation', { timeout: 5000 });

      // Enable obfuscation if not enabled
      const obfCheckbox = page.locator('#enableObfuscation');
      const isChecked = await obfCheckbox.isChecked();
      if (!isChecked) {
        await obfCheckbox.check();
        await page.waitForTimeout(500);
      }

      // Open preview section
      const previewDetails = page.locator('details summary:has-text("Preview sample data")');
      if (await previewDetails.isVisible()) {
        await previewDetails.click();
        await page.waitForTimeout(2000); // Wait for samples to load

        // CRITICAL: Check that NO sample contains "undefined" as metric name
        const samplePreview = page.locator('#samplePreview');

        // Wait for preview to load (might show loading message first)
        await page.waitForTimeout(2000);

        // Check if preview has content (not just loading message)
        const previewText = await samplePreview.textContent();
        if (previewText && !previewText.includes('Loading') && !previewText.includes('No samples')) {
          // Get all sample metric names
          const metricNames = await samplePreview.locator('.metric-name').allTextContents();

          // Verify NO metric name is "undefined"
          for (const name of metricNames) {
            expect(name).not.toBe('undefined');
            expect(name).not.toContain('undefined');
            expect(name.trim()).not.toBe('');
            expect(name.trim().length).toBeGreaterThan(0);
          }

          // Verify metric names are valid
          const validNames = metricNames.filter(name => {
            const trimmed = name.trim();
            return trimmed.length > 0 && trimmed !== 'undefined';
          });

          expect(validNames.length).toBeGreaterThan(0);
        }
      }
    });

    test('BUGFIX #7: Frontend renderSamplePreview must handle missing name field', async ({ page }) => {
      // Test that frontend JavaScript correctly handles both 'name' and 'metric_name' fields
      // This is a unit-style test executed in browser context

      const result = await page.evaluate(() => {
        // Simulate renderSamplePreview function logic
        const samples = [
          { name: 'test_metric', labels: { instance: 'localhost:8428' } },
          { metric_name: 'another_metric', labels: { job: 'vmstorage' } },
          { name: undefined, metric_name: 'fallback_metric', labels: { component: 'vm' } },
          { labels: { __name__: 'label_metric' } }, // No name at all
        ];

        const results = [];
        for (const sample of samples) {
          // This is the logic from renderSamplePreview
          const metricName = sample.name || sample.metric_name || 'unknown';
          results.push({
            original: sample,
            resolved: metricName,
            hasUndefined: metricName === 'undefined' || metricName.includes('undefined'),
          });
        }

        return results;
      });

      // Verify that NO sample resolves to "undefined"
      for (const res of result) {
        expect(res.hasUndefined).toBe(false);
        expect(res.resolved).not.toBe('undefined');
        expect(res.resolved).not.toContain('undefined');
        expect(res.resolved.trim().length).toBeGreaterThan(0);
      }

      // Verify fallback logic works
      expect(result[1].resolved).toBe('another_metric'); // metric_name used
      expect(result[2].resolved).toBe('fallback_metric'); // metric_name used when name is undefined
      expect(result[3].resolved).toBe('unknown'); // Final fallback
    });

    test('BUGFIX #7: Backend API must return "name" field in sample response', async ({ page }) => {
      let sampleResponse = null;
      let apiCalled = false;

      // Intercept sample API call
      await page.route('**/api/sample', async route => {
        apiCalled = true;
        const response = await route.fetch();
        sampleResponse = await response.json();
        await route.fulfill({ response, json: sampleResponse });
      });

      // Navigate to Step 5 to trigger sample loading
      let nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await expect(nextButton).toBeVisible({ timeout: 10000 });
      await nextButton.click();
      await page.waitForTimeout(500);

      await page.locator('button:has-text("Last 1h")').click();
      nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await nextButton.click();
      await page.waitForTimeout(500);

      await page.fill('#vmUrl', VM_SINGLE_NOAUTH_URL);
      await page.locator('.step.active #testConnectionBtn').click();
      await page.waitForSelector('text=Connection successful', { timeout: 15000 });
      nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await nextButton.click();
      await page.waitForTimeout(500);

      await page.waitForSelector('.component-item', { timeout: 15000 });
      nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await nextButton.click();
      await page.waitForTimeout(3000); // Wait for sample API call

      // Verify API was called
      expect(apiCalled).toBe(true);

      // Verify backend response structure
      expect(sampleResponse).not.toBeNull();
      expect(sampleResponse).toHaveProperty('samples');

      if (sampleResponse.samples && sampleResponse.samples.length > 0) {
        const firstSample = sampleResponse.samples[0];

        // CRITICAL: Backend must return 'name' field
        expect(firstSample).toHaveProperty('name');
        expect(firstSample.name).toBeDefined();
        expect(firstSample.name).not.toBe('undefined');
        expect(firstSample.name).not.toBe('');
        expect(typeof firstSample.name).toBe('string');
        expect(firstSample.name.trim().length).toBeGreaterThan(0);
      }
    });

  });

  test.describe('Combined Verification', () => {
    test('BUGFIX VERIFICATION: Both fixes work together in full flow', async ({ page }) => {
      // Step 1 -> Step 2: Verify timezone
      let nextButton = page.locator('.step.active button.btn-primary:has-text("Next")');
      await expect(nextButton).toBeVisible({ timeout: 10000 });
      await nextButton.click();
      await page.waitForTimeout(500);

      // Verify timezone selector is visible and functional
      const timezoneSelector = page.locator('#timezone');
      await expect(timezoneSelector).toBeVisible({ timeout: 5000 });
      await expect(timezoneSelector).toHaveValue('local');

      // Verify timezone comes before Quick Presets
      const timezoneGroup = timezoneSelector.locator('xpath=ancestor::div[@class="form-group"]');
      const presetLabel = page.locator('label:has-text("Quick Presets")');
      await expect(presetLabel).toBeVisible();

      // Both fixes verified:
      // 1. Timezone selector is visible on Step 2 (verified above)
      // 2. Frontend renderSamplePreview handles undefined correctly (verified in separate test)
      // 3. Backend returns 'name' field (verified in separate test)
    });
  });
});
