# vmgather architecture

A high-level breakdown of how the VictoriaMetrics metrics exporter is structured internally.

## Guiding principles

- **Single binary** – UI assets are embedded; the HTTP server defaults to localhost (configurable via `-addr` and auto-fallback to a free port).
- **Privacy-first** – the pipeline obfuscates data as it streams instead of handling temporary plaintext copies.
- **Stateless** – no configuration files or databases; everything is supplied per-run.
- **Predictable UX** – the same 6-step flow as other VictoriaMetrics utilities.

## Component overview

| Layer | Package | Responsibility |
| --- | --- | --- |
| Presentation | `internal/server` | Hosts the HTTP server, serves static assets, exposes REST endpoints to the UI. |
| Presentation | `internal/importer/server` | Standalone VMImport UI & API for uploading bundles back into VictoriaMetrics. |
| Application | `internal/application/services` | Orchestrates validation, discovery, sampling, and export workflows. |
| Infrastructure | `internal/infrastructure/vm` | VictoriaMetrics client (query, export APIs, auth, multitenancy). |
| Infrastructure | `internal/infrastructure/obfuscation` | Deterministic obfuscation for IPs/jobs/custom labels. |
| Infrastructure | `internal/infrastructure/archive` | Streams export data, writes metadata, generates ZIP + checksum. |
| Domain | `internal/domain` | Shared types/configs used across the stack. |

### Frontend

- Vanilla JS + HTML/CSS, compiled and embedded via `go:embed`.
- Implements the wizard, validation states, and progress updates.
- Communicates only with the local server (`/api/*` endpoints).

### Backend

- Go 1.21+ HTTP server using the standard library.
- Defaults to `localhost:8080` (exporter) and `0.0.0.0:8081` (importer); falls back to a free port when busy.
- Provides REST APIs mirroring other VictoriaMetrics tools.

## Data flow

```
Browser
  ↓ (REST)
internal/server (HTTP handler)
  ↓
application/services.VMService
  ↓
infrastructure/vm.Client ───→ VictoriaMetrics API
  ↓                                         ↑
application/services.ExportService          │
  ↓                                         │
infrastructure/obfuscation.Manager          │
  ↓                                         │
infrastructure/archive.Writer ──────────────┘
  ↓
ZIP archive → browser download
```

### Exporter specifics

- Batching: auto-selects 30s/1m/5m windows (or custom interval) per time range; minimum batch interval 30s.
- Metric step: defaults to the same 30s/1m/5m cadence unless overridden via `metric_step_seconds`.
- Fallback: if `/api/v1/export` returns 404/missing route, transparently switches to `query_range` with normalized `/rw/prometheus` → `/prometheus` paths for VMAuth.
- Staging: `/api/fs/check` creates/validates staging directories and write access; job metadata exposes the staging path.
- Job manager: up to 3 concurrent exports, ETA/progress tracking, cancellation, retention window for finished jobs.
- Obfuscation: instance/job/custom labels applied consistently to samples and exports; deterministic maps are embedded in archive metadata; `metadata.json` + `README.txt` accompany `metrics.jsonl` in the ZIP along with SHA256.

## API surface

| Endpoint | Purpose |
| --- | --- |
| `POST /api/validate` | Checks reachability, auth, and returns detected VM flavour + version. |
	| `POST /api/discover` | Finds available components, per-job series estimates, and jobs via `vm_app_version`. |
| `POST /api/sample` | Fetches preview metrics (up to a safe limit) for UI confirmation. |
| `POST /api/export` | Legacy synchronous export used by CLI tools. Still available for compatibility. |
| `POST /api/export/start` | Starts a batched export job, including optional `staging_dir` and `metric_step_seconds` hints, and returns job meta (batches/ETA/staging path). |
| `GET /api/export/status` | Polls the state of a running export job (progress, ETA, final archive metadata). |
| `GET /api/download?path=…` | Returns the generated ZIP file. |
| `GET /api/fs/list` | Lists directories for staging selection with basic write hints. |
| `POST /api/fs/check` | Validates/creates a staging directory and write-ability. |
| `POST /api/export/cancel` | Cancels a running export job. |
| `GET /api/config` | Returns UI defaults (version, recommended staging dir, OS hints). |

All endpoints accept/return JSON with error details suitable for UI presentation.

## Obfuscation

- **IPs** – replaced with `777.777.X.Y`, retaining port numbers and component grouping.
- **Jobs** – renamed to `<component>-job-<n>` while keeping the original component prefix.
- **Custom labels** – user-provided keys; mappings kept in memory for the session, not persisted.
- **Sample previews** – `/api/sample` responses and export previews reuse the obfuscator so the UI never shows raw instances/jobs once obfuscation is enabled.
- **Deterministic** – the same input within a session maps to the same output so support can correlate metrics.

## Security characteristics

- Credentials remain in memory only for the duration of the call and are never written to disk.
- The HTTP server binds to `localhost` and random ports to lower the risk surface.
- Temporary files are removed immediately after the bundle is downloaded or when the process exits.

## Testing hooks

- Unit tests cover domain logic, VM API client edge cases, obfuscation permutations, and archive writing.
- Integration tests spin up VictoriaMetrics flavours through `local-test-env`.
- Playwright E2E suites exercise the complete wizard flow, ensuring API contracts stay stable.

## VMImporter companion flow

VMImporter is purposely isolated from the exporter codebase to minimise coupling: aside from sharing the repo and build tooling it does not import exporter packages. Its flow is intentionally short:

```
Browser (drop zone + endpoint form)
  ↓ /api/upload (multipart form)
internal/importer/server
  ↓
sendToEndpoint → VictoriaMetrics /api/v1/import
```

The UI mirrors vmgather's connection card but introduces tenant/account ID fields and a drag-and-drop area for `.jsonl`/`.zip` bundles. The backend treats uploaded data as opaque bytes and re-streams it to VictoriaMetrics, reusing the same auth/TLS toggles. Tests reuse `local-test-env` so uploads can be validated against real vmselect/vminsert setups before releasing.

### VMImporter specifics

- Bundle ingestion: accepts `.zip` (extracts `metrics.jsonl`/`metadata.json`) or raw `.jsonl`; rejects archives without metrics.
- Chunked streaming: uploads in ~512KB chunks to `/api/v1/import`, with progress reporting, byte counters, and resumable offsets on failure.
- Resume: `/api/import/resume` continues a failed job from the saved offset and cached bundle path.
- Retention: optional `drop_old` drops points older than the target’s retention (fetched via `/api/v1/status/tsdb`); warnings surface via `/api/analyze`.
- Tenant isolation: always forwards tenant/account via `X-Vm-TenantID` and supports Basic/custom header auth plus TLS skip.
- Verification: post-upload sampling (`/api/v1/series` + time window derived from metadata) to confirm visibility; status is exposed via `/api/import/status`.
