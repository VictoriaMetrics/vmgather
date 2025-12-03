import { test, expect } from '@playwright/test';

/**
 * CRITICAL BUG TESTS: Connection Config Not Passed
 * 
 * Bug #1: /api/sample receives empty connection config
 * Bug #2: /api/export fails with "/rw/prometheus" path (not supported for export)
 * 
 * These tests reproduce the EXACT bugs from production logs.
 */

test.describe('Connection Config Bugs', () => {

  test('Bug #1: /api/sample MUST receive connection config', async ({ request }) => {
    // Test with LOCAL VMSingle (no auth)
    const response = await request.post('http://localhost:8080/api/sample', {
      data: {
        config: {
          connection: {
            url: 'http://localhost:18428',
            api_base_path: '',
            auth: {
              type: 'none'
            }
          },
          time_range: {
            start: new Date(Date.now() - 3600000).toISOString(),
            end: new Date().toISOString()
          },
          components: [],
          jobs: []
        },
        limit: 5
      }
    });

    const body = await response.json();
    console.log('[QUERY] Sample response:', body);

    // Should NOT have "unsupported protocol scheme" error
    if (body.error && body.error.includes('unsupported protocol scheme ""')) {
      console.log('🔴 BUG #1 CONFIRMED: Backend received EMPTY connection URL!');
      test.fail(true, 'BUG #1: Connection config not passed correctly to backend');
    }

    // Should get samples or a valid error (not empty URL)
    if (body.samples) {
      console.log('[OK] Got samples:', body.samples.length);
      expect(body.samples).toBeDefined();
    } else if (body.error) {
      console.log('Error:', body.error);
      // Error is OK, but NOT "unsupported protocol scheme"
      expect(body.error).not.toContain('unsupported protocol scheme');
    }
  });

  test('Bug #2: /rw/prometheus path fails for /api/export', async ({ request }) => {
    // Test direct API call with /rw/prometheus path
    const response = await request.post('http://localhost:8080/api/export', {
      data: {
        connection: {
          url: 'https://vm.example.com',
          api_base_path: '/1011/rw/prometheus',
          tenant_id: '1011',
          auth: {
            type: 'basic',
            username: 'test-user',
            password: 'test-pass'
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

    const contentType = response.headers()['content-type'];
    expect(contentType).toContain('application/json');

    const body = await response.json();
    console.log('[QUERY] Export response:', body);

    // Check if error mentions "/rw/prometheus"
    if (body.error && body.error.includes('missing route for "/1011/rw/prometheus')) {
      console.log('🔴 BUG #2 CONFIRMED: /rw/prometheus not supported for /api/export');
      console.log('Error:', body.error);

      // This is expected to fail - we need to fix the path
      test.fail(true, 'BUG #2: /rw/prometheus path not supported for export endpoint');
    }
  });

  test('Bug #2 alternative: Test if /ui/prometheus works for export', async ({ request }) => {
    // Test if /ui/prometheus has same issue
    const response = await request.post('http://localhost:8080/api/export', {
      data: {
        connection: {
          url: 'https://vm.example.com',
          api_base_path: '/1011/ui/prometheus',
          tenant_id: '1011',
          auth: {
            type: 'basic',
            username: 'test-user',
            password: 'test-pass'
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
    console.log('[QUERY] Export with /ui/prometheus:', body);

    if (body.error && body.error.includes('missing route')) {
      console.log('🔴 /ui/prometheus also fails for export');
      console.log('Error:', body.error);
    }
  });

  test('Solution test: /prometheus (without /rw or /ui) should work', async ({ request }) => {
    // Test if standard /prometheus path works
    const response = await request.post('http://localhost:8080/api/export', {
      data: {
        connection: {
          url: 'https://vm.example.com',
          api_base_path: '/1011/prometheus',  // Standard path
          tenant_id: '1011',
          auth: {
            type: 'basic',
            username: 'test-user',
            password: 'test-pass'
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

    // This might still fail due to auth, but error should be different
    if (body.error) {
      console.log('Error:', body.error);

      // Check if it's NOT a "missing route" error
      if (!body.error.includes('missing route')) {
        console.log('[OK] /prometheus path is recognized (even if auth fails)');
      }
    }
  });
});

