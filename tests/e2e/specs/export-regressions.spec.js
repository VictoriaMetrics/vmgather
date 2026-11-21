// Regression specs for export failures seen in production.
const { test, expect } = require('@playwright/test');

const COMPONENTS = [
  { component: 'victoria-metrics', jobs: ['victoria_metrics', 'victoria_metrics_2'], instance_count: 2, metrics_count_estimate: 1000, job_metrics: { victoria_metrics: 800, victoria_metrics_2: 200 } },
  { component: 'vmagent', jobs: ['victoria_metrics_agent_container'], instance_count: 2, metrics_count_estimate: 500, job_metrics: { victoria_metrics_agent_container: 500 } },
  { component: 'vmalert', jobs: ['vmalert'], instance_count: 1, metrics_count_estimate: 100, job_metrics: { vmalert: 100 } },
];

async function mockBackend(page) {
  await page.route('**/api/validate', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, valid: true, is_victoria_metrics: true, vm_components: ['vmsingle'], components: 3, version: 'v1.142.0' }) }));
  await page.route('**/api/discover', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ components: COMPONENTS }) }));
  await page.route('**/api/sample', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ samples: [{ name: 'go_mem', labels: { job: 'x' } }] }) }));
  await page.route('**/api/fs/check', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true, exists: true, writable: true, abs_path: '/tmp/vmgather-e2e', message: 'Ready' }) }));
}

async function openComponentsStep(page) {
  await page.goto('/');
  await page.waitForLoadState('networkidle');
  await page.evaluate(() => window.nextStep && window.nextStep());
  await page.waitForSelector('.step[data-step="2"].active');
  await page.locator('.step.active button:has-text("Next")').first().click();
  await page.locator('#vmUrl').fill('http://localhost:8428');
  await page.locator('#testConnectionBtn').click();
  await page.waitForSelector('#step3Next:enabled');
  await page.locator('#step3Next').click();
  await page.waitForSelector('.component-item input[type="checkbox"]');
}

async function startExport(page, components) {
  for (const c of components) {
    await page.locator(`.component-header input[data-component="${c}"]`).check();
  }
  await page.locator('.step.active button:has-text("Next")').first().click();
  await page.waitForSelector('.step[data-step="6"].active');
  await page.locator('#startExportBtn').click();
}

