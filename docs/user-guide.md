# VMExporter User Guide

## Overview

VMExporter is a tool for exporting VictoriaMetrics internal metrics to help support teams diagnose issues.

## Installation

### Download Binary

1. Go to [Releases](https://github.com/VictoriaMetrics/support/releases)
2. Download binary for your platform
3. Make it executable: `chmod +x vmexporter-*`

### Verify Download

Check SHA256 checksum:
```bash
sha256sum vmexporter-linux-amd64
# Compare with checksums.txt from release
```

## Usage

### Step 1: Start VMExporter

```bash
./vmexporter-darwin-arm64  # macOS
# or
./vmexporter-linux-amd64   # Linux
# or
vmexporter-windows-amd64.exe  # Windows
```

Browser opens automatically at http://localhost:8080

### Step 2: Select Time Range

Choose time range for metrics export:
- Preset options: Last 15min, 1h, 6h, 24h
- Custom range: Use datetime picker

### Step 3: Configure Connection

Enter VictoriaMetrics connection details:

**URL Examples:**
- VMSingle: `http://localhost:8428`
- VMCluster: `http://vmselect:8481/select/0/prometheus`
- Multitenant: `http://vmselect:8481/select/multitenant`

**Authentication:**
- None
- Basic Auth (username/password)
- Bearer Token
- Custom Header

### Step 4: Test Connection

Click "Test Connection" to verify:
- URL is reachable
- Authentication works
- VictoriaMetrics is detected

### Step 5: Select Components

Choose which VM components to export:
- vmstorage
- vmselect
- vminsert
- vmagent
- vmalert
- vmsingle

### Step 6: Configure Obfuscation (Optional)

Protect sensitive data:
- **IP Addresses**: Replace with 777.777.x.x
- **Job Names**: Replace with generic names
- **Custom Labels**: Select any label for obfuscation

### Step 7: Export

Click "Start Export" to:
1. Fetch metrics from VictoriaMetrics
2. Apply obfuscation (if enabled)
3. Create ZIP archive
4. Download to your computer

## Troubleshooting

### Connection Failed

- Verify URL is correct
- Check authentication credentials
- Ensure VictoriaMetrics is accessible
- Check firewall rules

### No Components Found

- Verify VictoriaMetrics is running
- Check `vm_app_version` metric exists
- Try different time range

### Export Too Large

- Reduce time range
- Select fewer components
- Export specific jobs only

## Support

- GitHub Issues: https://github.com/VictoriaMetrics/support/issues
- Email: info@victoriametrics.com
- Slack: https://slack.victoriametrics.com/

