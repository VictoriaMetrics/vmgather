# Changelog

All notable changes to vmgather are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/) and versions adhere to semantic versioning.

## [v1.13.0] - 2026-11-21

### Added
- **Experimental** snapshot mode (server mode) for teams that cannot send MDX: `-snapshot.dst=s3://bucket/prefix` exports the MDX metrics of VictoriaMetrics components in fixed, aligned, half-open windows (`-snapshot.period`, default 1h) and uploads every window with data to S3, GCS, Azure Blob or a local path; empty windows are skipped and do not block later ones. It runs once for a Kubernetes CronJob or in a loop with `-snapshot.interval`. The state is kept in the bucket and moves only after a complete upload, so a failed or interrupted window is exported again; `-snapshot.start`/`-snapshot.end` backfill a fixed range without touching the state. Unless jobs are set, the components are discovered for every window. See README, "Snapshot mode (experimental)".
- **Experimental** snapshot mode reuses VictoriaMetrics code: the vmagent `-remoteWrite.mdx.enable` filter (`lib/mdx`) decides which series are exported, and the vmbackup storage layer (`lib/backup`) uploads them, with the same destinations, credentials and flags (`-customS3Endpoint`, `-credsFilePath`, ...). A snapshot uses the vmbackup layout and can be downloaded with vmrestore.
- **Experimental** `-snapshot.retention` (default `30d`) deletes old snapshots, including incomplete uploads, after every scheduled run; the local work directory is emptied before and after every run, so failed runs do not fill the disk.
- **Experimental** `mdx_filter` export option (cluster mode, used by snapshot mode) and `mdx_filtered` in `metadata.json`.
- `-version` prints the vmgather version.
- `VMGATHER_VM_PASSWORD`, `VMGATHER_VM_BEARER_TOKEN` and `VMGATHER_VM_HEADER_VALUE` override the auth fields of the oneshot and snapshot config, for Kubernetes Secrets (oneshot and snapshot mode are experimental).
- **Experimental** Kubernetes manifests in `deploy/k8s/`: a snapshot CronJob and a one-time Job that exports MDX metrics from a vmbackup backup (vmrestore into a temporary vmsingle, then a backfill).
- `-allowed-hosts host1,host2` flag in vmgather and vmimporter: extra Host names accepted from clients on the same machine, for a local reverse proxy.
- A failed export can be resumed from the UI (before, only a canceled one). Completed batches are kept; the main button still starts a new export with the current selection.
- vmimporter removes finished jobs 1 hour after they end, together with the upload kept for resuming a failed job, and removes kept uploads on shutdown. The status of a job is not available after it is removed.

