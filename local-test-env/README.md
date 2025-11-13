# Local Test Environment

This directory contains a complete Docker-based test environment for VMExporter with VictoriaMetrics.

## Quick Start

```bash
# Start all VictoriaMetrics instances
docker-compose -f docker-compose.test.yml up -d

# Wait 30 seconds for metrics to be collected
sleep 30

# Run integration tests
cd ..
INTEGRATION_TEST=1 go test ./tests/integration/...

# Run E2E tests
cd tests/e2e
npm install
npm test

# Stop environment
cd ../../local-test-env
docker-compose -f docker-compose.test.yml down
```

## What's Included

This environment provides:

1. **VictoriaMetrics Single** (no auth)
   - Port: 8428
   - URL: `http://localhost:8428`

2. **VictoriaMetrics Single** (with VMAuth)
   - Port: 8427
   - URL: `http://localhost:8427`
   - User: `monitoring-read`
   - Password: `secret-password-123`

3. **VictoriaMetrics Cluster** (no auth)
   - VMSelect: 8481
   - VMInsert: 8480
   - VMStorage: 8482, 8483

4. **VictoriaMetrics Cluster** (with VMAuth)
   - Port: 8426
   - Multi-tenant with `/rw/prometheus` and `/r/prometheus` paths
   - Simulates VictoriaMetrics Managed (MaaS) configuration

5. **Export API Test Environment**
   - Port: 8425
   - Tenant 1011: Legacy (no export API) - tests fallback
   - Tenant 2022: Modern (with export API) - tests direct export

6. **Nginx Proxy**
   - Port: 8888
   - Provides domain-based routing
   - Strips tenant prefixes

7. **VAgent**
   - Scrapes all VM components
   - Writes to multiple tenants (0, 1011, 2022)

## Test Scenarios

### Scenario 1: VMSingle No Auth
```bash
curl http://localhost:8428/api/v1/query?query=up
```

### Scenario 2: VMSingle with Auth
```bash
curl -u monitoring-read:secret-password-123 \
  http://localhost:8427/api/v1/query?query=up
```

### Scenario 3: VM Cluster
```bash
curl http://localhost:8481/select/0/prometheus/api/v1/query?query=up
```

### Scenario 4: VM Cluster with VMAuth (MaaS-like)
```bash
curl -u monitoring-rw:secret-password-123 \
  http://localhost:8426/1011/rw/prometheus/api/v1/query?query=up
```

### Scenario 5: Export API Fallback
```bash
# Legacy tenant (will fallback to query_range)
curl -u tenant1011-legacy:password \
  http://localhost:8425/api/v1/export

# Modern tenant (direct export)
curl -u tenant2022-modern:password \
  http://localhost:8425/api/v1/export
```

## Configuration Files

- `docker-compose.test.yml` - Main Docker Compose configuration
- `test-configs/vmauth-*.yml` - VMAuth configurations for different scenarios
- `test-configs/prometheus.yml` - Prometheus scrape configuration
- `test-configs/nginx.conf` - Nginx reverse proxy configuration

## Testing VMExporter

### Manual Testing

1. Start the environment:
```bash
docker-compose -f docker-compose.test.yml up -d
sleep 30
```

2. Run VMExporter:
```bash
cd ..
./vmexporter
```

3. Open browser: `http://localhost:8080`

4. Test different scenarios:
   - VMSingle: `http://localhost:8428`
   - VMAuth: `http://localhost:8427` (user: `monitoring-read`, password: `secret-password-123`)
   - Cluster: `http://localhost:8481/select/0/prometheus`
   - MaaS-like: `http://localhost:8426/1011/rw/prometheus`

### Automated Testing

```bash
# Integration tests
INTEGRATION_TEST=1 go test ./tests/integration/...

# E2E tests
cd tests/e2e
npm install
npm test
```

## Troubleshooting

### No metrics available
Wait 30-60 seconds after starting for vmagent to scrape metrics.

### Connection refused
Ensure all containers are running:
```bash
docker-compose -f docker-compose.test.yml ps
```

### Port conflicts
Stop conflicting services or change ports in `docker-compose.test.yml`.

## Cleanup

```bash
# Stop and remove all containers
docker-compose -f docker-compose.test.yml down -v

# Remove volumes (clears all data)
docker volume prune
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Test Environment                         │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  VMSingle    │  │  VMSingle    │  │  VM Cluster  │      │
│  │  (no auth)   │  │  (+ VMAuth)  │  │  (3 nodes)   │      │
│  │  :8428       │  │  :8427       │  │  :8481       │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  VMAuth      │  │  VMAuth      │  │  Nginx       │      │
│  │  (cluster)   │  │  (export)    │  │  (proxy)     │      │
│  │  :8426       │  │  :8425       │  │  :8888       │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│                                                               │
│  ┌──────────────┐                                            │
│  │  VAgent      │  ← Scrapes all components                 │
│  │  :8430       │  → Writes to all tenants                  │
│  └──────────────┘                                            │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

## Notes

- All passwords are for testing only
- Data is ephemeral (stored in Docker volumes)
- Environment is designed for local development and testing
- Not suitable for production use

## Requirements

- Docker 20.10+
- Docker Compose 2.0+
- 4GB RAM minimum
- 10GB disk space

## License

Same as VMExporter - Apache 2.0
