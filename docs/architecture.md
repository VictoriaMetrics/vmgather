# VMExporter Architecture

## Overview

VMExporter is a standalone Go application that exports VictoriaMetrics internal metrics for support diagnostics.

## Design Principles

- **Single Binary**: No external dependencies
- **Web-Based UI**: Cross-platform compatibility
- **Privacy First**: Data obfuscation built-in
- **Streaming**: Memory-efficient for large exports
- **Stateless**: No data persistence

## System Components

### Frontend (Web UI)

- **Technology**: Vanilla JavaScript + HTML/CSS
- **Embedded**: Static files embedded in Go binary
- **Flow**: 6-step wizard interface
- **Features**:
  - Time range selection (presets + datetime picker)
  - Multi-stage connection validation
  - Component discovery and selection
  - Sample data preview
  - Obfuscation configuration
  - Real-time export progress

### Backend (Go HTTP Server)

- **Technology**: Go 1.21+ standard library
- **Port**: 8080 (auto-selects if busy)
- **API**: REST endpoints for frontend
- **Features**:
  - HTTP server with embedded static files
  - VictoriaMetrics client
  - Streaming metrics export
  - Data obfuscation
  - ZIP archive creation

## Architecture Layers

### 1. Presentation Layer

**HTTP Server** (`internal/server`)
- Serves web UI
- REST API endpoints
- Request/response handling

### 2. Application Layer

**Services** (`internal/application/services`)
- VMService: Component discovery, metrics sampling
- ExportService: Full export workflow orchestration

### 3. Infrastructure Layer

**VM Client** (`internal/infrastructure/vm`)
- HTTP client for VictoriaMetrics API
- Query execution (/api/v1/query, /api/v1/export)
- Authentication handling
- Multi-tenant support

**Obfuscator** (`internal/infrastructure/obfuscation`)
- IP address obfuscation (777.777.x.x pool)
- Job name obfuscation
- Label obfuscation (user-selected)
- Deterministic mapping

**Archive Writer** (`internal/infrastructure/archive`)
- ZIP archive creation
- Metadata file generation
- SHA256 checksum calculation

### 4. Domain Layer

**Types** (`internal/domain`)
- Core data structures
- Business entities
- Configuration models

## Data Flow

```
User → Web UI → HTTP Server → VMService → VM Client → VictoriaMetrics
                                    ↓
                              ExportService
                                    ↓
                              Obfuscator (optional)
                                    ↓
                              Archive Writer
                                    ↓
                              ZIP Download → User
```

## API Endpoints

### POST /api/validate
Validates VictoriaMetrics connection.

**Request:**
```json
{
  "url": "http://localhost:8428",
  "auth": {"type": "none"}
}
```

**Response:**
```json
{
  "success": true,
  "version": "v1.95.1",
  "vm_components": ["vmsingle"]
}
```

### POST /api/discover
Discovers VM components and jobs.

**Response:**
```json
{
  "components": [
    {
      "name": "vmstorage",
      "jobs": ["vmstorage-0", "vmstorage-1"]
    }
  ]
}
```

### POST /api/sample
Fetches sample metrics for preview.

**Response:**
```json
{
  "metrics": [
    {
      "name": "vm_rows",
      "labels": {"instance": "localhost:8482", "job": "vmstorage"}
    }
  ]
}
```

### POST /api/export
Executes full metrics export.

**Response:**
```json
{
  "archive_path": "/tmp/export_123.zip",
  "sample_data": [...]
}
```

### GET /api/download?path=...
Downloads the export archive.

## Obfuscation Strategy

### IP Addresses
- Original: `192.168.1.10:8482`
- Obfuscated: `777.777.1.1:8482`
- Port preserved for debugging

### Job Names
- Original: `production-vmstorage`
- Obfuscated: `vmstorage-job-1`
- Component type preserved

### Custom Labels
- User selects labels to obfuscate
- Deterministic mapping (same input = same output)
- Mapping stored in `obfuscation_map.json`

## Security

### Credentials
- Never persisted to disk
- Never logged
- Transmitted only to VictoriaMetrics
- Cleared from memory after use

### TLS
- Supports HTTPS connections
- Optional TLS verification skip

### Obfuscation
- Deterministic (reversible with map)
- Preserves structure for debugging
- User controls what to obfuscate

## Testing

### Unit Tests (50+)
- Domain logic
- VM client
- Obfuscation
- Archive creation

### E2E Tests (31)
- Playwright browser automation
- Full wizard flow
- Multi-stage validation
- Real VictoriaMetrics instances

### Integration Tests (14 scenarios)
- Docker Compose environment
- VMSingle, VMCluster, VMAuth
- Various authentication methods
- Multitenant configurations

## Performance

### Memory
- Streaming architecture
- No full dataset in memory
- Suitable for large exports

### Speed
- Parallel processing where possible
- Efficient JSONL parsing
- Compressed output


