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
VMSELECT_STANDALONE_URL="${VMSELECT_STANDALONE_URL:-http://localhost:8491}"
VMSELECT_STANDALONE_SELECT_TENANT_0="${VMSELECT_STANDALONE_SELECT_TENANT_0:-${VMSELECT_STANDALONE_URL}/select/0/prometheus}"
VM_AUTH_EXPORT_URL="${VM_AUTH_EXPORT_URL:-http://localhost:${VM_AUTH_EXPORT_PORT:-8425}}"

# VMAuth export-test credentials (defaults match local-test-env/test-configs/vmauth-export-test.yml)
VM_AUTH_EXPORT_USER="${VM_AUTH_EXPORT_MODERN_USER:-tenant2022-modern}"
VM_AUTH_EXPORT_PASS="${VM_AUTH_EXPORT_MODERN_PASS:-modern-pass-2022}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-60}"
SLEEP_SECONDS="${SLEEP_SECONDS:-3}"

echo "[healthcheck] waiting for vm_app_version at ${VM_SINGLE_URL}"
single_ok=0
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  response=$(curl -fs "${VM_SINGLE_URL}/api/v1/query?query=vm_app_version")
  if echo "$response" | grep -q '"result":\[' && ! echo "$response" | grep -q '"result":\[\]'; then
    # Extract timestamp and check if it's fresh (within last 120 seconds)
    timestamp=$(echo "$response" | grep -o '"value":\[[0-9.]*' | head -1 | grep -o '[0-9.]*$' | cut -d. -f1)
    current_time=$(date +%s)
    if [ -n "$timestamp" ] && [ $((current_time - timestamp)) -lt 120 ]; then
      echo "[healthcheck] vm_app_version available (fresh) at ${VM_SINGLE_URL}"
      single_ok=1
      break
    fi
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "[healthcheck] waiting for vm_app_version at ${VM_CLUSTER_URL}"
cluster_ok=0
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  response=$(curl -fs "${VM_CLUSTER_URL}/api/v1/query?query=vm_app_version")
  if echo "$response" | grep -q '"result":\[' && ! echo "$response" | grep -q '"result":\[\]'; then
    # Extract timestamp and check if it's fresh (within last 120 seconds)
    timestamp=$(echo "$response" | grep -o '"value":\[[0-9.]*' | head -1 | grep -o '[0-9.]*$' | cut -d. -f1)
    current_time=$(date +%s)
    if [ -n "$timestamp" ] && [ $((current_time - timestamp)) -lt 120 ]; then
      echo "[healthcheck] vm_app_version available (fresh) at ${VM_CLUSTER_URL}"
      cluster_ok=1
      break
    fi
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "[healthcheck] waiting for vm_app_version at ${VMSELECT_STANDALONE_SELECT_TENANT_0}"
vmselect_ok=0
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  response=$(curl -fs "${VMSELECT_STANDALONE_SELECT_TENANT_0}/api/v1/query?query=vm_app_version")
  if echo "$response" | grep -q '"result":\[' && ! echo "$response" | grep -q '"result":\[\]'; then
    # Extract timestamp and check if it's fresh (within last 120 seconds)
    timestamp=$(echo "$response" | grep -o '"value":\[[0-9.]*' | head -1 | grep -o '[0-9.]*$' | cut -d. -f1)
    current_time=$(date +%s)
    if [ -n "$timestamp" ] && [ $((current_time - timestamp)) -lt 120 ]; then
      echo "[healthcheck] vm_app_version available (fresh) at ${VMSELECT_STANDALONE_SELECT_TENANT_0}"
      vmselect_ok=1
      break
    fi
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "[healthcheck] waiting for vm_app_version via vmauth-export-test at ${VM_AUTH_EXPORT_URL} (tenant 2022)"
export_ok=0
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  response=$(curl -fs -u "${VM_AUTH_EXPORT_USER}:${VM_AUTH_EXPORT_PASS}" \
    "${VM_AUTH_EXPORT_URL}/api/v1/query?query=vm_app_version")
  if echo "$response" | grep -q '"result":\[' && ! echo "$response" | grep -q '"result":\[\]'; then
    timestamp=$(echo "$response" | grep -o '"value":\[[0-9.]*' | head -1 | grep -o '[0-9.]*$' | cut -d. -f1)
    current_time=$(date +%s)
    if [ -n "$timestamp" ] && [ $((current_time - timestamp)) -lt 120 ]; then
      echo "[healthcheck] vm_app_version available (fresh) via vmauth-export-test at ${VM_AUTH_EXPORT_URL}"
      export_ok=1
      break
    fi
  fi
  sleep "${SLEEP_SECONDS}"
done

if [ "$single_ok" -eq 1 ] && [ "$cluster_ok" -eq 1 ] && [ "$vmselect_ok" -eq 1 ] && [ "$export_ok" -eq 1 ]; then
  exit 0
fi

echo "[healthcheck] ERROR: vm_app_version not found within ${TIMEOUT_SECONDS}s"
echo "[healthcheck]   - ${VM_SINGLE_URL}"
echo "[healthcheck]   - ${VM_CLUSTER_URL}"
echo "[healthcheck]   - ${VMSELECT_STANDALONE_SELECT_TENANT_0}"
echo "[healthcheck]   - ${VM_AUTH_EXPORT_URL} (vmauth-export-test)"
exit 1
