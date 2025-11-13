# VMExporter

VMExporter helps export VictoriaMetrics internal metrics for support diagnostics.

## Features

- Web-based wizard interface (6-step flow)
- Automatic VictoriaMetrics component discovery
- Data obfuscation (IP addresses, job names and any other label)
- Multi-tenant support
- Multiple authentication methods (Basic, Bearer, Header, None)
- Cross-platform binaries (Linux, macOS, Windows)
- Single binary, no dependencies

## Quick Start

Download binary for your platform from the releases page.

Run:
```bash
chmod +x vmexporter-*
./vmexporter-darwin-arm64  # macOS Apple Silicon
# or
./vmexporter-linux-amd64   # Linux
# or
vmexporter-windows-amd64.exe  # Windows
```

Opens browser at http://localhost:<RANDOM_PORT>

## Documentation

- [User Guide](docs/user-guide.md)
- [Architecture](docs/architecture.md)
- [Development](docs/development.md)

## Use Cases

VMExporter helps VictoriaMetrics customers provide diagnostic data to support 
while maintaining data privacy through obfuscation.

## Installation

### Binary Downloads

Download pre-built binaries from releases.

Available platforms:
- Linux (amd64, arm64, 386)
- macOS (amd64, arm64)
- Windows (amd64, arm64, 386)

### Building from Source

Requirements: Go 1.21+

```bash
git clone https://github.com/VictoriaMetrics/support.git
cd support
make build
```

## Development

### Testing

Unit tests (50+ tests):
```bash
make test
```

E2E tests (35+ tests, requires Docker):
```bash
# Start test environment
make test-env-up

# Run E2E tests
cd tests/e2e
npm install
npm test

# Or run all scenarios
make test-scenarios

# Stop environment
make test-env-down
```

See [local-test-env/README.md](local-test-env/README.md) for detailed test environment documentation.

**Test Coverage:**
- ✅ URL parsing for all VictoriaMetrics configurations
- ✅ Path normalization for VictoriaMetrics Managed (`/rw/prometheus` → `/prometheus` for export)
- ✅ Authentication (Basic, Bearer, Custom Header)
- ✅ Multi-tenancy support
- ✅ Export functionality with obfuscation
- ✅ Error handling and edge cases

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## Security

See [SECURITY.md](SECURITY.md) for security policy and reporting vulnerabilities.

## License

Apache 2.0 - see [LICENSE](LICENSE) file.

## Support

- GitHub Issues: [Report bugs or request features](https://github.com/VictoriaMetrics/support/issues)
- Email: info@victoriametrics.com
- Slack: [VictoriaMetrics Community](https://slack.victoriametrics.com/)
