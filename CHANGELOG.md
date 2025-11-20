# Changelog

All notable changes to VMExporter are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/) and versions adhere to semantic versioning.

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
- VMImport – a companion UI/binary/Docker image that replays VMExporter bundles back into VictoriaMetrics (`cmd/vmimporter`, `internal/importer/server`). Includes tenant-aware endpoint form, drag-and-drop uploader, and unit tests.
- Official Dockerfiles for both utilities with Buildx-compatible multi-arch builds (`build/docker/Dockerfile.vmexporter` and `build/docker/Dockerfile.vmimporter`), plus Make targets to produce amd64+arm64 images in CI.
- Builder script now emits vmexporter **and** vmimporter binaries across the entire platform matrix with combined checksums.
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
