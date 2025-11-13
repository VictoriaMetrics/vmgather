# Contributing to VMExporter

Thank you for your interest in contributing to VMExporter!

## Reporting Issues

Please use GitHub Issues to report bugs or request features.

When reporting bugs, include:
- VMExporter version
- Operating system and architecture
- VictoriaMetrics version
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs

## Development Setup

### Prerequisites

- Go 1.21 or later
- Make
- Docker (for integration tests)

### Building

```bash
# Clone repository
git clone https://github.com/VictoriaMetrics/support.git
cd vmexporter

# Build
make build

# Run tests
make test

# Run E2E tests
make test-e2e
```

### Project Structure

```
cmd/vmexporter/        - Main application entry point
internal/domain/       - Domain models and types
internal/application/  - Business logic services
internal/infrastructure/ - External integrations (VM client, obfuscation, archive)
internal/server/       - HTTP server and API
tests/e2e/            - End-to-end tests (Playwright)
```

### Code Style

- Follow standard Go conventions
- Add tests for new functionality
- Update CHANGELOG.md for significant changes
- Keep commits focused and atomic

### Pull Requests

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add/update tests
5. Ensure all tests pass
6. Update documentation if needed
7. Submit pull request

## Questions?

For questions, please open a GitHub Issue or contact info@victoriametrics.com

## License

By contributing, you agree that your contributions will be licensed under Apache 2.0.

