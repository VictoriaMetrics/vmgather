const { test, expect } = require('@playwright/test');

test.describe('Multitenant URL Support', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('http://localhost:8080');
    
    // Navigate to Step 3 (Connection)
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(300);
    
    await page.locator('#timeFrom').fill('2025-01-01T00:00');
    await page.locator('#timeTo').fill('2025-01-01T01:00');
    await page.locator('.step.active button.btn-primary').click();
    await page.waitForTimeout(300);
  });

  test('should parse multitenant URL correctly', async ({ page }) => {
    const multitenantUrl = 'http://vmselect:8481/select/multitenant';
    
    // Fill multitenant URL
    await page.locator('#vmUrl').fill(multitenantUrl);
    
    // Check console logs for parsed URL
    const consoleLogs = [];
    page.on('console', msg => {
      if (msg.type() === 'log') {
        consoleLogs.push(msg.text());
      }
    });
    
    // Select Basic Auth
    await page.locator('#authType').selectOption('basic');
    await page.locator('#username').fill('test-user');
    await page.locator('#password').fill('test-password');
    
    // Trigger test connection (will fail but we check logs)
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForTimeout(1000);
    
    // Verify console logs contain multitenant info
    const configLog = consoleLogs.find(log => log.includes('Connection Config'));
    expect(configLog).toBeTruthy();
    
    // Check for multitenant flag in logs
    const hasMultitenantLog = consoleLogs.some(log => 
      log.includes('is_multitenant') && log.includes('true')
    );
    expect(hasMultitenantLog).toBeTruthy();
  });

  test('should parse tenant ID URL correctly', async ({ page }) => {
    const tenantUrl = 'https://vm.example.com/1011';
    
    // Fill tenant ID URL
    await page.locator('#vmUrl').fill(tenantUrl);
    
    // Check console logs
    const consoleLogs = [];
    page.on('console', msg => {
      if (msg.type() === 'log') {
        consoleLogs.push(msg.text());
      }
    });
    
    // Select Basic Auth
    await page.locator('#authType').selectOption('basic');
    await page.locator('#username').fill('test-user');
    await page.locator('#password').fill('test-password');
    
    // Trigger test connection
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForTimeout(1000);
    
    // Verify tenant ID is parsed
    const hasTenantLog = consoleLogs.some(log => 
      log.includes('tenant_id') && log.includes('1011')
    );
    expect(hasTenantLog).toBeTruthy();
  });

  test('should show detailed error message with console hint', async ({ page }) => {
    // Fill invalid URL
    await page.locator('#vmUrl').fill('http://nonexistent-host:8481');
    
    // Select auth
    await page.locator('#authType').selectOption('basic');
    await page.locator('#username').fill('test-user');
    await page.locator('#password').fill('test-password');
    
    // Test connection
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForTimeout(2000);
    
    // Check error message
    const errorBox = page.locator('#connectionResult .error-message');
    await expect(errorBox).toBeVisible();
    
    // Verify console hint is shown (updated text)
    await expect(errorBox).toContainText('Open browser console');
  });

  test('should log auth configuration without exposing credentials', async ({ page }) => {
    const consoleLogs = [];
    page.on('console', msg => {
      if (msg.type() === 'log') {
        consoleLogs.push(msg.text());
      }
    });
    
    // Fill connection details
    await page.locator('#vmUrl').fill('http://localhost:8428');
    await page.locator('#authType').selectOption('basic');
    await page.locator('#username').fill('secret-user');
    await page.locator('#password').fill('secret-password');
    
    // Test connection
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForTimeout(1000);
    
    // Verify credentials are NOT logged
    const hasPasswordInLogs = consoleLogs.some(log => 
      log.includes('secret-password')
    );
    expect(hasPasswordInLogs).toBe(false);
    
    // But auth type and presence should be logged (updated format)
    const hasAuthInfo = consoleLogs.some(log => 
      (log.includes('Auth: Basic') || log.includes('type') && log.includes('basic'))
    );
    expect(hasAuthInfo).toBeTruthy();
    
    // Check that final config was logged
    const hasConfigLog = consoleLogs.some(log => 
      log.includes('Final config') || log.includes('Connection Config')
    );
    expect(hasConfigLog).toBeTruthy();
  });

  test('should handle URL with full prometheus path', async ({ page }) => {
    const fullUrl = 'https://vm.example.com/1011/ui/prometheus/api/v1/query?query=sum(1)';
    
    const consoleLogs = [];
    page.on('console', msg => {
      if (msg.type() === 'log') {
        consoleLogs.push(msg.text());
      }
    });
    
    // Fill full URL
    await page.locator('#vmUrl').fill(fullUrl);
    
    // Select auth
    await page.locator('#authType').selectOption('basic');
    await page.locator('#username').fill('test');
    await page.locator('#password').fill('test');
    
    // Test connection
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForTimeout(1000);
    
    // Verify base URL is extracted correctly
    const hasBaseUrl = consoleLogs.some(log => 
      log.includes('vm.example.com')
    );
    expect(hasBaseUrl).toBeTruthy();
    
    // Verify tenant ID is extracted
    const hasTenant = consoleLogs.some(log => 
      log.includes('tenant_id') && log.includes('1011')
    );
    expect(hasTenant).toBeTruthy();
  });

  test('should log connection test lifecycle', async ({ page }) => {
    const consoleLogs = [];
    
    page.on('console', msg => {
      consoleLogs.push(msg.text());
    });
    
    // Fill connection
    await page.locator('#vmUrl').fill('http://localhost:8428');
    await page.locator('#authType').selectOption('none');
    
    // Test connection
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForTimeout(3000);
    
    // Should have config log
    const hasConfigLog = consoleLogs.some(log => 
      log.includes('Connection Config') || log.includes('Connection Test')
    );
    expect(hasConfigLog).toBeTruthy();
    
    // Should have result log (success or error)
    const hasResultLog = consoleLogs.some(log => 
      log.includes('successful') || log.includes('failed') || log.includes('Success') || log.includes('Failed')
    );
    expect(hasResultLog).toBeTruthy();
  });

  test('should parse vmselect URL with tenant correctly', async ({ page }) => {
    const vmselectUrl = 'http://vmselect:8481/select/0/prometheus';
    
    const consoleLogs = [];
    page.on('console', msg => {
      if (msg.type() === 'log') {
        consoleLogs.push(msg.text());
      }
    });
    
    await page.locator('#vmUrl').fill(vmselectUrl);
    await page.locator('#authType').selectOption('none');
    
    await page.locator('#testConnectionBtn').click();
    await page.waitForTimeout(1000);
    
    // Verify tenant ID "0" is parsed (check in Parsed URL or Final config)
    const hasTenantZero = consoleLogs.some(log => 
      (log.includes('tenantId') && log.includes('0')) || 
      (log.includes('tenant_id') && log.includes('0'))
    );
    expect(hasTenantZero).toBeTruthy();
    
    // Verify api_base_path includes /select/0/prometheus
    const hasCorrectPath = consoleLogs.some(log => 
      (log.includes('apiBasePath') || log.includes('api_base_path')) && 
      log.includes('/select/0/prometheus')
    );
    expect(hasCorrectPath).toBeTruthy();
  });
});

