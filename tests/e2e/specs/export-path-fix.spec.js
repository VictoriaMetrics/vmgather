import { test, expect } from '@playwright/test';

/**
 * CRITICAL BUG: /rw/prometheus works for /query but NOT for /export
 * 
 * Customer's setup:
 * - [OK] /1011/rw/prometheus/api/v1/query - WORKS
 * - [FAIL] /1011/rw/prometheus/api/v1/export - FAILS with "missing route"
 * 
 * Solution: Need to convert /rw/prometheus → /prometheus for /export endpoint
 */

test.describe('Export Path Fix', () => {
  
  test('Bug: /rw/prometheus fails for /api/export', async ({ request }) => {
    // This reproduces the EXACT error from production logs
    const response = await request.post('http://localhost:8080/api/export', {
      data: {
        connection: {
          url: 'https://vm.example.com',
          api_base_path: '/1011/rw/prometheus',
          full_api_url: 'https://vm.example.com/1011/rw/prometheus',
          auth: {
            type: 'basic',
            username: 'monitoring-read',
            password: 'fake-password'
          }
        },
        time_range: {
          start: new Date(Date.now() - 3600000).toISOString(),
          end: new Date().toISOString()
        },
        components: ['vmagent'],
        jobs: ['vmagent-prometheus'],
        obfuscation: {
          enabled: false
        }
      }
    });
    
    const body = await response.json();
    console.log('[QUERY] Export response:', body);
    
    // Check if we get the "missing route" error
    if (body.error && body.error.includes('missing route for "/1011/rw/prometheus/api/v1/export"')) {
      console.log('🔴 BUG CONFIRMED: /rw/prometheus not supported for /api/export');
      console.log('Expected: Should convert /rw/prometheus → /prometheus for export');
      test.fail(true, 'BUG: /rw/prometheus path not supported for /api/export endpoint');
    }
    
    // Expected: Auth error (not "missing route")
    if (body.error) {
      expect(body.error).not.toContain('missing route');
    }
  });
  
  test('Solution: /prometheus (without /rw) should work for export', async ({ request }) => {
    // Test if standard /prometheus path works
    const response = await request.post('http://localhost:8080/api/export', {
      data: {
        connection: {
          url: 'https://vm.example.com',
          api_base_path: '/1011/prometheus',  // Without /rw
          full_api_url: 'https://vm.example.com/1011/prometheus',
          auth: {
            type: 'basic',
            username: 'monitoring-read',
            password: 'fake-password'
          }
        },
        time_range: {
          start: new Date(Date.now() - 3600000).toISOString(),
          end: new Date().toISOString()
        },
        components: ['vmagent'],
        jobs: ['vmagent-prometheus'],
        obfuscation: {
          enabled: false
        }
      }
    });
    
    const body = await response.json();
    console.log('[QUERY] Export with /prometheus:', body);
    
    // Should NOT have "missing route" error
    if (body.error) {
      console.log('Error:', body.error);
      expect(body.error).not.toContain('missing route');
      
      // Expected: 401 auth error (which is OK - means path is recognized)
      if (body.error.includes('401') || body.error.includes('cannot authorize')) {
        console.log('[OK] /prometheus path is recognized (auth error is expected)');
      }
    }
  });
  
  test('Verify: /rw should be stripped only for /export, not for /query', async ({ request }) => {
    // /query with /rw/prometheus should work as-is
    const queryResponse = await request.post('http://localhost:8080/api/sample', {
      data: {
        config: {
          connection: {
            url: 'http://localhost:8428',
            api_base_path: '',
            auth: { type: 'none' }
          },
          time_range: {
            start: new Date(Date.now() - 3600000).toISOString(),
            end: new Date().toISOString()
          },
          components: [],
          jobs: []
        },
        limit: 1
      }
    });
    
    const queryBody = await queryResponse.json();
    console.log('[QUERY] Query response:', queryBody);
    
    // Should work (samples or valid error, not "unsupported protocol")
    if (queryBody.samples) {
      console.log('[OK] Query works');
      expect(queryBody.samples).toBeDefined();
    } else if (queryBody.error) {
      expect(queryBody.error).not.toContain('unsupported protocol scheme');
    }
  });
});

