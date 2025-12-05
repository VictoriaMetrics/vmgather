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

| Command | Purpose |
| --- | --- |
| `make test` | Run Go unit tests with race detector disabled (faster for CI). |
| `make test-coverage` | Produce `coverage.out`. |
| `INTEGRATION_TEST=1 go test ./tests/integration/...` | Exercises VM client against dockerised VictoriaMetrics. |
| `go test ./internal/importer/server` | Quick importer-only tests. |
| `make test-env-up` / `make test-env-down` | Start/stop the `local-test-env`. |
| `make test-scenarios` | Runs the curated scenario script across VM flavours. |
| `make test-e2e` | Playwright UI suite (requires local-test-env). |

## Code style

- Follow [Effective Go](https://go.dev/doc/effective_go) and VictoriaMetrics' standard lints.
- Keep exported functions documented; rely on table-driven tests.
- Use vanilla JS/ES6 for frontend, mirroring other VictoriaMetrics UI utilities.
- Avoid introducing new dependencies without prior discussion.

## Release builds

```bash
make build             # local platform (vmgather + vmimporter)
make build-all         # linux/macos/windows matrices + checksums for both binaries
make docker-build      # buildx multi-arch images (linux/amd64 + linux/arm64)
```

Artifacts are placed in `dist/` and uploaded to GitHub releases. Docker images land in your local Buildx cache (or registry when `DOCKER_OUTPUT=type=registry`). Ensure [CHANGELOG.md](../CHANGELOG.md) reflects user-visible changes before tagging.

## Debug tips

- `VMGATHER_LOG=debug ./vmgather` enables verbose logging (see environment variables in code).
- Use the docker scenarios in `local-test-env` to reproduce customer issues offline.
- Browser dev tools help inspect API payloads when reproducing UI bugs.