### Changed
- Shorter, plainer texts in the vmgather and vmimporter UI: no status tags such as `[OK]` or `[INFO]`, a short check and import summary in vmimporter (retention as "100 years", label limit as "14 of 40"), and technical details under "Details".
- Obfuscation is the vmagent `-remoteWrite.obfuscateLabels` algorithm: a value becomes its hex SHA-256 digest, the same in every export, resumed export, snapshot and MDX stream. It replaces the `777.777.x.y` addresses, `vm_component_<name>_<n>` jobs and `<label>-<n>` values, which were different in every export. Ports and component names are no longer visible in obfuscated labels, and there is no salt: values with a small space such as `IP:port` can be recovered by brute force (drop such labels if they must not leave your network).
- Auto batch window is 1 hour (was 30s/1m/5m). `/api/v1/export` repeats every label set once per window, so the old windows multiplied the payload. On a small lab (2 jobs, 6 hours) the export took 0.6-0.9s and 1.7-1.9 MB instead of 9.5-14.4s and 15.8-16.5 MB, with identical samples.
- `/api/v1/export` requests have no total deadline any more. A request is canceled when no data arrives for 2 minutes; a stream that stalls after some data is split by time, a target that never answers fails after one idle period.
- Export writes batches straight to the staging file and rolls back a failed attempt with truncate, so data is no longer copied twice (1 GB export: 4.6-6.8s to 2.0s in a local test).
- vmimporter passes sample values through as JSON without a float round trip (about 1.6x faster in benchmarks).
- vmimporter streams uploads to a temp file instead of keeping up to 512 MB in memory (peak RSS 1076 MB to 19 MB for a 419 MB bundle) and reads zip bundles in place instead of extracting them (a 9 MB zip used 1 GB of temp space).
- Discovery finds the components that had `vm_app_version` at any time in the selected range (`last_over_time` over the range), not only at its end, so a component that stopped before the end is no longer left out. Instant queries send `deny_partial_response=1`, so a cluster with an unavailable vmstorage fails discovery instead of returning part of the jobs.
- Discovery names components by their full name (`victoria-metrics`, `victoria-logs`, `config-reloader`) instead of merging VictoriaMetrics and VictoriaLogs into `victoria`.
- Cluster exports without jobs and custom exports without a query are rejected with HTTP 400 (also in CLI oneshot) instead of exporting everything.
- The metric sampling step applies only to MetricsQL (query_range) exports; the UI says so. Selector and cluster exports always keep raw samples.
- Build, release binaries, Docker images and CI use Go 1.27.4; building from source needs Go 1.27.3 or newer, which the VictoriaMetrics module v1.156.0 requires. Docker images run on distroless `static-debian12` (static binaries, no OpenSSL). `make security-check` runs govulncheck v1.8.0 on the same Go version as CI; lint uses golangci-lint v2.14.0. CI tests Go 1.27 only (was 1.24 to 1.26).
- The vmgather binary grows from 8.5 MB to 54 MB (linux/amd64, stripped), because `lib/backup` brings the AWS, GCS and Azure SDKs. vmimporter does not change (7.9 MB).

### Fixed
- Cluster mode: components with a single job were left out of the export, and selecting only such components exported the whole database (`{__name__!=""}`): up to the 10M series limit on large installs, all tenant metrics on small ones.
- 502/503 and broken connections failed the whole export. They are retried up to 3 times (after 2s, 8s, 30s). A 504 from a proxy (nginx, vmauth) splits the window by time.
- Resume with obfuscation gave different real instances the same obfuscated value, and `metrics_count` counted only batches after resume. Resume also skips straight to the archive when all batches are done and cuts a partly written batch from the staging file.
- A sample exactly on a batch window border was exported twice (up to 50% duplicates for aligned data such as vmalert recording rules). Windows end 1ms before the next one starts, requests use millisecond precision, and query_range splits stay on the step grid.
- The query_range fallback wrote one line per sample and failed on NaN or Inf. It writes one line per series per chunk, NaN as `null` and Inf as `"Infinity"`, like `/api/v1/export`.
- Export lines longer than 1 MB failed with `token too long`. VictoriaMetrics writes up to 8192 rows per line and prints large values in full (2.6 MB lines with 1-hour windows); the limit is now 16 MB.
- vmimporter applied the time shift after retention trimming, so "Shift to now" imported 0 points, and ignored `drop_old=false` on import.
- vmimporter dropped a whole series line when one value was `null`, `"Infinity"` or `"NaN"`.
- vmimporter resume offsets were wrong for CRLF files, so some lines were sent twice.
- vmimporter verification cut the `/api/v1/series` answer at 64 KB, did not escape quotes and backslashes in label values, used the unshifted time range after a shift, and gave up before new series became searchable (3-11s on VictoriaMetrics v1.155.0). It now waits up to 30s, retries only an empty answer, 5xx and network errors, and reports a 4xx answer at once.
- vmimporter left the upload and the extracted file of a failed import in the temp dir forever.
- 9 e2e specs were broken since the autopilot UI (v1.10.0).
- `make test-env-up` and `make manual-env-up` ran `docker system prune -af` and `docker builder prune -af` when Docker reported "no space left on device", which deleted the images, build cache and stopped containers of every project on the machine. They now stop only their own compose project and fail with a message.
- The local e2e stand used `latest` images, and the current Prometheus image could not start on its tmpfs. The images are pinned now and the tmpfs is writable.
- `metadata.json` listed the real job names when the job label was obfuscated. They are obfuscated the same way now.
- The UI said that obfuscation mappings are included in `metadata.json`; they never were, and with digests there is no mapping.
- vmimporter marked every clean bundle with "Preflight: issues detected", because the default sampled check counted as a warning.
- vmimporter showed "Retention unknown" for a target with a very long retention (for example `-retentionPeriod=100y`, a cutoff before 1970).
- Both UIs requested a missing `/favicon.ico`, so every page load logged a browser console error.
- Two e2e checks used `/[FAIL]/` and `/[OK]/` regular expressions, which match any text with one of those letters; they now check the hint state.

