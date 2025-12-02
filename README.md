# VMGather

VMGather collects VictoriaMetrics internal metrics, obfuscates sensitive data, and produces support-ready bundles in a single binary.

## Table of contents

1. [Highlights](#highlights)
2. [Downloads](#downloads)
3. [Quick start](#quick-start)
4. [VMImport companion](#vmimport-companion)
5. [Documentation set](#documentation-set)
6. [Usage workflow](#usage-workflow)
7. [Privacy & obfuscation](#privacy--obfuscation)
8. [Testing matrix](#testing-matrix)
9. [Build & release](#build--release)
10. [Contributing](#contributing)
11. [Security](#security)
12. [License & support](#license--support)

## Highlights

- **Single binary UI** – embedded web interfaces for both exporter and importer with VictoriaMetrics-style wizards.
- **Automatic discovery** – exporter detects vmagent, vmstorage, vminsert, vmselect, vmalert, and vmsingle instances.
- **Deterministic obfuscation** – configurable anonymisation for IPs, jobs, tenants, and custom labels, applied to samples and exports.
- **Disk-safe staging** – exporter streams batches to a chosen directory so partial files survive crashes or manual interrupts.
- **Adjustable metric cadence** – choose 30s/1m/5m dedup steps per export or override explicitly.
- **Batched exports with ETA** – splits long ranges into windows and shows progress + forecasted completion.
- **Wide auth surface** – Basic, Bearer, custom headers, multi-tenant VMAuth flows; importer forwards tenant headers.
- **Chunked importing** – importer streams VMGather bundles in resumable chunks with post-upload verification.
- **Retention-aware imports** – importer trims samples outside target retention by default and displays cutoff in UTC with auto preflight on file drop.
- **Time alignment helper** – choose “Align first sample” or “Shift to now” to slide bundles into the active retention window before upload.
- **Cross-platform builds** – Linux, macOS, Windows (amd64/arm64/386) with identical CLI flags.
- **First-run ready** – opens browser on launch and guides through validation, sampling, import, and export.

## Downloads

Grab the latest binaries from the [Releases page](https://github.com/VictoriaMetrics/support/releases) or reuse them from CI artifacts.

| Platform | File name pattern | Notes |
| --- | --- | --- |
| Linux | `vmgather-vX.Y.Z-linux-amd64`<br>`vmgather-vX.Y.Z-linux-arm64` | mark executable: `chmod +x vmgather-*` |
| macOS | `vmgather-vX.Y.Z-macos-apple-silicon`<br>`vmgather-vX.Y.Z-macos-intel` | first launch may require “Open Anyway” in System Settings |
| Windows | `vmgather-vX.Y.Z-windows-amd64.exe`<br>`vmgather-vX.Y.Z-windows-arm64.exe` | double-click or run from PowerShell |

VMImport binaries are shipped side-by-side using the same naming scheme: replace the prefix with `vmimporter-…`.

Verify downloads with the published SHA256 hashes:

```bash
sha256sum vmgather-vX.Y.Z-linux-amd64
cat checksums.txt | grep vmgather-vX.Y.Z-linux-amd64
```

## Quick start

### macOS

```bash
chmod +x vmgather-vX.Y.Z-macos-*
open ./vmgather-vX.Y.Z-macos-apple-silicon
# or run from terminal:
./vmgather-vX.Y.Z-macos-apple-silicon
```

When Gatekeeper warns about an unidentified developer: System Settings → Privacy & Security → **Open Anyway**.

### Linux

```bash
chmod +x vmgather-vX.Y.Z-linux-amd64
./vmgather-vX.Y.Z-linux-amd64
```

### Windows

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
.\vmgather-vX.Y.Z-windows-amd64.exe
```

The binary starts an HTTP server and opens a browser window at `http://localhost:8080` (falls back to a free port if busy).

### From source

Requirements: Go 1.21+, Make, Git.

```bash
git clone https://github.com/VictoriaMetrics/support.git
cd support
make build
./vmgather
```

### Docker (vmgather + vmimporter)

Use [Buildx](https://docs.docker.com/build/buildx/) to produce linux/amd64 and linux/arm64 images locally:

```bash
# Build both utilities
make docker-build

# Run VMGather at http://localhost:8080
docker run --rm -p 8080:8080 vmgather:$(git describe --tags --always)

# Run VMImport at http://localhost:8081
docker run --rm -p 8081:8081 vmimporter:$(git describe --tags --always)
```

Set `DOCKER_OUTPUT=type=registry` to push directly to your registry, or override the tags via `docker buildx build -t <registry>/vmgather:tag …`.

Both Dockerfiles live in `build/docker/` and follow distroless best practices (scratch runtime, static binaries).

### CLI flags

Both `vmgather` and `vmimporter` support `-addr` (bind address) and `-no-browser` to skip auto-launching a browser during scripting or Docker-based runs. VMGather's default is `localhost:8080` with automatic fallback to a free port; VMImport defaults to `0.0.0.0:8081` to avoid clashing with VMGather. VMGather also accepts `-output` to choose the directory for generated archives (defaults to `./exports`).

## VMImport companion

VMImport is a sibling utility that consumes VMGather bundles (`.jsonl` or `.zip`) and replays them into VictoriaMetrics via the `/api/v1/import` endpoint. It ships with the same embedded UI/HTTP server approach for parity:

- Reuses the connection card from VMGather, but adds a dedicated **Tenant / Account ID** input so multi-tenant inserts are one click away.
- Drag-and-drop bundle picker triggers an automatic preflight (JSONL sanity, retention cutoff, time range, suggested shift) and displays friendly progress/error states.
- Retention trimming is enabled by default; the UI shows the target cutoff in UTC and the shifted bundle range before upload.
- Time alignment controls stay disabled until analysis finishes; “Shift to now” and suggested-shift buttons ensure the bundle fits the active retention.
- Supports Basic auth, TLS verification toggles, and streaming large files directly to VictoriaMetrics.
- Shares the local-test environment (`local-test-env/`) so you can exercise uploads against the same scenarios used for VMGather.

Run the importer binary directly:

```bash
./vmimporter -addr 0.0.0.0:8081
```

…or use Docker:

```bash
docker run --rm -p 8081:8081 vmimporter:latest
```

The UI exposes the same health endpoint (`/api/health`) as VMGather for container liveness probes.

## Documentation set

- [User guide](docs/user-guide.md) – full wizard walkthrough with screenshots and troubleshooting tips.
- [Architecture](docs/architecture.md) – component diagram, APIs, obfuscation pipeline.
- [Development](docs/development.md) – project structure, targets, lint/test recipes.
- [Local test environment](local-test-env/README.md) – docker-compose environment mirroring VictoriaMetrics setups.

## Usage workflow

Exporter
1. Start the binary – the UI auto-detects an open port.
2. **Connect** to VictoriaMetrics single, cluster, or managed endpoints (`vmselect`, `vmagent`, VMAuth, MaaS paths).
3. **Validate** credentials and detect components.
4. **Preview** metrics via sampling API calls.
5. **Configure obfuscation** for IPs, jobs, or extra labels and review the estimated number of series that will be exported per component/job.
6. **Export** – pick a staging directory + metric step, watch the live progress/ETA (with the current partial file path), and let the backend stream batches to disk before archiving/obfuscating into a final bundle.

Importer
1. Start `./vmimporter` (or Docker) – UI runs at `:8081` by default.
2. **Select bundle** – drop a VMGather `.zip`/`.jsonl` or pick via file dialog.
3. **Endpoint & auth** – enter VictoriaMetrics import URL, tenant/account ID, and auth (Basic or custom header); toggle TLS verify as needed.
4. **Analyze (optional)** – run preflight to see time range, series hints, retention warnings, and sample labels.
5. **Import** – start upload; importer streams in ~512KB chunks, shows progress, and verifies data via `/api/v1/series` after completion. Resume is available if a job fails mid-flight.

See [docs/user-guide.md](docs/user-guide.md) for UI screenshots and parameter descriptions.

## Privacy & obfuscation

- Default mappings mask private networks with `777.777.x.x` while preserving ports for debugging.
- Job names retain component prefixes (`vmstorage-job-1`) for observability without exposing tenant names.
- Custom labels are mapped deterministically; mappings stay in memory and are not written to the archive.
- Obfuscation settings apply to previews and exports; obfuscated mappings are included in archive metadata for support correlation.
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

`make build` compiles both `./vmgather` and `./vmimporter`. `make build-all` produces the full cross-platform matrix for *each* binary in `dist/` and writes a combined `checksums.txt`. Update [CHANGELOG.md](CHANGELOG.md) before tagging and attach the generated artifacts to the GitHub release.

Docker images follow the same release train. Use `make docker-build` (or the per-app targets) to create multi-architecture images via Buildx. Override `PLATFORMS`, `VERSION`, or `DOCKER_OUTPUT` when pushing to your own registry.

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