test.describe('Export regressions', () => {
  test('components with a single job send their job', async ({ page }) => {
    await mockBackend(page);
    let payload;
    await page.route('**/api/export/start', r => { payload = r.request().postDataJSON(); r.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'stop' }) }); });
    page.on('dialog', d => d.dismiss());
    await openComponentsStep(page);
    await startExport(page, ['victoria-metrics', 'vmagent', 'vmalert']);
    await expect.poll(() => payload).toBeTruthy();
    expect([...payload.jobs].sort()).toEqual(['victoria_metrics', 'victoria_metrics_2', 'victoria_metrics_agent_container', 'vmalert'].sort());
  });

  test('only a single-job component selected still sends its job', async ({ page }) => {
    await mockBackend(page);
    let payload;
    await page.route('**/api/export/start', r => { payload = r.request().postDataJSON(); r.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'stop' }) }); });
    page.on('dialog', d => d.dismiss());
    await openComponentsStep(page);
    await startExport(page, ['vmagent']);
    await expect.poll(() => payload).toBeTruthy();
    expect(payload.jobs).toEqual(['victoria_metrics_agent_container']);
  });

  test('failed export can be resumed', async ({ page }) => {
    await mockBackend(page);
    let resumed = false;
    await page.route('**/api/export/start', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ job_id: 'job-failed', total_batches: 10, batch_window_seconds: 3600, staging_path: '/tmp/vmgather-e2e/job-failed.partial.jsonl' }) }));
    await page.route('**/api/export/status**', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(resumed
      ? { job_id: 'job-failed', state: 'running', total_batches: 10, completed_batches: 2, progress: 0.2 }
      : { job_id: 'job-failed', state: 'failed', total_batches: 10, completed_batches: 2, progress: 0.2, error: 'export failed: unexpected status code 400: cannot process request' }) }));
    await page.route('**/api/export/resume', r => { resumed = true; r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ job_id: 'job-failed', state: 'pending', total_batches: 10, completed_batches: 2 }) }); });
    page.on('dialog', d => d.dismiss());
    await openComponentsStep(page);
    await startExport(page, ['vmagent']);
    await expect(page.locator('#resumeExportBtn')).toBeVisible({ timeout: 10000 });
    await page.locator('#resumeExportBtn').click();
    await expect.poll(() => resumed).toBe(true);
  });

  test('after a failure the main button starts a new job and Resume sends the failed job id', async ({ page }) => {
    await mockBackend(page);
    const starts = [];
    let resumeBody;
    await page.route('**/api/export/start', r => {
      starts.push(r.request().postDataJSON());
      r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ job_id: `job-${starts.length}`, total_batches: 10, batch_window_seconds: 3600, staging_path: '/tmp/vmgather-e2e/x.partial.jsonl' }) });
    });
    await page.route('**/api/export/status**', r => {
      const id = new URL(r.request().url()).searchParams.get('id') || 'job-1';
      r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ job_id: id, state: 'failed', total_batches: 10, completed_batches: 3, error: 'query exceeds VictoriaMetrics series limits' }) });
    });
    await page.route('**/api/export/resume', r => { resumeBody = r.request().postDataJSON(); r.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'stop' }) }); });
    page.on('dialog', d => d.dismiss());
    await openComponentsStep(page);
    await startExport(page, ['vmagent']);
    await expect(page.locator('#resumeExportBtn')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('#exportCancelNotice')).toContainText('3 of 10 batches are kept');

    // The main button is not in resume mode: it starts a new export with the current selection.
    await expect(page.locator('#startExportBtn')).not.toHaveText(/Resume/);
    await page.locator('#startExportBtn').click();
    await expect.poll(() => starts.length).toBe(2);

    // Resume continues the failed job by id.
    await expect(page.locator('#resumeExportBtn')).toBeVisible({ timeout: 10000 });
    await page.locator('#resumeExportBtn').click();
    await expect.poll(() => resumeBody && resumeBody.job_id).toBe('job-2');
  });

  test('a multi-job component with one job unchecked sends only checked jobs', async ({ page }) => {
    await mockBackend(page);
    let payload;
    await page.route('**/api/export/start', r => { payload = r.request().postDataJSON(); r.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'stop' }) }); });
    page.on('dialog', d => d.dismiss());
    await openComponentsStep(page);
    await page.locator('.component-header input[data-component="victoria-metrics"]').check();
    await page.locator('.job-item input[data-job="victoria_metrics_2"]').uncheck();
    await page.locator('.step.active button:has-text("Next")').first().click();
    await page.waitForSelector('.step[data-step="6"].active');
    await page.locator('#startExportBtn').click();
    await expect.poll(() => payload).toBeTruthy();
    expect(payload.jobs).toEqual(['victoria_metrics']);
  });

  // Errors come from the target or a proxy, job names and label values from scraped data: all of them are text.
  test('server errors and job names are shown as text, not HTML', async ({ page }) => {
    const probe = '<img src=x id=xss-probe onerror="window.__xss=1">';
    await page.route('**/api/validate', r => r.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ success: false, error: `bad gateway ${probe}` }) }));
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.evaluate(() => window.nextStep && window.nextStep());
    await page.waitForSelector('.step[data-step="2"].active');
    await page.locator('.step.active button:has-text("Next")').first().click();
    await page.locator('#vmUrl').fill('http://localhost:8428');
    await page.locator('#testConnectionBtn').click();
    await expect(page.locator('#validationSteps')).toContainText('bad gateway <img src=x id=xss-probe');
    await expect(page.locator('#xss-probe')).toHaveCount(0);

    await page.unroute('**/api/validate');
    await mockBackend(page);
    await page.route('**/api/discover', r => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ components: [
      { component: 'vmagent', jobs: ['<b id=xss-job>agent</b>', 'agent_2'], instance_count: 2, metrics_count_estimate: 10, job_metrics: {} },
    ] }) }));
    await page.locator('#testConnectionBtn').click();
    await page.waitForSelector('#step3Next:enabled');
    await page.locator('#step3Next').click();
    await expect(page.locator('.component-item')).toContainText('<b id=xss-job>agent</b>');
    await expect(page.locator('#xss-job')).toHaveCount(0);
    expect(await page.evaluate(() => window.__xss)).toBeUndefined();
  });
});
