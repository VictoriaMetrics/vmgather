# Development Guide

## Prerequisites

- Go 1.21 or later
- Make
- Docker (for integration tests)
- Node.js 18+ (for E2E tests)

## Getting Started

### Clone Repository

```bash
git clone https://github.com/VictoriaMetrics/support.git
cd vmexporter
```

### Build

```bash
make build
```

Binary created at `./vmexporter`

### Run

```bash
./vmexporter
```

Opens browser at http://localhost:8080

## Project Structure

```
vmexporter/
├── cmd/vmexporter/          # Main application entry point
├── internal/
│   ├── domain/              # Domain models and types
│   ├── application/         # Business logic services
│   │   └── services/        # VMService, ExportService
│   ├── infrastructure/      # External integrations
│   │   ├── vm/              # VictoriaMetrics client
│   │   ├── obfuscation/     # Data obfuscation
│   │   └── archive/         # ZIP archive creation
│   └── server/              # HTTP server and API
│       └── static/          # Web UI (embedded)
├── tests/e2e/               # End-to-end tests (Playwright)
├── local-test-env/          # Docker test environment
├── docs/                    # Documentation
└── dist/                    # Build artifacts
```

## Development Workflow

### 1. Make Changes

Edit code in `internal/` or `cmd/`

### 2. Run Tests

```bash
# Unit tests
make test

# With coverage
make test-coverage

# E2E tests
make test-e2e

# All tests
make test-all
```

### 3. Lint

```bash
make lint
```

### 4. Build

```bash
make build
```

### 5. Test Locally

```bash
./vmexporter
```

## Testing

### Unit Tests

Located in `*_test.go` files next to source code.

Run specific package:
```bash
go test ./internal/domain
go test ./internal/infrastructure/vm
```

### E2E Tests

Located in `tests/e2e/specs/`

Run:
```bash
cd tests/e2e
npm install
npm test
```

Run specific test:
```bash
npm test -- navigation.spec.js
```

### Integration Tests

Docker-based test environment with 14 scenarios.

Start environment:
```bash
make test-env-up
```

Run all scenarios:
```bash
make test-scenarios
```

Stop environment:
```bash
make test-env-down
```

## Code Style

### Go Conventions

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` for formatting
- Add GoDoc comments for exported symbols
- Keep functions focused and small
- Use table-driven tests

### Example

```go
// Client provides methods to interact with VictoriaMetrics API.
type Client struct {
    httpClient *http.Client
    conn       domain.VMConnection
}

// NewClient creates a new VictoriaMetrics API client.
func NewClient(conn domain.VMConnection) *Client {
    return &Client{
        httpClient: &http.Client{Timeout: 30 * time.Second},
        conn:       conn,
    }
}
```

### JavaScript (Frontend)

- Vanilla JS (no frameworks)
- ES6+ syntax
- Clear function names
- Comments for complex logic

## Building for Release

### Single Platform

```bash
make build
```

### All Platforms

```bash
make build-all
```

Creates binaries in `dist/` for:
- Linux (amd64, arm64, 386)
- macOS (amd64, arm64)
- Windows (amd64, arm64, 386)

Also generates `checksums.txt` with SHA256 hashes.

## Debugging

### Backend Logs

Server logs to stdout with structured format:
```
2025/11/12 10:00:00 🔌 Validating connection: http://localhost:8428
2025/11/12 10:00:01 ✅ Connection successful! Components found: 1
```

### Frontend Logs

Open browser console (F12) to see detailed logs:
```javascript
console.group('🔌 Multi-Stage Connection Test');
console.log('📋 Connection Config:', config);
console.groupEnd();
```

### Test Debugging

Run tests with verbose output:
```bash
go test -v ./internal/...
```

Playwright debug mode:
```bash
cd tests/e2e
PWDEBUG=1 npm test
```

## Common Tasks

### Add New API Endpoint

1. Add handler in `internal/server/server.go`
2. Add route in `setupRoutes()`
3. Add tests
4. Update API documentation

### Add New Obfuscation Type

1. Add logic in `internal/infrastructure/obfuscation/obfuscator.go`
2. Add tests in `obfuscator_test.go`
3. Update UI in `internal/server/static/`
4. Update documentation

### Update Frontend

1. Edit `internal/server/static/index.html` or `app.js`
2. Rebuild: `make build`
3. Test: `./vmexporter`
4. Add E2E tests in `tests/e2e/specs/`

## Troubleshooting

### Build Fails

```bash
# Clean and rebuild
make clean
make build
```

### Tests Fail

```bash
# Check dependencies
go mod tidy

# Run specific test
go test -v -run TestName ./internal/...
```

### E2E Tests Fail

```bash
# Ensure test environment is running
make test-env-up

# Check Docker containers
docker ps

# View logs
make test-env-logs
```

## Resources

- [Go Documentation](https://go.dev/doc/)
- [VictoriaMetrics API](https://docs.victoriametrics.com/Single-server-VictoriaMetrics.html#prometheus-querying-api-usage)
- [Playwright Docs](https://playwright.dev/)

## Questions?

- Open GitHub Issue
- Email: info@victoriametrics.com
- Slack: https://slack.victoriametrics.com/