### Removed
- The unused `split_by_metric_name` safety option.
- The vmimporter "Metric sampling step" selector: it was only saved in the profile and had no effect on the import.
- The fake obfuscation formats (`777.777.x.y`, `vm_component_<name>_<n>`, `<label>-<n>`) and the internal `instance_map`/`job_map` metadata fields, which were never written to archives.

### Security
- Go 1.27.4: govulncheck finds no vulnerabilities. Go 1.26.9, used for v1.12.0, has GO-2026-6859 (net/http) and GO-2026-6809 (crypto/x509). Docker images were built with Go 1.26.9 by digest although the Makefile said 1.25.11.
- `golang.org/x/crypto` v0.59.0 removes GO-2026-5932 (unmaintained `openpgp`, found at module level; vmgather does not call it).
- The distroless `static-debian12` runtime removes CVE-2026-84782 (HIGH, libssl3) reported for the previous `base-debian12` image.
- vmgather and vmimporter reject cross-origin state-changing requests (CSRF): another local page or a local HTML file could start exports, create directories or push data into a VictoriaMetrics reachable from the browser.
- Clients on the same machine must use `localhost`, a `*.localhost` name, an IP address or a name from `-allowed-hosts`, which blocks DNS rebinding (a page could read directory listings). It does not cover Docker port mapping or a rebinding to a LAN address of a server listening on `0.0.0.0`; the cross-origin check still applies there.

## [v1.12.0] - 2026-10-09

### Changed
- Optimize JSONL exports with passthrough, fastjson label processing and larger I/O buffers. Unmodified exports check only record shape and trust VictoriaMetrics JSON.
- Respect Linux container CPU quotas in both binaries unless `GOMAXPROCS` is set. Reuse vmimporter's HTTP client in `skip_tls_verify` mode.
- Add `connection.disable_compression` for exports and queries. Gzip remains enabled by default.
- Add unauthenticated `/debug/pprof/` endpoints with vmgather's `-debug` flag.
- Require Go 1.24+ for source builds. Use Go 1.26.9 for release binaries, Docker builders and CI security scans.
- Keep raw records out of passthrough errors and reject non-string labels during obfuscation.
- Update CI linting, vulnerability checks and Go compatibility coverage. Add export benchmarks and regression tests.

## [v1.11.0] - 2026-06-12

### Added
- Adaptive export now emits structured diagnostics for every attempt and retry decision (`[EXPORT][ATTEMPT]`, `[EXPORT][ADAPTIVE] decision=query_range_fallback|split_by_job|split_by_time|increase_step`), tagging strategy, depth, error kind, sampling step, selected jobs, time window, and selector so support can trace how autopilot reacted to a given VictoriaMetrics rejection.
- Added `logAdaptiveFailure`, which explains why an adaptive export ultimately failed (`too_many_series` vs `query_timeout` vs other), including the unrecoverable configuration context, instead of surfacing only the raw error.
- Per-job adaptive exports now log retry progress (`[EXPORT][JOB] adaptive-retry … retries/kind/strategy/step/range`) and the final failing context, making multi-job autopilot runs diagnosable.
- Discovery/estimation now produces explicit warnings (`componentEstimateWarnings`, `selectorEstimateWarnings`) when per-component or per-selector series estimates are unavailable, and surfaces the failing error kind to the UI.

