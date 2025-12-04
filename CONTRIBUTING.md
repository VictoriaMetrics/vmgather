# Contributing to vmgather

Thanks for helping improve the VictoriaMetrics toolchain! The guidelines below mirror the workflow we use across other VictoriaMetrics repositories.

## Report bugs and feature requests

- Use [GitHub Issues](https://github.com/VictoriaMetrics/vmgather/issues) for all defects, regressions, and feature proposals.
- Please attach:
  - vmgather version (binary name or git commit),
  - OS / architecture,
  - VictoriaMetrics flavour (single / cluster / managed) and version,
  - Exact repro steps and expected behaviour,
  - Logs or screenshots if applicable.

Security-sensitive problems should be reported privately via `info@victoriametrics.com`. See [SECURITY.md](SECURITY.md).

## Development environment

Prerequisites:

- Go 1.21 or newer,
- GNU Make,
- Docker (integration tests),
- Node.js 18+ and npm (E2E UI tests).

Bootstrap:

```bash
git clone https://github.com/VictoriaMetrics/vmgather.git
cd vmgather
make deps        # optional helper to download UI deps
make build
```

`./vmgather` starts the local UI on a random port.

## Project layout

```
cmd/vmgather/             - CLI entry point
internal/server/            - HTTP server + embedded static assets
internal/application/       - services orchestrating validation/export
internal/infrastructure/    - VictoriaMetrics client, obfuscation, archive writer
internal/domain/            - shared types and config
tests/e2e/                  - Playwright specs
local-test-env/             - docker-compose for VictoriaMetrics scenarios
docs/                       - public documentation
dist/                       - release artifacts (generated)
```

## Coding standards

- Run `make lint` to execute Go linters consistent with VictoriaMetrics defaults.
- All new functionality must include unit tests; UI changes should include E2E coverage when practical.
- Update [docs](docs/) and [CHANGELOG.md](CHANGELOG.md) when observable behaviour changes.
- Keep commits focused; large features should be split into logical pieces.

## Testing checklist

```bash
make test             # Go unit tests
INTEGRATION_TEST=1 go test ./tests/integration/...
make test-e2e         # Playwright (requires local-test-env)
make test-scenarios   # runs curated Docker scenarios
```

Use `local-test-env/README.md` to spin up VictoriaMetrics flavours before E2E/integration runs.

## Pull requests

1. Fork and create a topic branch (`feature/export-improvements`).
2. Implement the change with tests.
3. Run the relevant test suites and linting commands.
4. Update docs/CHANGELOG as needed.
5. Fill out `.github/PULL_REQUEST_TEMPLATE.md` and submit a PR.

Maintainers will review for correctness, style, and release impact. We may ask for additional tests or documentation before merging.

## License

By contributing, you agree that your work is licensed under the [Apache 2.0 License](LICENSE), the same as the rest of the project.
