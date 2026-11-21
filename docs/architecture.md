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
| Application | `internal/snapshot` | Snapshot mode: window planning, state, export of one window and upload through VictoriaMetrics `lib/backup`. |
| Infrastructure | `internal/infrastructure/obfuscation` | vmagent-compatible obfuscation: hex SHA-256 of label values. |
| Infrastructure | `internal/infrastructure/archive` | Streams export data, writes metadata, generates ZIP + checksum. |
| Domain | `internal/domain` | Shared types/configs used across the stack. |

### Frontend

- Vanilla JS + HTML/CSS, compiled and embedded via `go:embed`.
- Implements the wizard, validation states, and progress updates.
- Communicates only with the local server (`/api/*` endpoints).

### Backend

- Go 1.24+ HTTP server using the standard library.
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

- Batching: auto uses 1-hour windows (a shorter range is one window) or a custom interval; minimum batch interval 30s. `/api/v1/export` repeats every label set once per request window, so small windows multiply the payload.
- Metric step: used only by the query_range path (MetricsQL); defaults to 30s/1m/5m by time range unless overridden via `metric_step_seconds`.
- Fallback: if `/api/v1/export` returns 404/missing route, transparently switches to `query_range` with normalized `/rw/prometheus` → `/prometheus` paths for VMAuth.
- Staging: `/api/fs/check` creates/validates staging directories and write access; job metadata exposes the staging path.
- Job manager: up to 3 concurrent exports, ETA/progress tracking, cancellation, retention window for finished jobs.
- Line processing: one `lineProcessor` per export applies, in the vmagent order, the MDX filter (optional, VictoriaMetrics `lib/mdx`), dropped labels and obfuscation. Without any of them lines are copied unchanged without JSON parsing.
- MDX filter: `lib/mdx` keeps a series only after it saw `vm_app_version` (or `victoriametrics_app="true"`) for its job and instance, so before every batch the export reads `vm_app_version` with the same job matcher and window to prime the filter. A failed priming request fails the export.
- Obfuscation: instance/job/custom labels applied the same way to samples and exports; job names in `metadata.json` are obfuscated when the job label is; `metadata.json` + `README.txt` accompany `metrics.jsonl` in the ZIP along with SHA256.

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

- **Algorithm**: a label value becomes its hex SHA-256 digest, without salt, the same as vmagent `-remoteWrite.obfuscateLabels` (`app/vmagent/remotewrite/obfuscate.go`). vmagent's implementation is not exported, so vmgather has the same few lines and tests them against fixed digests.
- **Labels**: `instance`, `job` and user-provided keys.
- **Deterministic**: the same value gives the same digest in every export, resumed export, snapshot and MDX stream. No mapping is kept or written.
- **Limits**: values with a small space (IP:port) can be recovered by brute force.
- **Sample previews**: `/api/sample` responses use the same helper, so the UI never shows raw values once obfuscation is enabled.

## Snapshot mode

One run:

1. `planWindows` turns the state (end of the last uploaded window), `now - lag` and the period into aligned half-open windows.
2. For every window, oldest first:
   1. discover jobs with `vm_app_version` at any time in the window (`last_over_time` over the window, `deny_partial_response=1`), unless jobs are set in the config;
   2. `ExecuteExport` in cluster mode with the MDX filter for `[start, end - 1ms]`, into `<work>/<VM snapshot name>/vmgather.zip`;
   3. `lib/backup` `actions.Backup` uploads it to `<dst>/<start>_<end>/` (parts, `backup_metadata.ignore`, then `backup_complete.ignore`);
   4. write `<dst>/vmgather_state.json`.

- Storage access is VictoriaMetrics `lib/backup` (`actions.NewRemoteFS`, `actions.Backup`), so destinations, credentials, flags and retries are the vmbackup ones, and vmrestore downloads a snapshot.
- The state is written only after `backup_complete.ignore`. Snapshot names are derived from the window, so a retry overwrites the same place.
- A window without MDX series is not uploaded; the state still moves, so empty windows never block the schedule.
- Space stays bounded: the work dir is emptied before and after every run; `-snapshot.retention` (scheduled runs only) deletes expired snapshot dirs with `RemoteFS.ListParts`, `DeleteFile` and `DeletePart`, completion marker first, including incomplete uploads.
- A backfill (`-snapshot.end`) exports a fixed range, does not use the state and skips windows that already have `backup_complete.ignore`.
- `-snapshot.interval` runs the same function in a loop; without it the process exits after one run (Kubernetes CronJob).

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

- Bundle ingestion: accepts `.zip` (reads `metrics.jsonl`/`metadata.json` in place, nothing is extracted to disk) or raw `.jsonl`; rejects archives without metrics. Uploads are streamed to a temp file; a failed job keeps its upload for resume for 1 hour.
- Chunked streaming: uploads in ~512KB chunks to `/api/v1/import`, with progress reporting, byte counters, and resumable offsets on failure.
- Resume: `/api/import/resume` continues a failed job from the saved offset and cached bundle path.
- Retention: optional `drop_old` drops points older than the target’s retention (fetched via `/api/v1/status/tsdb`); warnings surface via `/api/analyze`.
- Tenant isolation: always forwards tenant/account via `X-Vm-TenantID` and supports Basic/custom header auth plus TLS skip.
- Verification: post-upload sampling (`/api/v1/series` + time window derived from metadata) to confirm visibility; status is exposed via `/api/import/status`.
