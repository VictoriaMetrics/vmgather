# Changelog

All notable changes to vmgather are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/) and versions adhere to semantic versioning.

## [v1.9.0] - Unreleased

### Security
- Docker build toolchain for `vmgather` and `vmimporter` images is upgraded to Go `1.25.7` (from `1.22`) to consume fixed Go stdlib security patches and reduce fixable `CRITICAL/HIGH` CVEs in registry scans.
- Docker runtime images now use `distroless ...:nonroot` with explicit `USER 65532:65532`, removing root-by-default execution.
- Docker publish targets now emit max-mode provenance and SBOM attestations (`--provenance=mode=max --sbom=true`) for release images.
- Docker builder/runtime base images are now pinned by digest in both Dockerfiles for deterministic builds and reduced supply-chain drift from mutable tags.
- Docker builder stages no longer install extra Alpine build packages (`git`, `build-base`), reducing build-time supply-chain surface while keeping `CGO_ENABLED=0` static binaries.
- Added a dedicated GitHub Actions security workflow (`.github/workflows/security.yml`) with `govulncheck`, `hadolint`, and `trivy` gates for Dockerfile misconfig and image CVEs.
- HTTP servers now set explicit hardening timeouts (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`) in both `vmgather` and `vmimporter`.
- Documentation and test examples now use env-based auth placeholders instead of static password-like strings to reduce false positives in secret scanners.

## [v1.8.0] - 2026-02-11

### Added
- Export wizard now lets you configure a separate batch window (auto/preset/custom seconds) independent of metric sampling step.
- Makefile now provides `test-unit-full` for running unit tests without `-short`.
- Makefile now provides `test-all-clean` for running the full suite and then cleaning the Docker test env + volumes (recommended for CI / OrbStack).
- Makefile now provides `manual-env-up/down/clean/logs` for stable local manual testing against a Docker VM stack.

### Changed
- Export batching payload is now built from the batch-window selector instead of forcing batching to match metric step.
- Metric step selector now refreshes the batch-window hint to reflect the current recommendation.
- Playwright E2E now starts a fresh `vmgather` web server by default (opt-in reuse via `PW_REUSE_EXISTING_SERVER=1`).
- Makefile now provides `test-e2e` and `test-all` targets for running Playwright locally.
- Makefile `test-full` now runs unit tests via `test-unit-full` (no `-short`).
- `make test-e2e` now defaults `E2E_REAL=1` and `LIVE_VM_URL` to the local Docker env, so "real" Playwright specs don't get skipped.
- `make test-env-up` auto-recovers from Docker disk-full errors by running `docker system prune -af` and retrying.
- `make test-env-full` now uses `make test-env-clean` instead of global `docker volume prune -f`, avoiding accidental cleanup of unrelated volumes.
- Local test env healthcheck/scenarios are now implemented in Go (`local-test-env/testconfig healthcheck|scenarios`) and invoked by Makefile (shell scripts removed).
- `make test-env-up` now uses isolated compose project names and stops the previously started test project (stored in `local-test-env/.compose-project.test`), avoiding name conflicts and stale-network flakes on OrbStack.
- Local Docker test environment no longer sets fixed `container_name` values and uses tmpfs-backed storage for VM data, preventing disk/volume issues on repeated runs.
- Makefile now provides a `pre-push` target (runs `make test-all-clean`).
- `local-test-env/testconfig healthcheck` now also validates `vmselect-standalone` readiness; integration scenarios include a standalone `vmselect` check.
- Local Docker test environment compose file no longer uses the deprecated `version:` field, removing noisy Docker Compose warnings.
- Documentation now starts with a mode quick choice and uses consistent VMSelect/tenant URL examples across README and the user guide.
- Export output location is now more predictable by default: if `-output` isn't set, vmgather uses `~/Downloads/vmgather` when available (otherwise falls back to `./exports`).

### Fixed
- Frontend batching payload field now matches the backend contract: `custom_interval_seconds` (was `custom_interval_secs`).
- Makefile test targets now preserve `go test` exit codes (piped output no longer masks failures).
- Flaky ExportService streaming unit test no longer times out under parallel load.
- Data races fixed in async job workflows (export job manager and importer upload response snapshot); `make test-race` is now clean.
- Release builds can now inject the correct runtime version via `-ldflags "-X main.version=..."` (both `vmgather` and `vmimporter`).
- Streaming exports no longer fail due to a hard-coded 30s HTTP client timeout; request-scoped context timeouts control export duration.
- Export API availability checks no longer leak HTTP response bodies; connections are closed immediately on success.
- Resumed exports no longer double-count completed batches; progress and ETA remain correct after resume.
- Job filter selectors now escape regex metacharacters (e.g. `.` or `|`) to avoid query corruption and regex injection risks.
- Canceled export jobs are now removed by retention cleanup after the configured retention period.
- Export jobs are no longer canceled by a hard-coded 15-minute manager timeout; only explicit cancel and per-batch timeouts apply.
- `/api/fs/*` endpoints now reject non-localhost requests, reducing the security surface when binding to `0.0.0.0`.
- `/api/download` now blocks symlink escapes outside the export directory (prevents downloading files outside `-output` via symlink).
- Export complete screen now shows where the archive was saved and offers a one-click "Copy" action; suggested download filenames are now Windows-safe via backend-provided `archive_name`.
- Verbose request/diagnostic dumps are now printed only when `-debug` is enabled (`/api/validate`, `/api/discover`, `/api/sample`).
- Obfuscation advanced sections (labels/preview) no longer auto-open by default; sample-loading errors and retries render consistently.
- Playwright E2E no longer intermittently fails with `net::ERR_CONNECTION_REFUSED` on longer runs; the `webServer` timeout is increased to keep the server alive.
- Connection test no longer hangs indefinitely during host reachability precheck; the `/metrics` probe is bounded and always proceeds to backend validation.
- Race-mode tests are now more stable; resume job tests no longer flake under `make test-race`.

## [v1.7.0] - 2026-01-23

### Added
- New collection mode toggle with card flip and dynamic background theme.
- Custom collection step with auto-detected selector vs MetricsQL queries.
- Selector discovery endpoint for job/instance grouping and series estimates.
- Custom-mode E2E coverage for selector and MetricsQL flows.
- Custom-mode label removal controls for export payloads.
- Local test data jobs (`test1`, `test2`) for selector/query validation.
- Experimental oneshot CLI export with optional stdout streaming.

### Changed
- Wizard now adapts steps per mode (cluster vs. custom query).
- Export pipeline supports MetricsQL via forced query_range fallback.
- Step copy updates for selector/query UX and Step 1 mode context.
- Connection step now surfaces a tooltip on the disabled Next button.

## [v1.6.0] - 2026-01-23


### Added
- Validation attempts and final endpoint details are now returned to the UI for connection checks.
- VMSelect auto-enrichment tests in Playwright coverage for base URLs and error cases.
- Local test env now includes a standalone `vmselect` scenario and dynamic ports via env file.
- GetSample now validates empty results and has coverage for 10-job or-filter queries.

### Changed
- Connection validation now retries with `/select/0/prometheus` when the base path is empty or `/prometheus`.
- Playwright e2e now loads `.env.dynamic` and uses env-driven URLs/baseURL for dynamic test ports.
- Sample query selector now uses `or`-groups in `{}` for job filters.

### Fixed
- Connection validation UI now surfaces final endpoint and per-attempt errors for clearer troubleshooting.
- Importer tests now use recent timestamps to avoid retention-window flakiness.
- GetSample no longer returns early with empty results; it now reports a clear error.
- Fix incorrect url format example to connect `vmselect`. See [#18](https://github.com/VictoriaMetrics/vmgather/issues/18).

## [v1.5.0] - 2025-12-12

### Added
- Type-safe Go configuration utility (`testconfig`) for test environments with auto-detection of Docker/local setup and dynamic URL construction.
- Clear test separation: `make test` (unit, no Docker), `make test-integration` (13 scenarios with Docker), `make test-full` (complete suite).
- Configuration inspection targets: `test-config-validate`, `test-config-json`, `test-config-env`.

### Changed
- Replaced shell-based test configuration with type-safe Go implementation.
- Refactored test infrastructure: hardcoded URLs → dynamic configuration via `testconfig`.
- Enhanced VMAuth configuration with additional bearer token entries for tenant 0 and custom headers.
- Consolidated testing documentation in `docs/development.md`.

### Fixed
- Bearer token authentication: fixed shell variable expansion in test scripts (single → double quotes).
- `build-safe` target: changed dependency from `test-full` to `test-race` (no Docker required).
- Testconfig binary: added `config.go` as build prerequisite for automatic rebuilds on changes.
- Test script error handling: added explicit check for `testconfig env` success before eval to prevent silent failures.
- Added pre-flight checks for Docker environment before integration tests.

## [v1.4.1] - 2025-12-05

### Security
- **Path Traversal Fix**: Implemented strict validation for `/api/download` to prevent arbitrary file access.
- **Secure Logging**: Added `--debug` flag and redaction for sensitive data (tokens, passwords) in logs.

### Reliability
- **OOM Prevention**: Refactored `exportViaQueryRange` to use streaming and 1-hour time chunking for large exports.

### Fixed
- **Error Swallowing**: `getSampleDataFromResult` now propagates errors, improving UX diagnostics.

### Changed
- **Release Engineering**: Standardized release process to align with VictoriaMetrics "Local-First" methodology.
    - Added `docs/release-guide.md` with detailed instructions.
    - Updated `Makefile` with `publish-via-docker` target for standardized local publication to Docker Hub and GHCR.
    - Removed Docker publishing from CI (`release.yml`) to enforce local security context.
- **Docker**: Updated official images to use `linux/amd64`, `linux/arm64`, and `linux/arm` (v7) platforms.
- **Build**: Default Go version set to `1.22` for consistency using `alpine` base images.


## [v1.4.0] - 2025-12-03

### Added
- Live discovery coverage against real VictoriaMetrics endpoints: integration (`live_discovery_test.go`) and E2E (`live-discovery.spec.js`) gated by `LIVE_VM_URL`, plus a healthcheck command to verify `vm_app_version` before tests.
- Local test env healthcheck (`local-test-env/testconfig healthcheck`) and published ports for single-node VM (`http://localhost:18428`), with quick-start docs updated.

### Changed
- CI integration job now runs live discovery with `LIVE_VM_URL=http://localhost:18428` and `-tags "integration realdiscovery"`.
- Makefile `test-env-up` prints remapped URLs and runs healthcheck to ensure metrics exist before proceeding.
- Discovery error responses now return a clear message when no VictoriaMetrics component metrics are found.
- Download handler now normalizes paths and restricts downloads to the configured export directory.
- Local dev env port conflicts resolved by remapping vmsingle host ports to 18428/18429 and pinning VM images to v1.129.1.

### Security
- **Path Traversal Fix**: Implemented strict validation for `/api/download` to prevent arbitrary file access.
- **Secure Logging**: Added `--debug` flag and redaction for sensitive data (tokens, passwords) in logs.

### Reliability
- **OOM Prevention**: Refactored `exportViaQueryRange` to use streaming and 1-hour time chunking for large exports.

### Fixed
- **Error Swallowing**: `getSampleDataFromResult` now propagates errors, improving UX diagnostics.
- Discovery failures caused by `/prometheus` base paths now fall back cleanly; missing metrics report a clear reason instead of generic 500.

### Testing
- `./local-test-env/testconfig healthcheck` validates `vm_app_version` availability on single and cluster endpoints.
- `INTEGRATION_TEST=1 go test -tags "integration realdiscovery" ./tests/integration/...` (uses LIVE_VM_URL=http://localhost:18428).
- `npm test` Playwright suite (92 specs; live discovery executed when `LIVE_VM_URL` is set).


## [v1.2.0] - 2025-11-27

### Added
- Importer UI now auto-runs preflight on file drop with visible loader; shows file time range, retention cutoff (UTC), points/skips/drops, and suggested time shift.
- Retention window card on Step 2 with cutoff fetched from target; “Shift to now” and align-first-sample controls display the shifted range before upload.
- New tests: importer skips invalid timestamps during analysis; extra retention/span warning coverage.

### Changed
- Start Import remains disabled until connection is valid, a file is selected, and preflight completes; time-alignment controls stay disabled until analysis finishes.
- Retention trimming is always enabled for imports; drop-old checkbox removed to avoid user errors.
- Step 2 reordered: file selection first (auto analysis), then retention/time-alignment, then batching; preflight button removed.
- README and user-guide updated with importer flow, retention awareness, and time-alignment behaviour.

### Fixed
- Preflight status now shows a spinner (“Validating bundle…”) instead of static text.
- Shift summary and picker stay in sync with suggested shift and manual selection.
- VMImporter tests adjusted to use project-local tmp dir and reuse fixtures to avoid tmp bloat.

## [v0.9.7-beta] - 2025-11-18

### Added
- Summary card on the obfuscation step with per-component and per-job series estimates (backed by new job metrics in discovery API).
- Full-sample obfuscation pipeline: advanced label picker, preview data, and exported ZIP now share the same settings.
- Playwright regression spec for connection validation quirks and IPv4-friendly test helpers for stable CI.

### Changed
- Step 3 help starts collapsed and the URL validator now rejects malformed strings instead of blindly prepending `http://`.
- README, user guide, and architecture notes document the stricter validation, sample handling, and release workflow updates.
- VMAuth integration test uses the production credentials and `/1011/rw/prometheus` path that customers actually hit.

### Fixed
- Sample API responses always include a `name` field and apply obfuscation immediately, eliminating `undefined` labels in the UI.
- Export metadata now records unique components/jobs, UTC timestamps, and the actual binary version.
- `/api/sample` and Playwright error scenarios show consistent loading/error states, keeping the wizard responsive.

## [v0.9.0-beta] - 2025-11-12

### Added
- Embedded 6-step wizard UI with automatic browser launch.
- VictoriaMetrics discovery across vmagent, vmalert, vmstorage, vminsert, vmselect, and vmsingle.
- Deterministic obfuscation for IPs, job labels, and user-selected labels.
- Multi-tenant authentication (Basic, Bearer, VMAuth header passthrough).
- Streaming export through `/api/v1/export` with ZIP packaging and metadata manifest.
- Cross-platform build matrix covering Linux, macOS, and Windows (amd64/arm64/386).

### Testing
- 50+ unit tests across domain logic and infrastructure adapters.
- 31 Playwright E2E tests spanning happy path, auth failures, and retries.
- 14 curated Docker scenarios in `local-test-env` to emulate VictoriaMetrics single/cluster/managed setups.

### Known issues
- Beta quality: API contract may change before v1.0.
- Limited production telemetry; feedback is welcome.
- UI localisation and accessibility are not final.

Please report regressions or feature requests via GitHub issues or info@victoriametrics.com.
## [v1.0.0] - 2025-11-20

### Added
- VMImport – a companion UI/binary/Docker image that replays vmgather bundles back into VictoriaMetrics (`cmd/vmimporter`, `internal/importer/server`). Includes tenant-aware endpoint form, drag-and-drop uploader, and unit tests.
- Official Dockerfiles for both utilities with Buildx-compatible multi-arch builds (`build/docker/Dockerfile.vmgather` and `build/docker/Dockerfile.vmimporter`), plus Make targets to produce amd64+arm64 images in CI.
- Builder script now emits vmgather **and** vmimporter binaries across the entire platform matrix with combined checksums.
- Docker image push automation to GitHub Container Registry (GHCR) in release workflow with versioned and `latest` tags.
- CSS variable system for consistent theming across the application (colors, spacing, typography).
- `data-testid` attributes for deterministic E2E testing (`#startExportBtn`).
- Checkbox spacing (`margin-right: 8px`) for improved visual clarity.
- VMImport now exposes `/api/import/status` so the UI and external tooling can track long-running imports via job IDs.

### Changed
- **UI Modernization**: Complete CSS refactoring with modern color palette (Slate/Blue), Inter font family, refined spacing and borders.
- **Icon Updates**: Replaced all emojis with SVG icons for professional appearance (header, success indicators, lists).
- **Visual Design**: Removed generic gradients, flattened shadows, updated button styles for contemporary aesthetic.
- Help section on Step 3 (Connection) now defaults to collapsed state for cleaner initial view.
- Obfuscation checkbox on Step 5 now defaults to unchecked (correct expected behavior).
- E2E test suite updated to match new UI defaults and button selectors.
- CI workflow (`main.yml`) enhanced with comprehensive build matrix and smoke tests.
- Release workflow (`release.yml`) now includes Docker image builds and pushes to GHCR.
- VMImport import flow moved to a job-based pipeline that unpacks ZIP bundles server-side, streams JSONL in fixed-size chunks, and verifies data before marking jobs complete.
- Import progress UI now shows live stages (uploading, extracting, streaming, verifying), compressed vs inflated size, chunk counters, and sample metric examples for better debugging feedback.

### Fixed
- **Flaky Test**: `TestHandleExportCancel` race condition resolved with proper synchronization (50ms delay, ticker-based retry).
- **UI Regressions**: Help section attribute expectations and button selectors updated in test suite.
- **Obfuscation Defaults**: Restored correct unchecked default state, updated all affected tests to explicitly enable when needed.
- Test suite stability: All 63 E2E tests passing, 0 flaky tests.
- VMImport UI no longer freezes during large uploads; it gracefully handles TLS failures, displays meaningful errors, and prevents duplicate submissions while a job is in flight.

### Documentation
- README now covers Docker usage, VMImport quick start, and the expanded release workflow.
- Architecture and development guides document the importer flow, repository layout updates, and new build commands.
