# VMGather user guide

Official walkthrough for the VictoriaMetrics metrics export wizard.

## Contents

1. [Before you start](#before-you-start)
2. [Launch and connection](#launch-and-connection)
3. [Wizard steps](#wizard-steps)
4. [Export bundle](#export-bundle)
5. [Troubleshooting](#troubleshooting)
6. [Support](#support)

## Before you start

- Download the latest binary from the [Releases page](https://github.com/VictoriaMetrics/support/releases).
- Verify the SHA256 checksum using the `checksums.txt` file.
- Ensure you have reachability to your VictoriaMetrics endpoints (single, cluster, VMAuth, or managed).
- Have credentials ready for tenants that require authentication.

## Launch and connection

1. Run the binary for your platform (`./vmgather-vX.Y.Z-linux-amd64`, `vmgather-vX.Y.Z-windows-amd64.exe`, etc.).
2. The application opens a browser window at `http://localhost:8080` (auto-switches to a free port if 8080 is taken).
3. The landing page shows the 6-step wizard with the current step highlighted.

### Supported target URLs

- VMSingle: `https://vm.example.com:8428`
- VMCluster (tenant 0): `https://vmselect.example.com:8481/select/0/prometheus`
- VMCluster + VMAuth: `https://vmauth.example.com/select/0/prometheus`
- VictoriaMetrics Managed / MaaS: `https://<tenant>.victoriametrics.cloud/<tenant-id>/rw/prometheus`

### Authentication

- None (default)
- Basic Auth (username/password)
- Bearer Token
- Custom header (key + value) for VMAuth deployments

## Wizard steps

| Step | Description |
| --- | --- |
| 1. **Time range** | Choose presets (15m, 1h, 6h, 24h) or define custom start/end timestamps. |
| 2. **Connection** | Enter URL + auth method. Invalid URLs disable the **Test Connection** button instantly. |
| 3. **Validation** | Click **Test connection**. VMGather checks reachability, detects product flavour, and confirms `/api/v1/export` availability. |
| 4. **Component discovery** | Select which VictoriaMetrics components, tenants, and jobs to include. UI lists everything found via `vm_app_version` metrics. |
| 5. **Obfuscation** | Enable anonymisation for IPs, job labels, or add custom label keys. An estimated series count per component/job is displayed based on discovery data. |
| 6. **Summary** | Review time range, target, authentication, selected components, and pre-obfuscated sample data before exporting. |

Each card contains hints and sample values matching VictoriaMetrics defaults.

## Export bundle

Click **Start export** to execute the workflow:

1. VMGather streams data via `/api/v1/export` (and falls back to `query_range` when needed).
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
- Issues: https://github.com/VictoriaMetrics/support/issues
- Email: info@victoriametrics.com
- Slack: https://slack.victoriametrics.com/

# VMImporter user guide

Importer UI mirrors the exporter wizard but is tailored for replaying VMGather bundles back into VictoriaMetrics.

## Steps

1. **Connection**: enter the target import endpoint (`http://localhost:8428`, `https://vm.example.com/1234/prometheus`, `/select/0/prometheus`, etc.), optional Tenant / Account ID, and auth (None/Basic/Bearer/Header). Test Connection must be green.
2. **Bundle**: drop a VMGather bundle (`.jsonl` or `.zip`). Preflight starts automatically:
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
