# Release Guide

You must have permissions to push to `victoriametrics/vmgather` on Docker Hub.

## How to release

### 1. Create Tag on GitHub

1. Push all your changes to `master`.
2. Open [Releases Page](https://github.com/VictoriaMetrics/vmgather/releases).
3. Click **Draft a new release**.
4. Enter tag version (for example `v1.4.1`).
5. Click **Publish release**.

GitHub Actions will automatically build binaries (`.tar.gz`) for the release.

### 2. Publish Docker Images (Local)

You need to do this step from your computer.

1. Login to registries (if you didn't do it before):
   ```bash
   docker login docker.io
   docker login ghcr.io
   ```

2. Fetch the new tag from GitHub:
   ```bash
   git fetch --tags
   ```

3. Build and push images:
   ```bash
   make release
   ```

This command will:
- Build images for `linux/amd64`, `linux/arm64`, `linux/arm`.
- Push them to Docker Hub and GHCR.
- Update the `latest` tag.

## Checks

Check that images appeared here:
*   **Docker Hub:**
    *   https://hub.docker.com/r/victoriametrics/vmgather/tags
    *   https://hub.docker.com/r/victoriametrics/vmimporter/tags
*   **Quay.io:**
    *   https://quay.io/repository/victoriametrics/vmgather?tab=tags
    *   https://quay.io/repository/victoriametrics/vmimporter?tab=tags

If `make release` fails with "unauthorized", check your docker permissions.
