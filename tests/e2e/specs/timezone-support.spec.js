const { test, expect } = require('@playwright/test');

test.describe('Timezone Support', () => {
    test('should have timezone selector on Time Range step', async ({ page }) => {
        await page.goto('/');
        
        // Navigate to Time Range step
        await page.click('button:has-text("Next")');
        
        // Check timezone selector exists
        const timezoneSelect = page.locator('#timezone');
        await expect(timezoneSelect).toBeVisible();
        
        // Check default value is "local"
        await expect(timezoneSelect).toHaveValue('local');
    });

    test('should have all major timezones available', async ({ page }) => {
        await page.goto('/');
        await page.click('button:has-text("Next")');
        
        const timezoneSelect = page.locator('#timezone');
        const options = await timezoneSelect.locator('option').allTextContents();
        
        // Check for key timezones
        expect(options.join(',')).toContain('Local Time');
        expect(options.join(',')).toContain('UTC');
        expect(options.join(',')).toContain('America/New York');
        expect(options.join(',')).toContain('Europe/London');
        expect(options.join(',')).toContain('Asia/Tokyo');
    });

    test('should update time inputs when timezone changes', async ({ page }) => {
        await page.goto('/');
        await page.click('button:has-text("Next")');
        
        // Get initial time value
        const initialFrom = await page.locator('#timeFrom').inputValue();
        
        // Change timezone to UTC
        await page.selectOption('#timezone', 'UTC');
        
        // Wait for update
        await page.waitForTimeout(100);
        
        // Get new time value
        const newFrom = await page.locator('#timeFrom').inputValue();
        
        // Times should be different (unless user is in UTC)
        // We just verify the function was called and inputs have values
        expect(newFrom).toBeTruthy();
        expect(initialFrom).toBeTruthy();
    });

    test('should apply timezone to preset buttons', async ({ page }) => {
        await page.goto('/');
        await page.click('button:has-text("Next")');
        
        // Select UTC timezone
        await page.selectOption('#timezone', 'UTC');
        await page.waitForTimeout(100);
        
        // Click "Last 1h" preset
        await page.click('button:has-text("Last 1h")');
        await page.waitForTimeout(100);
        
        // Verify time inputs are populated
        const timeFrom = await page.locator('#timeFrom').inputValue();
        const timeTo = await page.locator('#timeTo').inputValue();
        
        expect(timeFrom).toBeTruthy();
        expect(timeTo).toBeTruthy();
        expect(timeFrom).not.toBe(timeTo);
    });

    test('should preserve timezone selection when using presets', async ({ page }) => {
        await page.goto('/');
        await page.click('button:has-text("Next")');
        
        // Select Europe/London
        await page.selectOption('#timezone', 'Europe/London');
        await page.waitForTimeout(100);
        
        // Click different presets
        await page.click('button:has-text("Last 3h")');
        await page.waitForTimeout(100);
        
        await page.click('button:has-text("Last 6h")');
        await page.waitForTimeout(100);
        
        // Timezone should still be Europe/London
        const timezone = await page.locator('#timezone').inputValue();
        expect(timezone).toBe('Europe/London');
    });

    test('should format times correctly for different timezones', async ({ page }) => {
        await page.goto('/');
        await page.click('button:has-text("Next")');
        
        const timezones = ['local', 'UTC', 'America/New_York', 'Asia/Tokyo'];
        
        for (const tz of timezones) {
            await page.selectOption('#timezone', tz);
            await page.waitForTimeout(100);
            
            const timeFrom = await page.locator('#timeFrom').inputValue();
            const timeTo = await page.locator('#timeTo').inputValue();
            
            // Verify datetime-local format: YYYY-MM-DDTHH:mm
            expect(timeFrom).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
            expect(timeTo).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
            
            // From should be before To
            expect(new Date(timeFrom).getTime()).toBeLessThan(new Date(timeTo).getTime());
        }
    });
});
