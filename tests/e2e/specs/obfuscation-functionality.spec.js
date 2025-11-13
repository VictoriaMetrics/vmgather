const { test, expect } = require('@playwright/test');

test.describe('Obfuscation Functionality', () => {
    test.beforeEach(async ({ page }) => {
        await page.goto('http://localhost:8080');
        await page.waitForLoadState('networkidle');
    });

    test('should send correct obfuscation config to backend when instance is selected', async ({ page }) => {
        // Fill connection details
        await page.fill('#vmUrl', 'http://localhost:8428');
        await page.click('button:has-text("Validate Connection")');
        await page.waitForSelector('text=Connection successful', { timeout: 10000 });

        // Enable obfuscation
        await page.check('#enableObfuscation');
        await page.waitForSelector('#obfuscationOptions', { state: 'visible' });

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
        // Fill connection details
        await page.fill('#vmUrl', 'http://localhost:8428');
        await page.click('button:has-text("Validate Connection")');
        await page.waitForSelector('text=Connection successful', { timeout: 10000 });

        // Enable obfuscation
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
        // Fill connection details
        await page.fill('#vmUrl', 'http://localhost:8428');
        await page.click('button:has-text("Validate Connection")');
        await page.waitForSelector('text=Connection successful', { timeout: 10000 });

        // Ensure obfuscation is disabled (default state)
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
        // Fill connection details
        await page.fill('#vmUrl', 'http://localhost:8428');
        await page.click('button:has-text("Validate Connection")');
        await page.waitForSelector('text=Connection successful', { timeout: 10000 });

        // Enable obfuscation
        await page.check('#enableObfuscation');
        await page.waitForSelector('#obfuscationOptions', { state: 'visible' });

        // Select pod and namespace (should be ignored by backend)
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
        // Fill connection details
        await page.fill('#vmUrl', 'http://localhost:8428');
        await page.click('button:has-text("Validate Connection")');
        await page.waitForSelector('text=Connection successful', { timeout: 10000 });

        // Enable obfuscation
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
});