### Security
- Build, runtime, and security-scan Go toolchains are upgraded to Go `1.25.11` (from `1.25.9`) to consume the latest Go standard library vulnerability fixes.
- Docker runtime images now use a refreshed pinned distroless `base-debian12:nonroot` digest that ships patched `libssl3 3.0.20-1~deb12u2`, clearing the `CVE-2026-45447` HIGH finding (OpenSSL `PKCS7_verify()` heap use-after-free) raised by the image scan.
- Release binaries are now built with Go `1.25.11`, matching the container and security-scan baseline (previously built with Go `1.22`).

### Changed
- GitHub Actions across CI and release workflows are now pinned to full-length commit SHAs for supply-chain hardening.

## [v1.10.0] - 2026-04-28

### Added
- Added default export autopilot mode for `query_range` exports. It starts with the highest supported sampling fidelity and only increases the sampling step after VictoriaMetrics still rejects the already-split minimum time windows.
- Added a hard autopilot sampling ceiling of 5 minutes; vmgather now stops there with an actionable error instead of silently creating lower-fidelity bundles.
- Archive metadata now records adaptive mode, sampled export step, max step, and adaptive retry decisions so support can see whether a bundle stayed exact or was sampled by autopilot.
- Export jobs now track adaptive retry progress (`adaptive_retries`, `last_error_kind`, `current_strategy`) so the UI can show when vmgather is automatically changing strategy instead of just failing the batch.
- Added explicit VictoriaMetrics API error classification for missing export route, query timeouts, too-many-series failures, and transient transport errors.
- Added focused coverage for adaptive export behavior, including autopilot step increases, the 5-minute stop condition, time-splitting retries, job-based splitting, failed partial-attempt cleanup, and progress reporting.

### Changed
- Export pipeline now writes each batch attempt into a temporary attempt file and appends it to the main staging file only after the full attempt succeeds, preventing corrupted or duplicated staging output after retries.
- Custom selector exports with selected jobs now prefer `/api/v1/export` when the selector can be safely rewritten with a `job=~...` matcher, avoiding unnecessary `query_range` fallback.
- Export requests now send `reduce_mem_usage=1` and `max_rows_per_line=10000` to VictoriaMetrics `/api/v1/export` for safer large-bundle collection.
- Export progress UI now enables adaptive autopilot by default, exposes a single toggle, and surfaces automatic retry strategy changes during long-running exports.

### Fixed
- `query_range` failures caused by VictoriaMetrics execution timeout no longer fail the whole export immediately; vmgather now retries by splitting the current time window down to a configured minimum.
- If minimum-window `query_range` retries still hit VictoriaMetrics execution timeout, autopilot now retries with the next sampling step up to 5 minutes before returning the final error.
- `/api/v1/export` failures caused by excessive matched series no longer fail immediately when multiple jobs are selected; vmgather now retries sequentially per job.
- Split-by-job and split-by-time retries now keep successful sub-attempt output in a temporary group file until the whole split succeeds, preventing duplicate metrics after a later sub-attempt fails.
- Context deadline errors are now classified as query timeouts in the adaptive exporter path, so timeout-driven retries keep working under request deadlines.
- Export safety defaults now also recover from invalid negative split settings, and the JSON contract no longer marks the non-pointer `safety` field as `omitempty`.

### Security
- Docker and security-scan Go toolchains are upgraded to Go `1.25.9` to consume the latest Go standard library vulnerability fixes.
- Docker runtime images now use a refreshed pinned distroless `base-debian12:nonroot` digest with fixed OpenSSL packages.

## [v1.9.1] - 2026-02-23

### Added
- VMImporter now keeps a local “Recent profiles” history (up to 10 records) and lets you quickly re-apply endpoint/tenant/auth mode/metric step/time shift/drop-label settings.
- Added `/api/profiles/recent` to expose saved VMImporter connection profiles for the UI dropdown.
- Preflight analysis now reports full label-limit diagnostics from the target (`maxLabelsPerTimeseries`, over-limit series, max labels seen, affected points, and top label-frequency stats).
- Label manager now shows both values: labels currently shown in UI and total labels detected in sample (`showing X/Y`).
- Preflight now defaults to sampling the first `2000` lines and includes a `Full collection` action to scan the entire bundle when complete label diagnostics are required.

