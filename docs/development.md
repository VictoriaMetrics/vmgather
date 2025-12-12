# Development guide

Step-by-step instructions for hacking on vmgather like any other VictoriaMetrics project.

## Prerequisites

- Go 1.21 or newer,
- GNU Make,
- Docker (integration and scenario tests),
- Node.js 18+ with npm (Playwright UI tests).

## Bootstrap

```bash
git clone https://github.com/VictoriaMetrics/vmgather.git
cd vmgather
make build           # compiles ./vmgather and ./vmimporter
./vmgather         # launches the export wizard
./vmimporter         # launches the bundle uploader
```

`make deps` installs JS dependencies for E2E tests if needed.

## Repository layout

```
cmd/vmgather/           - Export wizard entry point
cmd/vmimporter/           - Bundle uploader entry point
internal/server/          - Exporter HTTP server + embedded UI
internal/importer/server/ - Importer HTTP server + embedded UI
internal/application/     - services orchestrating validation/export flows
internal/infrastructure/  - VictoriaMetrics client, obfuscation, archive writer
internal/domain/          - shared structs/enums
docs/                     - public documentation
local-test-env/           - docker-compose scenarios
tests/e2e/                - Playwright specs
dist/                     - build outputs (ignored)
```

## Typical workflow

1. Implement feature/fix inside `internal/...` or `cmd/vmgather/`.
2. Update/extend unit tests near the code.
3. Run checks:

```bash
make lint
make test
```

4. For changes affecting flows, run:

```bash
INTEGRATION_TEST=1 go test ./tests/integration/...
make test-e2e
```

5. Update docs/CHANGELOG as needed and send a PR.

## Testing commands

### Tests WITHOUT Docker (fast)

| Command | Purpose |
| --- | --- |
| `make test` | Go unit tests - **no Docker required** |
| `make test-fast` | Skip slow tests |
| `make test-coverage` | Generate coverage report |

**Use for:** Quick validation during development, CI pipelines without Docker.

### Tests WITH Docker (comprehensive)

| Command | Purpose |
| --- | --- |
| `make test-integration` | Binary tests Docker environment (13 scenarios) |
| `make test-env-up` | Start Docker environment |
| `make test-env-down` | Stop Docker environment |

**Use for:** Full validation before merge, testing real VM scenarios.

### Complete test suite

| Command | Purpose |
| --- | --- |
| `make test-full` | Everything: unit tests + Docker + integration |

**Use for:** Final validation, release candidate testing.

### Test configuration

Test environment uses type-safe Go configuration (`local-test-env/config.go`):
- No manual environment variables needed
- Automatic port detection
- Dynamic URL construction
- Validates before running

```bash
# View current test configuration
make test-config-json

# Validate configuration
make test-config-validate

# Override if needed (optional)
export VM_SINGLE_NOAUTH_URL=http://custom:18428
make test-scenarios
```

### E2E tests (Playwright)

```bash
cd tests/e2e
npm install
npm test
```

Requires `make test-env-up` running in background.

## Code style

- Follow [Effective Go](https://go.dev/doc/effective_go) and VictoriaMetrics' standard lints.
- Keep exported functions documented; rely on table-driven tests.
- Use vanilla JS/ES6 for frontend, mirroring other VictoriaMetrics UI utilities.
- Avoid introducing new dependencies without prior discussion.

## Release builds

```bash
make build             # Local platform (vmgather + vmimporter)
make build-all         # Cross-platform binaries + checksums
make docker-build      # Multi-arch Docker images
```

Artifacts are placed in `dist/`. See [release-guide.md](release-guide.md) for publishing instructions.

## Debug tips

- `VMGATHER_LOG=debug ./vmgather` enables verbose logging (see environment variables in code).
- Use the docker scenarios in `local-test-env` to reproduce customer issues offline.
- Browser dev tools help inspect API payloads when reproducing UI bugs.
