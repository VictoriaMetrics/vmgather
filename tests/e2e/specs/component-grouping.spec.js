import { test, expect } from '@playwright/test';

test.describe('Component Grouping - Multiple Jobs', () => {
  
  test.beforeEach(async ({ page }) => {
    // Mock connection validation
    await page.route('/api/validate', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          valid: true,
          version: 'v1.95.1',
          is_victoria_metrics: true
        })
      });
    });
  });
  
  test('should display job groups for components with multiple jobs', async ({ page }) => {
    // Mock backend response with component having multiple jobs
    await page.route('/api/discover', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          components: [
            {
              component: 'vmstorage',
              jobs: ['vmstorage-prod', 'vmstorage-dev'],
              instance_count: 6,
              metrics_count_estimate: 3000
            },
            {
              component: 'vmselect',
              jobs: ['vmselect-prod'],
              instance_count: 2,
              metrics_count_estimate: 1000
            }
          ]
        })
      });
    });
    
    // Navigate through wizard to step 4
    await page.goto('/');
    
    // Step 1: Welcome
    await page.locator('.step.active button:has-text("Next")').click();
    
    // Step 2: Time Range
    await page.locator('.step.active button:has-text("Next")').click();
    
    // Step 3: Connection
    await page.locator('.step.active #vmUrl').fill('http://vmselect:8481/select/0/prometheus');
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    
    // Step 4: Components - verify display
    await page.waitForSelector('.component-item');
    
    // Verify: vmstorage component header shows both jobs
    const vmstorageDetails = await page.locator('.component-item:has-text("vmstorage") .component-details').textContent();
    expect(vmstorageDetails).toContain('vmstorage-prod');
    expect(vmstorageDetails).toContain('vmstorage-dev');
    expect(vmstorageDetails).toContain('Instances: 6');
    
    // Verify: vmstorage job group is visible
    const vmstorageJobsGroup = page.locator('.component-item:has-text("vmstorage") .jobs-group');
    await expect(vmstorageJobsGroup).toBeVisible();
    
    // Verify: vmstorage has 2 job checkboxes
    const vmstorageJobCheckboxes = page.locator('.component-item:has-text("vmstorage") .job-item input[type="checkbox"]');
    await expect(vmstorageJobCheckboxes).toHaveCount(2);
    
    // Verify: vmselect does NOT have job group (single job)
    const vmselectJobsGroup = page.locator('.component-item:has-text("vmselect") .jobs-group');
    await expect(vmselectJobsGroup).not.toBeVisible();
  });
  
  test('should allow selecting individual jobs within component', async ({ page }) => {
    await page.route('/api/discover', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          components: [
            {
              component: 'vmstorage',
              jobs: ['vmstorage-prod', 'vmstorage-dev'],
              instance_count: 6,
              metrics_count_estimate: 3000
            }
          ]
        })
      });
    });
    
    // Navigate to step 4
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active #vmUrl').fill('http://vmselect:8481');
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    
    await page.waitForSelector('.component-item');
    
    // Initially, nothing is selected
    const componentCheckbox = page.locator('.component-item:has-text("vmstorage") .component-header input[type="checkbox"]');
    await expect(componentCheckbox).not.toBeChecked();
    
    // Select only first job
    await page.locator('.component-item:has-text("vmstorage") .job-item').first().locator('input[type="checkbox"]').click();
    
    // Verify: component checkbox is now checked (auto-selection)
    await expect(componentCheckbox).toBeChecked();
    
    // Verify: only one job is selected
    const selectedJobs = await page.locator('.component-item:has-text("vmstorage") .job-item input[type="checkbox"]:checked').count();
    expect(selectedJobs).toBe(1);
    
    // Select second job
    await page.locator('.component-item:has-text("vmstorage") .job-item').nth(1).locator('input[type="checkbox"]').click();
    
    // Verify: now both jobs are selected
    const selectedJobsAfter = await page.locator('.component-item:has-text("vmstorage") .job-item input[type="checkbox"]:checked').count();
    expect(selectedJobsAfter).toBe(2);
  });
  
  test('should deselect component when all jobs are unchecked', async ({ page }) => {
    await page.route('/api/discover', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          components: [
            {
              component: 'vmstorage',
              jobs: ['vmstorage-prod', 'vmstorage-dev'],
              instance_count: 6,
              metrics_count_estimate: 3000
            }
          ]
        })
      });
    });
    
    // Navigate to step 4
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active #vmUrl').fill('http://vmselect:8481');
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    
    await page.waitForSelector('.component-item');
    
    // Select component (selects all jobs)
    const componentCheckbox = page.locator('.component-item:has-text("vmstorage") .component-header input[type="checkbox"]');
    await componentCheckbox.click();
    
    // Verify: all jobs are selected
    await expect(componentCheckbox).toBeChecked();
    const selectedJobs = await page.locator('.component-item:has-text("vmstorage") .job-item input[type="checkbox"]:checked').count();
    expect(selectedJobs).toBe(2);
    
    // Uncheck all jobs one by one
    await page.locator('.component-item:has-text("vmstorage") .job-item').first().locator('input[type="checkbox"]').click();
    await page.locator('.component-item:has-text("vmstorage") .job-item').nth(1).locator('input[type="checkbox"]').click();
    
    // Verify: component checkbox is now unchecked (auto-deselection)
    await expect(componentCheckbox).not.toBeChecked();
  });
  
  test('should NOT show "undefined" for jobs', async ({ page }) => {
    await page.route('/api/discover', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          components: [
            {
              component: 'vmstorage',
              jobs: ['vmstorage-prod', 'vmstorage-dev'],
              instance_count: 6,
              metrics_count_estimate: 3000
            }
          ]
        })
      });
    });
    
    // Navigate to step 4
    await page.goto('/');
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active button:has-text("Next")').click();
    await page.locator('.step.active #vmUrl').fill('http://vmselect:8481');
    await page.locator('.step.active #testConnectionBtn').click();
    await page.waitForSelector('.step.active #step3Next:not([disabled])', { timeout: 10000 });
    await page.locator('.step.active #step3Next').click();
    
    await page.waitForSelector('.component-item');
    
    // Verify: NO "undefined" text in component details
    const vmstorageDetails = await page.locator('.component-item:has-text("vmstorage") .component-details').textContent();
    expect(vmstorageDetails).not.toContain('undefined');
    
    // Verify: Jobs are properly displayed
    expect(vmstorageDetails).toContain('Jobs: vmstorage-prod, vmstorage-dev');
  });
});


