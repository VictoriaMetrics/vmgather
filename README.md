# vmgather

vmgather collects VictoriaMetrics internal metrics, obfuscates sensitive data, and produces support-ready bundles in a single binary.

## Table of contents

1. [Highlights](#highlights)
2. [Downloads](#downloads)
3. [Quick start](#quick-start)
4. [VMImport companion](#vmimport-companion)
5. [Documentation set](#documentation-set)
6. [Mode quick choice](#mode-quick-choice)
7. [Usage workflow](#usage-workflow)
8. [Snapshot mode (experimental)](#snapshot-mode-experimental)
9. [Privacy & obfuscation](#privacy--obfuscation)
10. [Testing matrix](#testing-matrix)
11. [Build & release](#build--release)
12. [Contributing](#contributing)
13. [Security](#security)
14. [License & support](#license--support)

## Highlights

- **Single binary UI** – embedded web interfaces for both exporter and importer with VictoriaMetrics-style wizards.
- **Automatic discovery** – exporter detects vmagent, vmstorage, vminsert, vmselect, vmalert, and vmsingle instances.
- **Selector/Query (custom mode)** – collect any selector or MetricsQL query, with optional per-job target selection for selectors.
- **vmagent-compatible obfuscation**: selected label values become SHA-256 digests, the same as vmagent `-remoteWrite.obfuscateLabels`, so values match across exports and MDX streams.
- **Snapshot mode (experimental)**: scheduled exports of MDX metrics to your own S3, GCS, Azure Blob or local storage, ready for a Kubernetes CronJob; storage access is the vmbackup code.
- **Disk-safe staging** – exporter streams batches to a chosen directory so partial files survive crashes or manual interrupts.
- **Adjustable metric cadence**: choose a 30s/1m/5m sampling step for MetricsQL (query_range) exports; selector and cluster exports always keep raw samples.
- **Batched exports with ETA** – splits long ranges into windows and shows progress + forecasted completion.
- **Wide auth surface** – Basic, Bearer, custom headers, multi-tenant VMAuth flows; importer forwards tenant headers.
- **Chunked importing** – importer streams vmgather bundles in resumable chunks with post-upload verification.
- **Retention-aware imports** – importer trims samples outside target retention by default and displays cutoff in UTC with auto preflight on file drop.
- **Time alignment helper** – choose “Align first sample” or “Shift to now” to slide bundles into the active retention window before upload.
- **Cross-platform builds** – Linux, macOS, Windows (amd64/arm64/386).
- **First-run ready** – opens browser on launch and guides through validation, sampling, import, and export.

## Downloads

Grab the latest binaries from the [Releases page](https://github.com/VictoriaMetrics/vmgather/releases) or reuse them from CI artifacts.

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

Requirements: Go 1.24+, Make, Git.

```bash
git clone https://github.com/VictoriaMetrics/vmgather.git
cd vmgather
make build
./vmgather
```

## Mode quick choice

1. **Cluster metrics (wizard)** – start `./vmgather`, choose **Cluster metrics** on Step 1, then follow the 6-step wizard.
2. **Selector / Query (custom mode)** – start `./vmgather`, choose **Selector / Query**, paste a selector or MetricsQL, optionally pick jobs.
3. **CLI oneshot** – run `./vmgather -oneshot -oneshot-config ./export.json` (add `-export-stdout` to stream JSONL).

### Official Images
The latest release is automatically published to:

- [Docker Hub](https://hub.docker.com/u/victoriametrics): `victoriametrics/vmgather`, `victoriametrics/vmimporter`
- [Quay.io](https://quay.io/organization/victoriametrics): `quay.io/victoriametrics/vmgather`, `quay.io/victoriametrics/vmimporter`
- [GHCR](https://github.com/orgs/VictoriaMetrics/packages): `ghcr.io/victoriametrics/vmgather`, `ghcr.io/victoriametrics/vmimporter`


### Docker (vmgather + vmimporter)

You can pull the official images directly:

```bash
# vmgather
docker pull victoriametrics/vmgather:latest
# or
docker pull ghcr.io/victoriametrics/vmgather:latest

# vmimporter
docker pull victoriametrics/vmimporter:latest
# or
docker pull ghcr.io/victoriametrics/vmimporter:latest
```

Use [Buildx](https://docs.docker.com/build/building/multi-platform/) to produce linux/amd64 and linux/arm64 images locally:

```bash
# Build both utilities
make docker-build

# Run vmgather at http://localhost:8080
docker run --rm -p 8080:8080 vmgather:$(git describe --tags --always)

# Run VMImport at http://localhost:8081
docker run --rm -p 8081:8081 vmimporter:$(git describe --tags --always)
```

Set `DOCKER_OUTPUT=type=registry` to push directly to your registry, or override the tags via `docker buildx build -t <registry>/vmgather:tag …`.

Both Dockerfiles live in `build/docker/` and follow distroless best practices (`distroless/static-debian12` runtime without OpenSSL, static binaries).

### CLI flags

Both `vmgather` and `vmimporter` support `-addr` (bind address) and `-no-browser` to skip auto-launching a browser during scripting or Docker-based runs. vmgather's default is `localhost:8080` with automatic fallback to a free port; VMImport defaults to `0.0.0.0:8081` to avoid clashing with vmgather. vmgather also accepts `-output` to choose the directory for generated archives (defaults to `./exports`).

Both binaries accept `-allowed-hosts host1,host2`: extra Host names accepted from clients on the same machine, for a local reverse proxy. `localhost`, `*.localhost` and IP addresses are always accepted; other names from local clients are rejected to block DNS rebinding.

vmgather also accepts `-debug` for verbose logging. On vmgather, `-debug` additionally mounts Go's [`net/http/pprof`](https://pkg.go.dev/net/http/pprof) profiling endpoints at `/debug/pprof/` — useful for diagnosing high CPU usage during large exports, but pprof is unauthenticated, so only enable it on a deployment that isn't publicly reachable.

## VMImport companion

VMImport is a sibling utility that consumes vmgather bundles (`.jsonl` or `.zip`) and replays them into VictoriaMetrics via the `/api/v1/import` endpoint. It ships with the same embedded UI/HTTP server approach for parity:

- Reuses the connection card from vmgather, but adds a dedicated **Tenant / Account ID** input so multi-tenant inserts are one click away.
- Drag-and-drop bundle picker triggers an automatic preflight (JSONL sanity, retention cutoff, time range, suggested shift) and displays friendly progress/error states.
- Retention trimming is enabled by default; the UI shows the target cutoff in UTC and the shifted bundle range before upload.
- Time alignment controls stay disabled until analysis finishes; “Shift to now” and suggested-shift buttons ensure the bundle fits the active retention.
- Supports Basic auth, TLS verification toggles, and streaming large files directly to VictoriaMetrics.
- Shares the local-test environment (`local-test-env/`) so you can exercise uploads against the same scenarios used for vmgather.

Run the importer binary directly:

```bash
./vmimporter -addr 0.0.0.0:8081
```

…or use Docker:

```bash
docker run --rm -p 8081:8081 vmimporter:latest
```

The UI exposes the same health endpoint (`/api/health`) as vmgather for container liveness probes.

## Documentation set

- [User guide](docs/user-guide.md) – full wizard walkthrough with screenshots and troubleshooting tips.
- [Architecture](docs/architecture.md) – component diagram, APIs, obfuscation pipeline.
- [Development](docs/development.md) – project structure, targets, lint/test recipes.
- [Local test environment](local-test-env/README.md) – docker-compose environment mirroring VictoriaMetrics setups.

## Usage workflow

Exporter
1. Start the binary – the UI auto-detects an open port.
2. **Choose mode** – Cluster metrics (wizard) or Selector / Query (custom mode).
3. **Connect** to VictoriaMetrics single, cluster, or managed endpoints (`vmselect`, `vmagent`, VMAuth, MaaS paths).
4. **Define selector/query** in custom mode (auto-detected selector vs MetricsQL).
5. **Preview** metrics via sampling API calls (selector mode adds per-job target selection).
6. **Configure obfuscation** and remove labels for custom mode; review estimated series counts.
7. **Export** – pick a staging directory + metric step, watch progress/ETA, and let the backend stream batches to disk before archiving into a final bundle.

### Experimental CLI oneshot export

You can run a single export without the UI. This mode is experimental and intended for testing and automation.

Flags:
- `-oneshot` – run a single export and exit
- `-oneshot-config` – JSON file path (or `-` for stdin)
- `-export-stdout` – stream JSONL export to stdout (only with `-oneshot`)

Example:
```bash
./vmgather -oneshot -oneshot-config ./export.json -export-stdout
```

Sample `export.json`:
```json
{
  "connection": { "url": "http://localhost:18428", "auth": { "type": "none" } },
  "time_range": { "start": "2026-01-23T12:00:00Z", "end": "2026-01-23T13:00:00Z" },
  "mode": "custom",
  "query_type": "selector",
  "query": "{job=~\"test.*\"}",
  "jobs": ["test1"],
  "obfuscation": { "enabled": false, "drop_labels": ["env"] },
  "batching": { "enabled": false }
}
```

The `connection` object also accepts `skip_tls_verify` (bool, disables TLS certificate verification – lab/dev only) and `disable_compression` (bool, defaults to `false`; set `true` to skip gzip-compressed export responses when vmgather runs alongside the VictoriaMetrics instance it exports from and CPU, not network bandwidth, is the constrained resource).

Importer
1. Start `./vmimporter` (or Docker) – UI runs at `:8081` by default.
2. **Select bundle** – drop a vmgather `.zip`/`.jsonl` or pick via file dialog.
3. **Endpoint & auth** – enter VictoriaMetrics import URL, tenant/account ID, and auth (Basic or custom header); toggle TLS verify as needed.
4. **Analyze (optional)** – run preflight to see time range, series hints, retention warnings, and sample labels.
5. **Import** – start upload; importer streams in ~512KB chunks, shows progress, and verifies data via `/api/v1/series` after completion. Resume is available if a job fails mid-flight.

See [docs/user-guide.md](docs/user-guide.md) for UI screenshots and parameter descriptions.

## Snapshot mode (experimental)

Snapshot mode is experimental: flags, the bucket layout and the state file may change between releases.

Snapshot mode is for teams that cannot send MDX (Monitoring Data eXchange) to VictoriaMetrics. vmgather runs on a schedule in your infrastructure, exports the MDX metrics of your VictoriaMetrics components for fixed time windows and uploads every window to your own storage. You share the snapshots you choose with support.

It reuses VictoriaMetrics code, so it behaves like the tools you already run:
- **What is exported**: the vmagent `-remoteWrite.mdx.enable` filter (`lib/mdx`). A series is kept only if its job and instance (and tenant) have `vm_app_version`, or the series has `victoriametrics_app="true"` or the label from `-mdx.label`. Kept series get `victoriametrics_app="true"`.
- **Obfuscation**: the vmagent `-remoteWrite.obfuscateLabels` algorithm (see [Privacy & obfuscation](#privacy--obfuscation)).
- **Storage**: the vmbackup storage layer and upload (`lib/backup`). Destinations are `s3://bucket/prefix`, `gs://bucket/prefix`, `azblob://container/prefix` and `fs:///absolute/path`. Credentials and options use the vmbackup flags: `-customS3Endpoint`, `-s3ForcePathStyle`, `-credsFilePath`, `-configFilePath`, `-configProfile`, `-s3StorageClass`, `-s3ObjectTags` and others, plus the standard AWS, GCP and Azure credential chains (IRSA, workload identity, instance roles).

Run one snapshot and exit (for a Kubernetes CronJob or cron):
```bash
./vmgather -snapshot.dst=s3://my-bucket/vmgather -snapshot.config=./snapshot.json -output=/tmp/vmgather
```

Add `-snapshot.interval=10m` to keep the process running and check for complete windows every 10 minutes.

`snapshot.json` uses the oneshot config format; `time_range` is ignored. Leave `jobs` empty to discover, for every window, the VictoriaMetrics components that had `vm_app_version` at any time in it, so new components are picked up without config changes and a component that stopped during the window is still exported:
```json
{
  "connection": { "url": "http://vmselect:8481/select/0/prometheus", "auth": { "type": "basic", "username": "vmgather" } },
  "obfuscation": { "enabled": true, "obfuscate_instance": true }
}
```

Keep secrets out of the file: `VMGATHER_VM_PASSWORD`, `VMGATHER_VM_BEARER_TOKEN` and `VMGATHER_VM_HEADER_VALUE` override the matching auth fields.

| Flag | Default | Meaning |
|---|---|---|
| `-snapshot.dst` | | Destination. Setting it turns on snapshot mode |
| `-snapshot.config` | | Export config JSON |
| `-snapshot.period` | `1h` | Window length. Windows are aligned to it (UTC) |
| `-snapshot.lag` | `5m` | A window is exported only when its end is at least this far behind now |
| `-snapshot.maxCatchUp` | `24` | Maximum windows per run after downtime; the next run continues |
| `-snapshot.start` | | Start of the first window when the destination has no state yet. Default: one period before the newest complete window end |
| `-snapshot.end` | | Fixed end for a one-time backfill of `[-snapshot.start, -snapshot.end)`. Needs `-snapshot.start`, must be a multiple of the period and at least `-snapshot.lag` behind now. A backfill does not read or write the state and skips windows that are already uploaded, so a failed backfill can be started again |
| `-snapshot.interval` | `0` | `0` runs once and exits; a positive value runs in a loop |
| `-snapshot.retention` | `30d` | Scheduled runs delete snapshots whose window ended longer ago, including incomplete ones; `0` keeps them forever. VictoriaMetrics retention format: `h`, `d`, `w`, `y`, `M` (months); there is no minute suffix, use `300s` |

How it works:
- Windows are half-open `[start, end)`: a sample exactly at the end belongs to the next window, so no sample is in two snapshots.
- Every window goes to `<dst>/<start>_<end>/` (for example `20261121T090000Z_20261121T100000Z`) with the usual vmgather bundle `vmgather.zip` (`metrics.jsonl`, `metadata.json`, `README.txt`), so vmimporter imports it as is. The layout is the vmbackup layout: the zip is stored as `vmgather.zip/<size>_<offset>_<size>` parts (one object for bundles under 1 GiB), followed by `backup_metadata.ignore` and `backup_complete.ignore`, which is written last.
- `<dst>/vmgather_state.json` keeps the end of the last uploaded window. It is written only after the upload completed. A failed or interrupted window (SIGTERM included) is exported again by the next run into the same place.
- Inside a window the export uses the usual adaptive batches: heavy windows are split by time and 502/503 responses are retried, so a busy cluster slows a run down instead of breaking it. If a window still fails, the run exits with code 1 and the state does not move.
- A cluster with an unavailable vmstorage fails the run instead of returning part of the data: exports never accept partial responses, and discovery sends `deny_partial_response=1`.
- A window without MDX series (for example before any component existed, or during an outage of the whole monitoring) is not uploaded; the run logs a warning and the state moves on, so it never blocks later windows.
- A state that ends after the current time (clock moved back, edited file) stops the run with an error instead of waiting silently.

Download a snapshot with vmrestore, then import `vmgather.zip` with vmimporter:
```bash
vmrestore -src=s3://my-bucket/vmgather/20261121T090000Z_20261121T100000Z -storageDataPath=/tmp/snapshot
```

Disk space, local and remote, stays bounded also when runs fail:
- The local work directory `<-output>/snapshots` is emptied before and after every run, including failed and interrupted ones. During a run it holds one window: raw JSONL plus its zip.
- A failed window is retried into the same place and overwrites it, so failed attempts do not pile up in the bucket.
- `-snapshot.retention` deletes old snapshots, complete or not, after every scheduled run. A backfill never deletes anything, so give backfills their own prefix.
- For a bucket with object versioning add the vmbackup flag `-deleteAllObjectVersions`, otherwise deleted snapshots stay as old versions.
- A killed multipart upload (bundles over 5 MB) can leave invisible parts in S3: add a lifecycle rule that aborts incomplete multipart uploads.

Things to know:
- Run one writer per destination (`concurrencyPolicy: Forbid` for a CronJob).
- The storage user needs read, write, delete and list on the prefix (for S3: `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, `s3:ListBucket`).
- Samples that arrive later than `-snapshot.lag` (for example from a vmagent persistent queue after a long outage) are not exported again.
- S3 and S3-compatible storage (MinIO) and `fs://` are tested. GCS, Azure Blob and bundles over 1 GiB use the same vmbackup code, but vmgather does not test them yet.

Kubernetes manifests: [deploy/k8s/](deploy/k8s/) has a CronJob and a one-time Job that exports MDX metrics from a vmbackup backup (vmrestore into a temporary vmsingle, then a backfill).

## Privacy & obfuscation

- Obfuscated label values are replaced with their hex SHA-256 digest, the same as vmagent [`-remoteWrite.obfuscateLabels`](https://docs.victoriametrics.com/victoriametrics/vmagent/#obfuscating-label-values). The same value gives the same digest in every export, in every snapshot and in MDX streams from vmagent, so support can match them.
- There is no salt. Values with a small space, such as `IP:port`, can be recovered by brute force. Drop a label instead if it must not leave your network.
- You choose the labels: `instance`, `job` and any custom labels. When `job` is obfuscated, the job names in `metadata.json` are obfuscated too.
- Obfuscation settings apply to previews and exports. No mapping is stored: anyone with the real value can compute its digest (`printf '%s' 10.0.0.1:8482 | sha256sum`).
- No credentials or temporary archives persist to disk after the process ends.

## Testing

```bash
make test        # Unit tests (fast)
make test-full   # Complete suite: unit + integration scenarios
```

See [docs/development.md](docs/development.md) for detailed testing instructions.

## Build & release

```bash
make build       # Local build
make build-all   # Cross-platform binaries
```

See [docs/development.md](docs/development.md) and [docs/release-guide.md](docs/release-guide.md) for build and release instructions.

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
- Issues: https://github.com/VictoriaMetrics/vmgather/issues