### Changed
- Drop-label configuration is now normalized and applied consistently in both preflight analysis and streaming import.
- VMImporter explicitly protects core labels `__name__`, `job`, and `instance` from being dropped.
- Recent profile persistence sanitizes stored endpoint/auth settings and does not persist secrets (password, bearer token, header value).
- Successful endpoint checks and preflight analysis now also update recent profiles, so operator settings are retained even if a long import later fails mid-stream.

### Fixed
- Fixed confusing label-management UX: preflight now clearly indicates when only top labels are shown and how many labels exist in total.
- Label-limit warnings now clearly state that over-limit series will be dropped by VictoriaMetrics until label count is reduced or limit is increased.
- Added explicit warning when target `maxLabelsPerTimeseries` is unavailable but high label counts are detected in analyzed series.

## [v1.9.0] - 2026-02-17

### Security
- Docker build toolchain for `vmgather` and `vmimporter` images is upgraded to Go `1.25.7` (from `1.22`) to consume fixed Go stdlib security patches and reduce fixable `CRITICAL/HIGH` CVEs in registry scans.
- Docker runtime images now use `distroless ...:nonroot` with explicit `USER 65532:65532`, removing root-by-default execution.
- Docker publish targets now emit max-mode provenance and SBOM attestations (`--provenance=mode=max --sbom=true`) for release images.
- Docker builder/runtime base images are now pinned by digest in both Dockerfiles for deterministic builds and reduced supply-chain drift from mutable tags.
- Docker builder stages no longer install extra Alpine build packages (`git`, `build-base`), reducing build-time supply-chain surface while keeping `CGO_ENABLED=0` static binaries.
- Added a dedicated GitHub Actions security workflow (`.github/workflows/security.yml`) with `govulncheck`, `hadolint`, and `trivy` gates for Dockerfile misconfig and image CVEs.
- Docker builder stages now consolidate sequential `RUN` steps in both Dockerfiles to satisfy `hadolint` `DL3059` and keep the lint gate stable.
- Makefile now provides `security-check` (local CI-equivalent security gate: `govulncheck`, `gitleaks`, verified `trufflehog`, `hadolint`, `trivy` config/image), and `pre-push` now runs both `test-all-clean` and `security-check`.
- HTTP servers now set explicit hardening timeouts (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`) in both `vmgather` and `vmimporter`.
- Documentation and test examples now use env-based auth placeholders instead of static password-like strings to reduce false positives in secret scanners.
- Added secret-scanning hardening with a repo-level `.gitleaks.toml` allowlist for generated artifacts plus a CI `secret-scan` job (`gitleaks` + verified `trufflehog`) in `.github/workflows/security.yml`.
- Security workflow action references are now pinned to immutable commit SHAs, and `govulncheck` is pinned to `v1.1.4` for reproducible scans.
- Runtime container images now define explicit `HEALTHCHECK` probes (`/api/health`) for `vmgather` and `vmimporter` via a minimal static `container-healthcheck` binary.
- `skip_tls_verify` mode now emits explicit runtime warnings (in both exporter and importer paths) with redacted endpoints to reduce accidental insecure usage in production.
- TLS client configs now enforce `MinVersion: tls.VersionTLS12` where explicitly constructed; local test compose project suffix generation switched from `math/rand` to `crypto/rand` to satisfy high-severity SAST findings.

### Fixed
- `redactURLForLog` in importer now safely handles endpoints without user info (prevents potential nil-pointer panic during warning logs).
- `vmimporter` HTTP server no longer applies a global `ReadTimeout` that could abort large bundle uploads mid-transfer.
- `local-test-env/testconfig` now resolves `.env.dynamic` consistently when run from either repo root or `local-test-env/`, avoiding stale/default URL output during manual checks.
- VM discovery/estimation queries now clamp zero/future `time_range.end` values to `now`, preventing false "no components" results on valid live endpoints.

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
