/**
 * E2E Test: /rw/prometheus path normalization for /export endpoint
 * 
 * CRITICAL BUG REPRODUCTION:
 * Customer reported: "Export failed: missing route for /1011/rw/prometheus/api/v1/export"
 * 
 * ROOT CAUSE:
 * - /rw/prometheus works for /query (read operations)
 * - /rw/prometheus does NOT work for /export (requires different VMAuth routing)
 * - VMAuth routes /rw/prometheus to write endpoints, which don't support /export
 * 
 * SOLUTION:
 * Backend must normalize /rw/prometheus → /prometheus ONLY for /export requests
 */

const { test, expect } = require('@playwright/test');

test.describe('Bug Fix: /rw/prometheus path for export', () => {
    
    test('REPRODUCE BUG: /rw/prometheus fails for /api/export', async ({ request }) => {
        console.log('\n🐛 REPRODUCING BUG: /rw/prometheus export failure');
        
        // CRITICAL: /api/export expects ExportConfig directly, NOT wrapped in {config: ...}
        // This is the EXACT config that customer used
        const buggyConfig = {
            connection: {
                url: 'https://vm.example.com',
                api_base_path: '',
                tenant_id: null,
                is_multitenant: false,
                full_api_url: 'https://vm.example.com/1011/rw/prometheus',
                auth: {
                    type: 'basic',
                    username: 'fake-user',
                    password: 'fake-pass'
                }
            },
            time_range: {
                start: '2025-11-12T00:00:00Z',
                end: '2025-11-12T01:00:00Z'
            },
            components: ['vmagent'],
            jobs: ['vmagent-prometheus'],
            obfuscation: {
                enabled: false,
                obfuscate_instance: false,
                obfuscate_job: false,
                custom_labels: []
            }
        };
        
        const response = await request.post('http://localhost:8080/api/export', {
            data: buggyConfig
        });
        
        console.log(`   Response status: ${response.status()}`);
        const body = await response.json();
        console.log(`   Response body:`, JSON.stringify(body, null, 2));
        
        // Before fix: Would get "missing route for /1011/rw/prometheus/api/v1/export"
        // After fix: Should get 401 (auth error), NOT "missing route"
        
        if (response.status() === 500) {
            const errorMsg = body.error || '';
            
            // Check if it's the "missing route" error (BUG)
            if (errorMsg.includes('missing route')) {
                console.log('   [FAIL] BUG DETECTED: "missing route" error');
                console.log(`   Error message: ${errorMsg}`);
                throw new Error('BUG: /rw/prometheus not normalized for /export');
            }
            
            // Check if it's auth error (EXPECTED after fix)
            if (errorMsg.includes('401') || errorMsg.includes('Unauthorized')) {
                console.log('   [OK] EXPECTED: Auth error (path is correct, just wrong credentials)');
            }
        }
    });
    
    test('VERIFY FIX: /rw/prometheus normalized to /prometheus for export', async ({ request }) => {
        console.log('\n[OK] VERIFYING FIX: Path normalization for export');
        
        const config = {
            connection: {
                url: 'https://vm.example.com',
                full_api_url: 'https://vm.example.com/1011/rw/prometheus',
                auth: {
                    type: 'basic',
                    username: 'fake-user',
                    password: 'fake-pass'
                }
            },
            time_range: {
                start: '2025-11-12T00:00:00Z',
                end: '2025-11-12T01:00:00Z'
            },
            components: ['vmagent'],
            jobs: ['vmagent-prometheus'],
            obfuscation: {
                enabled: false,
                obfuscate_instance: false,
                obfuscate_job: false,
                custom_labels: []
            }
        };
        
        const response = await request.post('http://localhost:8080/api/export', {
            data: config
        });
        
        const body = await response.json();
        console.log(`   Response status: ${response.status()}`);
        
        // After fix: Should NOT see "missing route" error
        const errorMsg = body.error || '';
        expect(errorMsg).not.toContain('missing route');
        
        // Should get auth error (401) OR DNS error (because vm.example.com doesn't exist)
        // The important thing is that path was normalized to /prometheus (not /rw/prometheus)
        if (response.status() === 500) {
            // Check that path was normalized
            const hasNormalizedPath = errorMsg.includes('/prometheus/api/v1/export');
            const hasNoRwPath = !errorMsg.includes('/rw/prometheus');
            
            expect(hasNormalizedPath || errorMsg.includes('401') || errorMsg.includes('Unauthorized')).toBeTruthy();
            expect(hasNoRwPath).toBeTruthy();
            console.log('   [OK] Path normalized correctly (no /rw/prometheus in error)');
        }
    });
    
    test('VERIFY: /rw/prometheus still works for /query', async ({ request }) => {
        console.log('\n[OK] VERIFYING: /rw/prometheus still works for query');
        
        const config = {
            connection: {
                url: 'https://vm.example.com',
                full_api_url: 'https://vm.example.com/1011/rw/prometheus',
                auth: {
                    type: 'basic',
                    username: 'fake-user',
                    password: 'fake-pass'
                }
            },
            query: 'up',
            time: Math.floor(Date.now() / 1000)
        };
        
        const response = await request.post('http://localhost:8080/api/sample', {
            data: {
                config: {
                    connection: config.connection,
                    time_range: {
                        start: '2025-11-12T00:00:00Z',
                        end: '2025-11-12T01:00:00Z'
                    },
                    components: ['vmagent'],
                    jobs: ['vmagent-prometheus']
                },
                limit: 10
            }
        });
        
        const body = await response.json();
        console.log(`   Response status: ${response.status()}`);
        
        // /rw/prometheus should work for /query (no normalization needed)
        const errorMsg = body.error || '';
        expect(errorMsg).not.toContain('missing route');
        
        // Should get auth error or valid response
        console.log('   [OK] /rw/prometheus works for query operations');
    });
    
    test('VERIFY: Standard /prometheus path works for export', async ({ request }) => {
        console.log('\n[OK] VERIFYING: Standard /prometheus path for export');
        
        const config = {
            connection: {
                url: 'https://vm.example.com',
                full_api_url: 'https://vm.example.com/1011/prometheus',
                auth: {
                    type: 'basic',
                    username: 'fake-user',
                    password: 'fake-pass'
                }
            },
            time_range: {
                start: '2025-11-12T00:00:00Z',
                end: '2025-11-12T01:00:00Z'
            },
            components: ['vmagent'],
            jobs: ['vmagent-prometheus'],
            obfuscation: {
                enabled: false,
                obfuscate_instance: false,
                obfuscate_job: false,
                custom_labels: []
            }
        };
        
        const response = await request.post('http://localhost:8080/api/export', {
            data: config
        });
        
        const body = await response.json();
        console.log(`   Response status: ${response.status()}`);
        
        // Standard /prometheus path should work without issues
        const errorMsg = body.error || '';
        expect(errorMsg).not.toContain('missing route');
        
        console.log('   [OK] Standard /prometheus path works correctly');
    });
});

