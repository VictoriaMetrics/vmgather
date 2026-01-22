import { test, expect } from '@playwright/test';

const VMGATHER_URL = process.env.VMGATHER_URL || 'http://localhost:8080';

/**
 * CRITICAL TESTS: API Error Response Format
 * 
 * These tests verify that ALL API endpoints return JSON even on errors.
 * This is CRITICAL because frontend expects JSON and breaks on text/plain responses.
 * 
 * Bug Report: @bugreportStage5.md
 * - "Unexpected token 'E', "Export fai"... is not valid JSON"
 * - "Unexpected response type: text/plain; charset=utf-8. Expected JSON"
 */

test.describe('API Error Responses - MUST Return JSON', () => {
  
  test('POST /api/sample - should return JSON on error', async ({ request }) => {
    // Send invalid request to /api/sample
    const response = await request.post(`${VMGATHER_URL}/api/sample`, {
      data: {
        config: {
          connection: {
            url: 'http://invalid-host-that-does-not-exist:9999',
            auth: { type: 'none' }
          },
          time_range: {
            start: new Date(Date.now() - 3600000).toISOString(),
            end: new Date().toISOString()
          },
          components: ['vmstorage'],
          jobs: ['test-job']
        },
        limit: 10
      }
    });
    
    // CRITICAL CHECK: Content-Type must be application/json
    const contentType = response.headers()['content-type'] || '';
    console.log('POST /api/sample - Status:', response.status());
    console.log('POST /api/sample - Content-Type:', contentType);
    
    expect(contentType).toContain('application/json');
    
    // CRITICAL CHECK: Response must be valid JSON
    const body = await response.json();
    console.log('POST /api/sample - Response:', body);
    
    // On error, must have 'error' field
    expect(response.status()).toBeGreaterThanOrEqual(400);
    expect(body).toHaveProperty('error');
    expect(typeof body.error).toBe('string');
  });
  
  test('POST /api/export - should return JSON on error', async ({ request }) => {
    // Send invalid request to /api/export
    const response = await request.post(`${VMGATHER_URL}/api/export`, {
      data: {
        connection: {
          url: 'http://invalid-host:9999',
          auth: { type: 'none' }
        },
        time_range: {
          start: new Date(Date.now() - 3600000).toISOString(),
          end: new Date().toISOString()
        },
        components: ['vmstorage'],
        jobs: ['test-job'],
        obfuscation: {
          enabled: false
        }
      }
    });
    
    // CRITICAL CHECK: Content-Type must be application/json
    const contentType = response.headers()['content-type'] || '';
    console.log('POST /api/export - Status:', response.status());
    console.log('POST /api/export - Content-Type:', contentType);
    
    expect(contentType).toContain('application/json');
    
    // CRITICAL CHECK: Response must be valid JSON
    const body = await response.json();
    console.log('POST /api/export - Response:', body);
    
    // On error, must have 'error' field
    expect(response.status()).toBeGreaterThanOrEqual(400);
    expect(body).toHaveProperty('error');
    expect(typeof body.error).toBe('string');
  });
  
  test('POST /api/discover - should return JSON on error', async ({ request }) => {
    const response = await request.post(`${VMGATHER_URL}/api/discover`, {
      data: {
        connection: {
          url: 'http://invalid:9999',
          auth: { type: 'none' }
        },
        time_range: {
          start: new Date(Date.now() - 3600000).toISOString(),
          end: new Date().toISOString()
        }
      }
    });
    
    const contentType = response.headers()['content-type'] || '';
    console.log('POST /api/discover - Status:', response.status());
    console.log('POST /api/discover - Content-Type:', contentType);
    
    expect(contentType).toContain('application/json');
    
    const body = await response.json();
    expect(response.status()).toBeGreaterThanOrEqual(400);
    expect(body).toHaveProperty('error');
  });
  
  test('POST /api/validate - should return JSON on error', async ({ request }) => {
    const response = await request.post(`${VMGATHER_URL}/api/validate`, {
      data: {
        url: 'http://invalid:9999',
        auth: { type: 'none' }
      }
    });
    
    const contentType = response.headers()['content-type'] || '';
    console.log('POST /api/validate - Status:', response.status());
    console.log('POST /api/validate - Content-Type:', contentType);
    
    // This might return 200 with success:false, but still must be JSON
    expect(contentType).toContain('application/json');
    
    const body = await response.json();
    expect(body).toBeDefined();
  });
  
  test('GET /api/download with missing path param - should return JSON error', async ({ request }) => {
    // Test without path parameter (should return JSON error from our code)
    const response = await request.get(`${VMGATHER_URL}/api/download`);
    
    const contentType = response.headers()['content-type'] || '';
    console.log('GET /api/download (no path) - Status:', response.status());
    console.log('GET /api/download (no path) - Content-Type:', contentType);
    
    // Our code handles this case - should return JSON
    expect(response.status()).toBe(400);
    expect(contentType).toContain('application/json');
    
    const body = await response.json();
    expect(body).toHaveProperty('error');
    expect(body.error).toContain('Missing path parameter');
  });
});
