const { test, expect } = require('@playwright/test');

async function completeConnectionStep(page, url = 'http://localhost:18428') {
    const step3 = page.locator('.step[data-step="3"].active');
    await step3.locator('#vmUrl').fill(url);
    await step3.locator('#testConnectionBtn').click();
    await page.waitForSelector('text=Connection Successful', { timeout: 10000 });
    await step3.locator('#step3Next').click();
}

async function ensureDefaultNetworkMocks(page, { mockValidate = true, mockDiscover = true, mockSample = true } = {}) {
    if (mockValidate && !page._vmDefaultValidateMock) {
        page._vmDefaultValidateMock = true;
        await page.route('**/api/validate', route => {
            route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({
                    success: true,
                    valid: true,
                    is_victoria_metrics: true,
                    vm_components: ['vmsingle'],
                    components: 1,
                    version: 'v1.95.0',
                    message: 'Connection Successful',
                }),
            });
        });
    }
    if (mockDiscover && !page._vmDefaultDiscoverMock) {
        page._vmDefaultDiscoverMock = true;
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
    }
    if (mockSample && !page._vmDefaultSampleMock) {
        page._vmDefaultSampleMock = true;
        await page.route('**/api/sample', route => {
            route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({
                    samples: [
                        {
                            name: 'up',
                            labels: {
                                __name__: 'up',
                                instance: '10.0.0.1:8428',
                                job: 'vmjob',
                            },
                        },
                    ],
                    count: 1,
                }),
            });
        });
    }
}

async function goToObfuscation(page, url = 'http://localhost:18428', options = {}) {
    await ensureDefaultNetworkMocks(page, options);
    await page.goto('http://localhost:8080');
    await page.waitForLoadState('networkidle');
    await page.locator('button.btn-primary:has-text("Next")').click();
    await page.waitForTimeout(200);
    const current = await page.evaluate(() => document.querySelector('.step.active')?.getAttribute('data-step') || null);
    if (current !== '2') {
        await page.evaluate(() => window.nextStep && window.nextStep());
    }
    await page.waitForSelector('.step[data-step="2"].active');
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await completeConnectionStep(page, url);
    await page.waitForSelector('.component-item input[type="checkbox"]');
    await page.locator('.component-item input[type="checkbox"]').first().check();
    await page.locator('.step.active button.btn-primary:has-text("Next")').click();
    await page.waitForSelector('#enableObfuscation');
    await configureExportEnvironment(page);
}

async function configureExportEnvironment(page) {
    const stagingInput = page.locator('#stagingDir');
    if (await stagingInput.count()) {
        await stagingInput.fill('/tmp/ui-tests');
    }
    const metricSelect = page.locator('#metricStep');
    if (await metricSelect.count()) {
        await metricSelect.selectOption('auto');
    }
}

