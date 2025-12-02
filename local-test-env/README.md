# VMGather local test environment

Docker Compose stack that mirrors common VictoriaMetrics deployments for integration tests, Playwright suites, and manual verification.

## Contents

1. [Topology](#topology)
2. [Quick start](#quick-start)
3. [Scenarios](#scenarios)
4. [Configuration files](#configuration-files)
5. [Using with VMGather](#using-with-vmgather)
6. [Troubleshooting](#troubleshooting)
7. [Cleanup](#cleanup)

## Topology

| Component | Purpose | Ports / credentials |
| --- | --- | --- |
| VMSingle (no auth) | Baseline VictoriaMetrics single-node | `http://localhost:8428` |
| VMSingle + VMAuth | Tests Basic Auth and VMAuth headers | `http://localhost:8427`, user `monitoring-read`, pass `secret-password-123` |
| VMCluster | vmselect/vmstorage/vminsert trio | vmselect `:8481`, vminsert `:8480`, vmstorage `:8482/:8483` |
| VMCluster + VMAuth | Emulates VictoriaMetrics Managed (`/rw/prometheus` and `/r/prometheus`) | `http://localhost:8426/<tenant>/rw/prometheus` |
| Export API tenants | Validates `/api/v1/export` vs fallback | `http://localhost:8425`, tenant `1011` (legacy), `2022` (modern) |
| NGINX proxy | Domain routing + tenant prefix stripping | `http://localhost:8888` |
| vmagent | Scrapes all the above, writes to tenants `0/1011/2022` | `:8430` |

## Quick start

```bash
# Start the stack
docker-compose -f docker-compose.test.yml up -d
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
cd ../../local-test-env
docker-compose -f docker-compose.test.yml down
```

## Scenarios

### Scenario 1 – VMSingle (no auth)
```bash
curl http://localhost:8428/api/v1/query?query=up
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

Adjust ports or credentials there if they conflict with local services.

## Using with VMGather

1. Start the stack and wait ~30 seconds.
2. From repo root run `./vmgather`.
3. In the wizard, try:
   - `http://localhost:8428` (no auth).
   - `http://localhost:8427` with Basic Auth.
   - `http://localhost:8481/select/0/prometheus`.
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
