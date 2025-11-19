# VMExporter

VMExporter collects VictoriaMetrics internal metrics, obfuscates sensitive data, and produces support-ready bundles in a single binary.

## Table of contents

1. [Highlights](#highlights)
2. [Downloads](#downloads)
3. [Quick start](#quick-start)
4. [Documentation set](#documentation-set)
5. [Usage workflow](#usage-workflow)
6. [Privacy & obfuscation](#privacy--obfuscation)
7. [Testing matrix](#testing-matrix)
8. [Build & release](#build--release)
9. [Contributing](#contributing)
10. [Security](#security)
11. [License & support](#license--support)

## Highlights

- **Single binary UI** – embedded web interface with a VictoriaMetrics-style 6-step wizard.
- **Automatic discovery** – detects vmagent, vmstorage, vminsert, vmselect, vmalert, and vmsingle instances.
- **Deterministic obfuscation** – configurable anonymisation for IPs, jobs, tenants, and custom labels.
- **Disk-safe staging** – streams export batches to a user-selected directory so partial files survive crashes or manual interrupts.
- **Adjustable metric cadence** – pick 30s/1m/5m deduplication steps per export to trim payload size on slow environments.
- **Batched exports with ETA** – splits long ranges into 30s/1m/5m windows and shows progress + forecasted completion.
- **Wide auth surface** – Basic, Bearer, custom headers, and multi-tenant VMAuth flows.
- **Cross-platform builds** – Linux, macOS, Windows (amd64/arm64/386) with identical CLI flags.
- **First-run ready** – opens browser on launch and guides through validation, sampling, and export.

## Downloads

Grab the latest binaries from the [Releases page](https://github.com/VictoriaMetrics/support/releases) or reuse them from CI artifacts.

| Platform | File name pattern | Notes |
| --- | --- | --- |
| Linux | `vmexporter-vX.Y.Z-linux-amd64`<br>`vmexporter-vX.Y.Z-linux-arm64` | mark executable: `chmod +x vmexporter-*` |
| macOS | `vmexporter-vX.Y.Z-macos-apple-silicon`<br>`vmexporter-vX.Y.Z-macos-intel` | first launch may require “Open Anyway” in System Settings |
| Windows | `vmexporter-vX.Y.Z-windows-amd64.exe`<br>`vmexporter-vX.Y.Z-windows-arm64.exe` | double-click or run from PowerShell |

Verify downloads with the published SHA256 hashes:

```bash
sha256sum vmexporter-vX.Y.Z-linux-amd64
cat checksums.txt | grep vmexporter-vX.Y.Z-linux-amd64
```

## Quick start

### macOS

```bash
chmod +x vmexporter-vX.Y.Z-macos-*
open ./vmexporter-vX.Y.Z-macos-apple-silicon
# or run from terminal:
./vmexporter-vX.Y.Z-macos-apple-silicon
```

When Gatekeeper warns about an unidentified developer: System Settings → Privacy & Security → **Open Anyway**.

### Linux

```bash
chmod +x vmexporter-vX.Y.Z-linux-amd64
./vmexporter-vX.Y.Z-linux-amd64
```

### Windows

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
.\vmexporter-vX.Y.Z-windows-amd64.exe
```

The binary starts an HTTP server and opens a browser window at `http://localhost:<random-port>`.

### From source

Requirements: Go 1.21+, Make, Git.

```bash
git clone https://github.com/VictoriaMetrics/support.git
cd support
make build
./vmexporter
```

## Documentation set

- [User guide](docs/user-guide.md) – full wizard walkthrough with screenshots and troubleshooting tips.
- [Architecture](docs/architecture.md) – component diagram, APIs, obfuscation pipeline.
- [Development](docs/development.md) – project structure, targets, lint/test recipes.
- [Local test environment](local-test-env/README.md) – docker-compose environment mirroring VictoriaMetrics setups.

## Usage workflow

1. Start the binary – the UI auto-detects an open port.
2. **Connect** to VictoriaMetrics single, cluster, or managed endpoints (`vmselect`, `vmagent`, VMAuth, MaaS paths).
3. **Validate** credentials and detect components.
4. **Preview** metrics via sampling API calls.
5. **Configure obfuscation** for IPs, jobs, or extra labels and review the estimated number of series that will be exported per component/job.
6. **Export** – pick a staging directory + metric step, watch the live progress/ETA (with the current partial file path), and let the backend stream batches to disk before archiving/obfuscating into a final bundle.

See [docs/user-guide.md](docs/user-guide.md) for UI screenshots and parameter descriptions.

## Privacy & obfuscation

- Default mappings mask private networks with `777.777.x.x` while preserving ports for debugging.
- Job names retain component prefixes (`vmstorage-job-1`) for observability without exposing tenant names.
- Custom labels are mapped deterministically; mappings stay in memory and are not written to the archive.
- No credentials or temporary archives persist to disk after the process ends.

## Testing matrix

| Layer | Command | Notes |
| --- | --- | --- |
| Unit tests | `make test` | 50+ packages cover domain, client, and obfuscation logic |
| Coverage | `make test-coverage` | produces `coverage.out` |
| Integration | `INTEGRATION_TEST=1 go test ./tests/integration/...` | runs against Dockerized VictoriaMetrics flavours |
| E2E (Playwright) | `make test-e2e` | UI happy path + failure cases |
| Scenario suite | `make test-scenarios` | executes the curated cases from `local-test-env/test-configs` |

See [docs/development.md](docs/development.md) and [local-test-env/README.md](local-test-env/README.md) for detailed instructions.

## Build & release

`make build-all` produces cross-platform binaries in `dist/` and a `checksums.txt` file. Release artifacts follow the naming scheme used across VictoriaMetrics products. Update [CHANGELOG.md](CHANGELOG.md) before tagging and attach the generated binaries to the GitHub release.

## Contributing

We follow the same workflow as other VictoriaMetrics repositories:

1. Discuss large features in an issue first.
2. Create a branch, implement the change, and add tests.
3. Run `make lint`, `make test`, and applicable scenario suites.
4. Update docs/CHANGELOG when behaviour changes.
5. Submit a pull request using the provided template.

More in [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See the project's [SECURITY.md](SECURITY.md) for reporting instructions.

## License & support

- License: [Apache 2.0](LICENSE)
- Issues: https://github.com/VictoriaMetrics/support/issues
