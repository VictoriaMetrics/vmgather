# VMGather Tests

This directory contains all tests for VMGather.

## Test Structure

```
tests/
├── e2e/                    # End-to-End tests (Playwright)
│   ├── specs/              # Test specifications
│   ├── playwright.config.js
│   └── package.json
└── integration/            # Integration tests (Go)
    ├── export_fallback_test.go
    ├── obfuscation_real_test.go
    └── url_parsing_test.go
```

## Test Types

### Unit Tests

Located in the same directories as the code they test (e.g., `internal/domain/*_test.go`).

**Run all unit tests:**
```bash
make test
```

**Run specific package tests:**
```bash
go test ./internal/domain/...
go test ./internal/application/services/...
go test ./internal/infrastructure/vm/...
```

**Coverage:**
- 50+ unit tests
- Cover domain logic, services, VM client, obfuscation, archive writer

### Integration Tests

Located in `tests/integration/`. Test real interactions with VictoriaMetrics.

**Prerequisites:**
- Docker and Docker Compose
- Local test environment running

**Run integration tests:**
```bash
# Start test environment
make test-env-up

# Run integration tests
INTEGRATION_TEST=1 go test ./tests/integration/...

# Stop test environment
make test-env-down
```

**What they test:**
- Export API fallback (query_range when export unavailable)
- Real obfuscation in exported archives
- URL parsing for different VM configurations
- Multi-tenant scenarios

### E2E Tests

Located in `tests/e2e/`. Test the full application flow through the browser.

**Prerequisites:**
- Node.js 18+
- VMGather running locally
- Local test environment (for some tests)

**Run E2E tests:**
```bash
cd tests/e2e
npm install
npm test
```

**Run specific test:**
```bash
npm test -- timezone-support.spec.js
```

**What they test:**
- UI wizard flow
- Component selection
- Obfuscation configuration
- Timezone support
- Export functionality

## Test Environment

VMGather includes a complete Docker-based test environment in `local-test-env/`.

See [local-test-env/README.md](../local-test-env/README.md) for details.

**Quick start:**
```bash
# Start all VictoriaMetrics instances
docker-compose -f local-test-env/docker-compose.test.yml up -d

# Wait for metrics to be collected
sleep 30

# Run tests
make test-scenarios
```

## Test Coverage

### Unit Tests (50+ tests)
- ✅ Domain types and validation
- ✅ VM client (query, export, auth)
- ✅ Obfuscation (instance, job, custom labels)
- ✅ Archive writer (zip creation)
- ✅ Export service (component detection)
- ✅ Timezone conversion

### Integration Tests (10+ tests)
- ✅ Export API fallback mechanism
- ✅ Real obfuscation in archives
- ✅ URL parsing for all VM configurations
- ✅ Multi-tenant support
- ✅ Path normalization

### E2E Tests (35+ tests)
- ✅ Wizard navigation
- ✅ Connection validation
- ✅ Component discovery
- ✅ Sample preview
- ✅ Obfuscation UI
- ✅ Timezone selection
- ✅ Export flow

## CI/CD

Tests run automatically on every push and pull request.

**GitHub Actions workflows:**
- `.github/workflows/main.yml` - Unit tests + Lint
- `.github/workflows/release.yml` - Build binaries on release

**E2E tests are disabled in CI** (require Docker environment). Run locally with:
```bash
make test-env-up
cd tests/e2e && npm test
```

## Writing Tests

### Unit Test Example

```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {
            name:  "valid input",
            input: "test",
            want:  "result",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := MyFunction(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

### E2E Test Example

```javascript
const { test, expect } = require('@playwright/test');

test('should do something', async ({ page }) => {
    await page.goto('http://localhost:8080');
    
    // Interact with UI
    await page.click('button:has-text("Get Started")');
    
    // Assert
    await expect(page.locator('.step-title')).toContainText('Time Range');
});
```

## Troubleshooting

### Integration tests fail with "connection refused"

Make sure the test environment is running:
```bash
docker-compose -f local-test-env/docker-compose.test.yml ps
```

If not running, start it:
```bash
make test-env-up
```

### E2E tests timeout

Increase timeout in `playwright.config.js`:
```javascript
timeout: 60000  // 60 seconds
```

Or run with more workers:
```bash
npm test -- --workers=1
```

### "No metrics available" in tests

Wait longer after starting test environment:
```bash
make test-env-up
sleep 60  # Wait for vmagent to scrape metrics
```

## Best Practices

1. **Unit tests** - Fast, no external dependencies
2. **Integration tests** - Use real Docker environment
3. **E2E tests** - Test critical user flows only
4. **Mock sparingly** - Prefer real dependencies in integration tests
5. **Clear test names** - Describe what is being tested
6. **Table-driven tests** - Use for multiple similar cases
7. **Clean up** - Always clean up test resources

## Resources

- [Go Testing](https://go.dev/doc/tutorial/add-a-test)
- [Playwright Documentation](https://playwright.dev/)
- [VictoriaMetrics API](https://docs.victoriametrics.com/api/)

