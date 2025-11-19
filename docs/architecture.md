# VMExporter architecture

A high-level breakdown of how the VictoriaMetrics metrics exporter is structured internally.

## Guiding principles

- **Single binary** – UI assets are embedded and the HTTP server binds to localhost only.
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
- Selects a random available port on startup (to avoid conflicts).
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

## VMImport companion flow

VMImport is purposely isolated from the exporter codebase to minimise coupling: aside from sharing the repo and build tooling it does not import exporter packages. Its flow is intentionally short:

```
Browser (drop zone + endpoint form)
  ↓ /api/upload (multipart form)
internal/importer/server
  ↓
sendToEndpoint → VictoriaMetrics /api/v1/import
```

The UI mirrors VMExporter's connection card but introduces tenant/account ID fields and a drag-and-drop area for `.jsonl`/`.zip` bundles. The backend treats uploaded data as opaque bytes and re-streams it to VictoriaMetrics, reusing the same auth/TLS toggles. Tests reuse `local-test-env` so uploads can be validated against real vmselect/vminsert setups before releasing.
