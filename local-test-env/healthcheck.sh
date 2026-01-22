#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="${VMGATHER_ENV_FILE:-.env.dynamic}"
if [ -f "$ENV_FILE" ]; then
  set -a
  . "$ENV_FILE"
  set +a
fi

VM_SINGLE_URL="${VM_SINGLE_URL:-${VM_SINGLE_NOAUTH_URL:-http://localhost:18428}}"
VM_CLUSTER_URL="${VM_CLUSTER_URL:-${VM_CLUSTER_SELECT_TENANT_0:-http://localhost:8481/select/0/prometheus}}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-60}"
SLEEP_SECONDS="${SLEEP_SECONDS:-3}"

echo "[healthcheck] waiting for vm_app_version at ${VM_SINGLE_URL}"
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  response=$(curl -fs "${VM_SINGLE_URL}/api/v1/query?query=vm_app_version")
  if echo "$response" | grep -q '"result":\[' && ! echo "$response" | grep -q '"result":\[\]'; then
    # Extract timestamp and check if it's fresh (within last 120 seconds)
    timestamp=$(echo "$response" | grep -o '"value":\[[0-9.]*' | head -1 | grep -o '[0-9.]*$' | cut -d. -f1)
    current_time=$(date +%s)
    if [ -n "$timestamp" ] && [ $((current_time - timestamp)) -lt 120 ]; then
      echo "[healthcheck] vm_app_version available (fresh) at ${VM_SINGLE_URL}"
      break
    fi
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "[healthcheck] waiting for vm_app_version at ${VM_CLUSTER_URL}"
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  response=$(curl -fs "${VM_CLUSTER_URL}/api/v1/query?query=vm_app_version")
  if echo "$response" | grep -q '"result":\[' && ! echo "$response" | grep -q '"result":\[\]'; then
    # Extract timestamp and check if it's fresh (within last 120 seconds)
    timestamp=$(echo "$response" | grep -o '"value":\[[0-9.]*' | head -1 | grep -o '[0-9.]*$' | cut -d. -f1)
    current_time=$(date +%s)
    if [ -n "$timestamp" ] && [ $((current_time - timestamp)) -lt 120 ]; then
      echo "[healthcheck] vm_app_version available (fresh) at ${VM_CLUSTER_URL}"
      exit 0
    fi
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "[healthcheck] ERROR: vm_app_version not found at ${VM_SINGLE_URL} or ${VM_CLUSTER_URL} within ${TIMEOUT_SECONDS}s"
exit 1
