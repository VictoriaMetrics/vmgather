const { test, expect } = require('@playwright/test');

test.describe('Multi-Stage Connection Validation - Real Environment', () => {
  test.beforeEach(async ({ page }) => {
    // Mock VM endpoints to keep tests hermetic
    await page.route('**/api/validate', async route => {
      const body = route.request().postDataJSON?.() || {};
      const conn = body.connection || body;
      const vmUrl = conn.url || conn.vm_url || '';
      const auth = conn.auth || {};

      if (vmUrl.includes('nonexistent-host')) {
        return route.fulfill({
          status: 502,
          contentType: 'application/json',
          body: JSON.stringify({
            success: false,
            error: 'Host unreachable: DNS lookup failed',
          }),
        });
      }

      if (auth.type === 'basic' && auth.username === 'wrong-user') {
        return route.fulfill({
          status: 401,
          contentType: 'application/json',
          body: JSON.stringify({
            success: false,
            error: 'Unauthorized',
          }),
        });
      }

      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          valid: true,
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
              name: 'up',
              labels: { __name__: 'up', job: 'vmjob', instance: '127.0.0.1:8428' },
            },
          ],
          count: 1,
        }),
      });
    });

    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');
    
    // Navigate to Step 3 (Connection)
    const step1 = page.locator('.step.active[data-step="1"]');
    await step1.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
    
    const step2 = page.locator('.step.active[data-step="2"]');
    await step2.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(300);
  });

  test('VMSingle No Auth - should show all validation steps', async ({ page }) => {
    const consoleLogs = [];
    page.on('console', msg => {
      consoleLogs.push(msg.text());
    });

    const step3 = page.locator('.step.active[data-step="3"]');
    
    // Fill connection details
    await step3.locator('#vmUrl').fill('http://localhost:8428');
    await step3.locator('#authType').selectOption('none');
    
    // Click test connection
    await step3.locator('#testConnectionBtn').click();
    
    // Wait for validation
    await page.waitForTimeout(4000);
    
    // Check validation steps container exists
    const stepsContainer = page.locator('#validationSteps');
    await expect(stepsContainer).toBeVisible();
    
    // Check for success summary
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible({ timeout: 10000 });
    
    // Verify console logs
    const hasMultiStageLog = consoleLogs.some(log => 
      log.includes('Multi-Stage Connection Test')
    );
    expect(hasMultiStageLog).toBeTruthy();
    
    // Verify URL was parsed
    const hasUrlParsed = consoleLogs.some(log => 
      log.includes('URL parsed') || log.includes('localhost:8428')
    );
    expect(hasUrlParsed).toBeTruthy();
  });

  test('VMSingle via VMAuth Basic - should validate and detect VM', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    
    await step3.locator('#vmUrl').fill('http://localhost:8427');
    await step3.locator('#authType').selectOption('basic');
    await page.waitForTimeout(200);
    await step3.locator('#username').fill('monitoring-read');
    await step3.locator('#password').fill('secret-password-123');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);
    
    const stepsContainer = page.locator('#validationSteps');
    await expect(stepsContainer).toBeVisible();
    
    // Should show VictoriaMetrics detected
    const vmDetected = stepsContainer.locator('text=/VictoriaMetrics detected/');
    await expect(vmDetected).toBeVisible({ timeout: 10000 });
    
    // Should show success
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible();
  });

  test('VMSingle via VMAuth Bearer - should validate with token', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    
    await step3.locator('#vmUrl').fill('http://localhost:8427');
    await step3.locator('#authType').selectOption('bearer');
    await page.waitForTimeout(200);
    await step3.locator('#token').fill('test-bearer-token-789');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);
    
    const stepsContainer = page.locator('#validationSteps');
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible({ timeout: 10000 });
  });

  test('VM Cluster - should parse and validate', async ({ page }) => {
    const consoleLogs = [];
    page.on('console', msg => {
      consoleLogs.push(msg.text());
    });

    const step3 = page.locator('.step.active[data-step="3"]');
    
    // VM Cluster requires tenant ID in URL
    await step3.locator('#vmUrl').fill('http://localhost:8481/select/0/prometheus');
    await step3.locator('#authType').selectOption('none');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);
    
    const stepsContainer = page.locator('#validationSteps');
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible({ timeout: 10000 });
    
    // Verify URL parsing in logs
    const hasUrlLog = consoleLogs.some(log => 
      log.includes('localhost:8481')
    );
    expect(hasUrlLog).toBeTruthy();
  });

  test('VM Cluster with Tenant ID - should parse tenant and validate', async ({ page }) => {
    const consoleLogs = [];
    page.on('console', msg => {
      consoleLogs.push(msg.text());
    });

    const step3 = page.locator('.step.active[data-step="3"]');
    
    // URL with tenant ID
    await step3.locator('#vmUrl').fill('http://localhost:8481/select/0/prometheus');
    await step3.locator('#authType').selectOption('none');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);
    
    // Should show success
    const stepsContainer = page.locator('#validationSteps');
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible({ timeout: 10000 });
    
    // Verify API base path in URL (tenant path is part of URL)
    const hasPathLog = consoleLogs.some(log => 
      log.includes('/select/0/prometheus') || log.includes('localhost:8481')
    );
    expect(hasPathLog).toBeTruthy();
  });

  test('VM Cluster via VMAuth - should validate with basic auth', async ({ page }) => {
    // Skip this test - VMAuth cluster not configured in test env
    test.skip();
  });

  test('Invalid host - should show error message', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    
    await step3.locator('#vmUrl').fill('http://nonexistent-host-xyz:8428');
    await step3.locator('#authType').selectOption('none');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(8000); // Wait for network timeout
    
    // Should show error in connection result
    const result = page.locator('#connectionResult');
    await expect(result).toBeVisible({ timeout: 10000 });
    
    // Should contain error text
    const text = await result.textContent();
    expect(text).toMatch(/Failed|Error|failed|error/i);
  });

  test('Wrong credentials - should show auth error', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    
    await step3.locator('#vmUrl').fill('http://localhost:8427');
    await step3.locator('#authType').selectOption('basic');
    await page.waitForTimeout(200);
    await step3.locator('#username').fill('wrong-user');
    await step3.locator('#password').fill('wrong-pass');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(6000);
    
    // Should show error in connection result
    const result = page.locator('#connectionResult');
    await expect(result).toBeVisible({ timeout: 10000 });
    
    // Should contain auth error (401/403)
    const text = await result.textContent();
    expect(text).toMatch(/401|403|Unauthorized|Forbidden|Failed|Error/i);
  });

  test('All steps should appear progressively', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    
    await step3.locator('#vmUrl').fill('http://localhost:8428');
    await step3.locator('#authType').selectOption('none');
    
    await step3.locator('#testConnectionBtn').click();
    
    // Check steps appear one by one
    await page.waitForTimeout(200);
    const stepsContainer = page.locator('#validationSteps');
    await expect(stepsContainer).toBeVisible();
    
    // Step 1 should appear quickly
    await page.waitForTimeout(400);
    let stepCount = await stepsContainer.locator('> div').count();
    expect(stepCount).toBeGreaterThanOrEqual(1);
    
    // More steps should appear
    await page.waitForTimeout(800);
    stepCount = await stepsContainer.locator('> div').count();
    expect(stepCount).toBeGreaterThanOrEqual(2);
    
    // Final result
    await page.waitForTimeout(2000);
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible();
  });

  test('Success should show VM components info', async ({ page }) => {
    const step3 = page.locator('.step.active[data-step="3"]');
    
    await step3.locator('#vmUrl').fill('http://localhost:8428');
    await step3.locator('#authType').selectOption('none');
    
    await step3.locator('#testConnectionBtn').click();
    await page.waitForTimeout(4000);
    
    const stepsContainer = page.locator('#validationSteps');
    const successBox = stepsContainer.locator('text=/Connection Successful/');
    await expect(successBox).toBeVisible({ timeout: 10000 });
    
    // Should show version
    await expect(stepsContainer).toContainText('Version:');
    
    // Should show components count
    await expect(stepsContainer).toContainText('Components:');
  });
});