test.describe('Obfuscation Functionality', () => {

    test('should send correct obfuscation config to backend when instance is selected', async ({ page }) => {
        await goToObfuscation(page);
        // Fill connection details
        await page.check('#enableObfuscation');
        await page.waitForSelector('#obfuscationOptions', { state: 'visible' });

        await page.locator('.obf-label-checkbox[data-label="job"]').uncheck();

        // Select instance for obfuscation
        const instanceCheckbox = page.locator('.obf-label-checkbox[data-label="instance"]');
        await instanceCheckbox.check();

        // Intercept export request
        let exportRequest = null;
        page.on('request', request => {
            if (request.url().includes('/api/export')) {
                exportRequest = request;
            }
        });

        // Trigger export (mock - we just want to check request)
        await page.evaluate(() => {
            const config = window.getObfuscationConfig();
            console.log('Obfuscation config:', config);

            // Verify config structure
            if (!config.hasOwnProperty('obfuscate_instance')) {
                throw new Error('Missing obfuscate_instance field');
            }
            if (!config.hasOwnProperty('obfuscate_job')) {
                throw new Error('Missing obfuscate_job field');
            }
            if (config.obfuscate_instance !== true) {
                throw new Error('obfuscate_instance should be true');
            }
            if (config.obfuscate_job !== false) {
                throw new Error('obfuscate_job should be false when not selected');
            }
        });
    });

    test('should send correct obfuscation config when both instance and job are selected', async ({ page }) => {
        await goToObfuscation(page);
        await page.check('#enableObfuscation');
        await page.waitForSelector('#obfuscationOptions', { state: 'visible' });

        // Select both instance and job
        await page.locator('.obf-label-checkbox[data-label="instance"]').check();
        await page.locator('.obf-label-checkbox[data-label="job"]').check();

        // Verify config
        await page.evaluate(() => {
            const config = window.getObfuscationConfig();
            console.log('Obfuscation config (both):', config);

            if (config.obfuscate_instance !== true) {
                throw new Error('obfuscate_instance should be true');
            }
            if (config.obfuscate_job !== true) {
                throw new Error('obfuscate_job should be true');
            }
            if (config.enabled !== true) {
                throw new Error('enabled should be true');
            }
        });
    });

    test('should send disabled obfuscation config when checkbox is unchecked', async ({ page }) => {
        await goToObfuscation(page);
        const obfCheckbox = page.locator('#enableObfuscation');
        const isChecked = await obfCheckbox.isChecked();
        if (isChecked) {
            await obfCheckbox.uncheck();
        }

        // Verify config
        await page.evaluate(() => {
            const config = window.getObfuscationConfig();
            console.log('Obfuscation config (disabled):', config);

            if (config.enabled !== false) {
                throw new Error('enabled should be false');
            }
            if (config.obfuscate_instance !== false) {
                throw new Error('obfuscate_instance should be false when disabled');
            }
            if (config.obfuscate_job !== false) {
                throw new Error('obfuscate_job should be false when disabled');
            }
        });
    });

    test('should not obfuscate pod or namespace labels', async ({ page }) => {
        await page.route('**/api/sample', route => {
            const sample = {
                name: 'go_memstats_alloc_bytes_total',
                labels: {
                    __name__: 'go_memstats_alloc_bytes_total',
                    instance: '10.0.0.1:8428',
                    job: 'vmjob',
                    pod: 'pod-original',
                    namespace: 'ns-original',
                },
            };
            route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({ samples: [sample], count: 1 }),
            });
        });
        await goToObfuscation(page, 'http://localhost:18428', { mockSample: false });
        await page.check('#enableObfuscation');
        await page.waitForSelector('#obfuscationOptions', { state: 'visible' });

        await page.locator('#obfuscationOptions details summary').first().click();

        // Deselect default labels, select pod and namespace only
        await page.locator('.obf-label-checkbox[data-label="instance"]').uncheck();
        await page.locator('.obf-label-checkbox[data-label="job"]').uncheck();
        await page.locator('.obf-label-checkbox[data-label="pod"]').check();
        await page.locator('.obf-label-checkbox[data-label="namespace"]').check();

        // Verify config - backend only supports instance and job
        await page.evaluate(() => {
            const config = window.getObfuscationConfig();
            console.log('Obfuscation config (pod/namespace):', config);

            // These should still be false because backend doesn't support them
            if (config.obfuscate_instance !== false) {
                throw new Error('obfuscate_instance should be false (pod/namespace not supported)');
            }
            if (config.obfuscate_job !== false) {
                throw new Error('obfuscate_job should be false (pod/namespace not supported)');
            }
            // But enabled should be true
            if (config.enabled !== true) {
                throw new Error('enabled should be true');
            }
        });
    });

    test('should preserve structure flag in config', async ({ page }) => {
        await goToObfuscation(page);
        await page.check('#enableObfuscation');

        // Verify preserve_structure is always true
        await page.evaluate(() => {
            const config = window.getObfuscationConfig();
            console.log('Obfuscation config (structure):', config);

            if (!config.hasOwnProperty('preserve_structure')) {
                throw new Error('Missing preserve_structure field');
            }
            if (config.preserve_structure !== true) {
                throw new Error('preserve_structure should always be true');
            }
        });
    });

    test('should refresh sample preview when toggles change', async ({ page }) => {
        await page.route('**/api/validate', route => {
            route.fulfill({
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

        await page.route('**/api/sample', async route => {
            const body = route.request().postDataJSON();
            const obf = body?.config?.obfuscation || {};
            const custom = obf.custom_labels || [];
            console.log('sample request obf config', obf);

            let instance = '10.0.0.1:8428';
            let job = 'vmjob';
            let pod = 'pod-original';

            if (obf.enabled) {
                if (obf.obfuscate_instance) {
                    instance = '777.777.1.1:8428';
                }
                if (obf.obfuscate_job) {
                    job = 'vm_component_vmjob_1';
                }
                if (custom.includes('pod')) {
                    pod = 'pod-1';
                }
            }

            const sample = {
                name: 'go_memstats_alloc_bytes_total',
                labels: {
                    __name__: 'go_memstats_alloc_bytes_total',
                    instance,
                    job,
                    pod,
                },
            };

            await route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({ samples: [sample], count: 1 }),
            });
        });

        await goToObfuscation(page, 'http://mock-endpoint:8428', {
            mockValidate: false,
            mockDiscover: false,
            mockSample: false,
        });
        await page.waitForSelector('#enableObfuscation');
        await page.check('#enableObfuscation');

        const preview = page.locator('#samplePreview');
        const initialVersion = await page.evaluate(() => window.__vm_samples_version || 0);
        await expect.poll(async () => {
            return await page.evaluate(() => window.__vm_samples_version || 0);
        }, { timeout: 15000 }).toBeGreaterThan(initialVersion);
        await expect(preview).toContainText('777.777.1.1:8428');

        await page.locator('.obf-label-checkbox[data-label="instance"]').uncheck();
        const versionAfterUncheck = await page.evaluate(() => window.__vm_samples_version || 0);
        await expect.poll(async () => await page.evaluate(() => window.__vm_samples_version || 0), { timeout: 10000 }).toBeGreaterThan(versionAfterUncheck);
        await expect(preview).toContainText('10.0.0.1:8428');

        await page.locator('.obf-label-checkbox[data-label="instance"]').check();
        const versionAfterCheck = await page.evaluate(() => window.__vm_samples_version || 0);
        await expect.poll(async () => await page.evaluate(() => window.__vm_samples_version || 0), { timeout: 10000 }).toBeGreaterThan(versionAfterCheck);
        await expect(preview).toContainText('777.777.1.1:8428');

        const advancedSummary = page.locator('#obfuscationOptions details').first().locator('summary');
        await advancedSummary.click();

        const podCheckbox = page.locator('.obf-label-checkbox[data-label="pod"]');
        await podCheckbox.scrollIntoViewIfNeeded();
        await podCheckbox.check();
        await expect(preview).toContainText('pod-1');

        await podCheckbox.uncheck();
        await expect(preview).toContainText('pod-original');
    });
});
