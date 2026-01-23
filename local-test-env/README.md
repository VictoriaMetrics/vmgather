# vmgather local test environment

Docker Compose stack that mirrors common VictoriaMetrics deployments for integration tests, Playwright suites, and manual verification.

## Contents

1. [Topology](#topology)
2. [Quick start](#quick-start)
3. [Scenarios](#scenarios)
4. [Configuration files](#configuration-files)
5. [Using with vmgather](#using-with-vmgather)
6. [Troubleshooting](#troubleshooting)
7. [Cleanup](#cleanup)

## Topology

| Component | Purpose | Ports / credentials |
| --- | --- | --- |
| VMSingle (no auth) | Baseline VictoriaMetrics single-node | `http://localhost:18428` |
| VMSingle + VMAuth | Tests Basic Auth and VMAuth headers | `http://localhost:8427`, user `monitoring-read`, pass `secret-password-123` |
| VMCluster | vmselect/vmstorage/vminsert trio | vmselect `:8481`, vminsert `:8480`, vmstorage `:8482/:8483` |
| VMSelect standalone | vmselect backed by a single vmstorage | `http://localhost:8491` |
| VMCluster + VMAuth | Emulates VictoriaMetrics Managed (`/rw/prometheus` and `/r/prometheus`) | `http://localhost:8426/<tenant>/rw/prometheus` |
| Export API tenants | Validates `/api/v1/export` vs fallback | `http://localhost:8425`, tenant `1011` (legacy), `2022` (modern) |
| NGINX proxy | Domain routing + tenant prefix stripping | `http://localhost:8888` |
| vmagent | Scrapes all the above, writes to tenants `0/1011/2022` | `:8430` |

Custom test jobs (`test1`, `test2`) are also scraped from `test-data-generator:9090` to simulate non-VM workloads. These should appear in selector/query mode but not in cluster component discovery.

## Quick start

```bash
# From repo root, start the stack (auto-picks free ports)
make test-env-up
# Allow vmagent to collect some metrics
sleep 30

# Run integration tests
cd ..
INTEGRATION_TEST=1 go test ./tests/integration/...

# Run Playwright tests
cd tests/e2e
npm install
npm test

# Stop everything when done
make test-env-down
```

## Scenarios

### Scenario 1 – VMSingle (no auth)
```bash
curl http://localhost:18428/api/v1/query?query=up
```

### Scenario 2 – VMSingle behind VMAuth
```bash
curl -u monitoring-read:secret-password-123 \
  http://localhost:8427/api/v1/query?query=up
```

### Scenario 3 – VMCluster tenant 0
```bash
curl http://localhost:8481/select/0/prometheus/api/v1/query?query=up
```

### Scenario 3b – Standalone vmselect (single storage)
```bash
curl http://localhost:8491/select/0/prometheus/api/v1/query?query=up
```

### Scenario 4 – VMCluster + VMAuth (Managed-style)
```bash
curl -u monitoring-rw:secret-password-123 \
  http://localhost:8426/1011/rw/prometheus/api/v1/query?query=up
```

### Scenario 5 – Export API vs fallback
```bash
# Legacy tenant – should fall back to query_range
curl -u tenant1011-legacy:password \
  http://localhost:8425/api/v1/export

# Modern tenant – streaming export available
curl -u tenant2022-modern:password \
  http://localhost:8425/api/v1/export
```

## Configuration files

- `docker-compose.test.yml` – spins up all services.
- `test-configs/vmauth-*.yml` – VMAuth rules for various tenants.
- `test-configs/prometheus.yml` – vmagent scrape definitions.
- `test-configs/nginx.conf` – reverse proxy for domain-based routing.

Ports are auto-assigned by `testconfig bootstrap`, which writes `local-test-env/.env.dynamic`.
You can override any port or URL by setting env vars before running `make test-env-up`.

## Using with vmgather

1. Start the stack and wait ~30 seconds.
2. From repo root run `./vmgather`.
3. In the wizard, try:
   - `http://localhost:18428` (no auth).
   - `http://localhost:8427` with Basic Auth.
   - `http://localhost:8481/select/0/prometheus`.
   - `http://localhost:8491/select/0/prometheus` (standalone vmselect).
   - `http://localhost:8426/1011/rw/prometheus` for Managed-style paths.
4. Validate obfuscation by exporting small windows and inspecting the bundle.

## Troubleshooting

- **No metrics yet** – vmagent needs 30–60 seconds after boot.
- **Connection refused** – check container status: `docker-compose -f docker-compose.test.yml ps`.
- **Port clash** – edit the compose file and restart.
- **Export errors** – consult service logs: `docker-compose -f docker-compose.test.yml logs vmselect`.

## Cleanup

```bash
docker-compose -f docker-compose.test.yml down -v
docker volume prune
```

## Requirements

- Docker 20.10+
- Docker Compose 2.0+
- 4GB RAM minimum
- 10GB free disk space
- Go 1.22+ (for testconfig utility)
- make (for Makefile targets)

## Configuration

Test environment uses type-safe Go configuration (`local-test-env/config.go`) that automatically detects:
- Local vs Docker environment
- `localhost` vs `host.docker.internal` vs Docker network names
- All URLs and credentials from environment variables with sensible defaults

```bash
# Generate dynamic ports and env file (auto-called by make test-env-up)
make test-config-bootstrap

# Validate configuration
make test-config-validate

# View configuration as JSON
make test-config-json

# View configuration as environment variables
make test-config-env
```

Override any setting via environment variables:
```bash
export VM_SINGLE_NOAUTH_URL=http://custom-host:18428
export VM_SINGLE_AUTH_USER=custom-user
make test-scenarios
```
