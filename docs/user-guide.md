# vmgather user guide

Official walkthrough for the VictoriaMetrics metrics export wizard.

## Contents

1. [Before you start](#before-you-start)
2. [Mode quick choice](#mode-quick-choice)
3. [Launch and connection](#launch-and-connection)
4. [Wizard steps](#wizard-steps)
5. [Export bundle](#export-bundle)
6. [Troubleshooting](#troubleshooting)
7. [Support](#support)

## Before you start

- Download the latest binary from the [Releases page](https://github.com/VictoriaMetrics/vmgather/releases).
- Verify the SHA256 checksum using the `checksums.txt` file.
- Ensure you have reachability to your VictoriaMetrics endpoints (single, cluster, VMAuth, or managed).
- Have credentials ready for tenants that require authentication.

## Mode quick choice

1. **Cluster metrics (wizard)** – run `./vmgather`, choose **Cluster metrics** on Step 1, then follow the 6-step wizard.
2. **Selector / Query (custom mode)** – run `./vmgather`, choose **Selector / Query**, paste a selector or MetricsQL, optionally pick jobs.
3. **CLI oneshot** – run `./vmgather -oneshot -oneshot-config ./export.json` (add `-export-stdout` to stream JSONL).

## Launch and connection

1. Run the binary for your platform (`./vmgather-vX.Y.Z-linux-amd64`, `./vmgather-vX.Y.Z-windows-amd64.exe`, etc.).
2. The application opens a browser window at `http://localhost:8080` (auto-switches to a free port if 8080 is taken).
3. The landing page shows the wizard with the current step highlighted (6 steps for Cluster metrics, 6/7 for Selector / Query depending on selector vs MetricsQL).

### Supported target URLs

- VMSingle (local): `http://localhost:18428`
- VMSelect (cluster, tenant 0): `https://vmselect.example.com/select/0/prometheus`
- VMSelect (cluster, tenant 1011): `https://vmselect.example.com/select/1011/prometheus`
- VMAuth (cluster proxy): `https://vmauth.example.com`
- VictoriaMetrics Managed / MaaS: `https://<tenant>.victoriametrics.cloud/<tenant-id>/rw/prometheus`

### Authentication

- None (default)
- Basic Auth (username/password)
- Bearer Token
- Custom header (key + value) for VMAuth deployments

## Wizard steps

### Step 1 – Welcome & mode
- Choose **Cluster metrics** (default) or **Selector / Query** (custom mode). Mode can only be switched on Step 1.

### Cluster metrics flow (6 steps)
| Step | Description |
| --- | --- |
| 2. **Time range** | Choose presets (15m, 1h, 6h, 24h) or define custom start/end timestamps. |
| 3. **Connection** | Enter URL + auth method. Invalid URLs disable the **Test Connection** button instantly. |
| 4. **Component discovery** | Select which VictoriaMetrics components, tenants, and jobs to include. UI lists everything found via `vm_app_version` metrics. |
| 5. **Obfuscation** | Enable anonymisation for IPs, job labels, or add custom label keys. Estimated series count per component/job is displayed. |
| 6. **Complete** | Review export details and download the archive. |

### Selector / Query flow (6 or 7 steps)
| Step | Description |
| --- | --- |
| 2. **Time range** | Choose presets or custom timestamps. |
| 3. **Connection** | Enter URL + auth method. Test connection to proceed. |
| 4. **Selector / Query** | Enter a series selector or MetricsQL. vmgather auto-detects the type. |
| 5. **Select targets** | **Selector only** – choose jobs/instances discovered from the selector (skipped for MetricsQL). |
| 6. **Obfuscation + label removal** | Obfuscation defaults off; you can remove labels from export in this mode. |
| 7. **Complete** | Review export details and download the archive. |

Each card contains hints and sample values matching VictoriaMetrics defaults.

## Experimental CLI oneshot export

The exporter also supports an experimental oneshot mode for automation and test flows.

Run a single export and stream JSONL to stdout:
```bash
./vmgather -oneshot -oneshot-config ./export.json -export-stdout
```

Minimal `export.json` example:
```json
{
  "connection": { "url": "http://localhost:18428", "auth": { "type": "none" } },
  "time_range": { "start": "2026-01-23T12:00:00Z", "end": "2026-01-23T13:00:00Z" },
  "mode": "custom",
  "query_type": "metricsql",
  "query": "rate(vm_rows_inserted_total[5m])",
  "obfuscation": { "enabled": false },
  "batching": { "enabled": false }
}
```
## Export bundle

Click **Start export** to execute the workflow:

1. vmgather streams data via `/api/v1/export` (and falls back to `query_range` when needed).
2. Data is obfuscated on the fly.
3. Archive contents are written to a temporary directory:
   - `metrics.jsonl` – raw metrics dump.
   - `metadata.json` – VictoriaMetrics versions, selected components, timeframe, and checksums.
   - `README.txt` – human-readable summary for support (timestamps in UTC, unique component list, current binary version).
4. A ZIP archive is produced with SHA256 checksum displayed on completion. The UI shows obfuscated sample data from the final export for clarity.

Downloads start automatically in the browser; you can also retrieve the archive via the **Download again** button.

## Troubleshooting

### “Connection failed”

- Confirm the URL is reachable with `curl`.
- Validate credentials (VMAuth may require a tenant prefix).
- For managed clusters, ensure path rewriting is correct (`/rw/prometheus` vs `/prometheus`).

### “No components discovered”

- Check that `vm_app_version` metrics are available.
- Confirm the time range overlaps with active scraping.
- For multi-tenant cases, ensure you selected the correct tenant ID or VMAuth route.

### “Export timed out” / archive too large

- Narrow the time range or deselect unused components/jobs.
- Use the scenario environment described in `local-test-env/README.md` to reproduce locally.
- Inspect browser console logs for network failures if the UI becomes unresponsive.

## Support

- Documentation updates: [docs/](../docs/)
- Issues: https://github.com/VictoriaMetrics/vmgather/issues
- Email: info@victoriametrics.com
- Slack: https://slack.victoriametrics.com/

# VMImporter user guide

Importer UI mirrors the exporter wizard but is tailored for replaying vmgather bundles back into VictoriaMetrics.

## Steps

1. **Connection**: enter the target import endpoint (`http://localhost:18428`, `https://vmselect.example.com/select/0/prometheus`, `https://vm.example.com/1234/prometheus`, etc.), optional Tenant / Account ID, and auth (None/Basic/Bearer/Header). Test Connection must be green.
2. **Bundle**: drop a vmgather bundle (`.jsonl` or `.zip`). Preflight starts automatically:
   - validates JSONL structure and values,
   - reads time range (UTC),
   - fetches target retention and shows cutoff,
   - estimates points/dropped/skipped, and suggests a time shift if needed.
3. **Time alignment** (enabled after preflight): pick “Align first sample” (datetime-local in UTC) or click “Shift to now” to slide the bundle so its end lands at the current time without exceeding retention. Shift summary shows original and shifted ranges.
4. **Batching**: metric sampling step defaults to the adaptive hint; override if necessary.
5. **Import**: Start Import stays disabled until connection + preflight + file are ready. Import streams ~512KB chunks, shows progress/ETA, and runs a verification query on completion. Failed jobs expose a Resume option when possible.

## Behaviour & defaults

- Retention trimming is always on: samples older than the target cutoff are dropped server-side to avoid storage errors. Cutoff is displayed in UTC.
- Timezone handling: all times are shown and compared in UTC; user-facing picker is UTC to avoid drift with server TZ.
- Invalid timestamps/lines are skipped during preflight; counts are reported before import.

## Tips

- If you need to shift a historic bundle into the active window, use “Shift to now” or set the desired first-sample time—no manual offset math required.
- If retention fetch fails, importer still analyzes the bundle; warnings will note that cutoff is unknown.
- Multi-tenant headers are forwarded automatically when Tenant / Account ID is set.
