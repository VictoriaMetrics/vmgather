#!/bin/bash

# Comprehensive test script for all VM scenarios
# Tests all scenarios with dynamic configuration from testconfig utility

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

PASSED=0
FAILED=0
TOTAL=0

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "${BLUE}===============================================================================${NC}"
echo -e "${BLUE}VMGather - Comprehensive Scenario Testing${NC}"
echo -e "${BLUE}===============================================================================${NC}"
echo ""

# Check for testconfig utility
if [ ! -f "$SCRIPT_DIR/testconfig" ]; then
    echo -e "${YELLOW}[WARN] testconfig not found, building...${NC}"
    cd "$SCRIPT_DIR" && go build -o testconfig .
fi

# Load configuration from testconfig
echo -e "${BLUE}[INFO] Loading test configuration...${NC}"
if ! "$SCRIPT_DIR/testconfig" validate > /dev/null 2>&1; then
    echo -e "${RED}[ERROR] Configuration validation failed${NC}"
    "$SCRIPT_DIR/testconfig" validate
    exit 1
fi

# Export configuration as environment variables
CONFIG_ENV=$("$SCRIPT_DIR/testconfig" env 2>&1) || {
    echo -e "${RED}[ERROR] Failed to export configuration${NC}"
    echo "$CONFIG_ENV"
    exit 1
}
eval "$CONFIG_ENV"

echo -e "${GREEN}[OK] Configuration loaded${NC}"
echo ""

# Function to test endpoint
test_endpoint() {
    local name="$1"
    local url="$2"
    local auth_type="$3"
    local auth_value="$4"
    
    TOTAL=$((TOTAL + 1))
    echo -e "${YELLOW}[$TOTAL] Testing: $name${NC}"
    echo -e "    URL: $url"
    
    local curl_cmd="curl -s -f"
    
    case "$auth_type" in
        "basic")
            curl_cmd="$curl_cmd -u $auth_value"
            echo -e "    Auth: Basic ($auth_value)"
            ;;
        "bearer")
            curl_cmd="$curl_cmd -H \"Authorization: Bearer $auth_value\""
            echo -e "    Auth: Bearer"
            ;;
        "header")
            curl_cmd="$curl_cmd -H \"$auth_value\""
            echo -e "    Auth: Custom Header"
            ;;
        "none")
            echo -e "    Auth: None"
            ;;
    esac
    
    # Test query endpoint
    local query_url="${url}/api/v1/query?query=vm_app_version"
    
    if eval "$curl_cmd '$query_url'" > /dev/null 2>&1; then
        echo -e "    ${GREEN}[PASS]${NC}"
        PASSED=$((PASSED + 1))
    else
        echo -e "    ${RED}[FAIL]${NC}"
        FAILED=$((FAILED + 1))
    fi
    echo ""
}

# Wait for services to be ready
echo -e "${BLUE}[INFO] Waiting for services to be ready...${NC}"
sleep 5

echo -e "${BLUE}===============================================================================${NC}"
echo -e "${BLUE}Starting tests...${NC}"
echo -e "${BLUE}===============================================================================${NC}"
echo ""

# Scenario 1: VMSingle No Auth
test_endpoint \
    "VMSingle No Auth" \
    "$VM_SINGLE_NOAUTH_URL" \
    "none" \
    ""

# Scenario 2: VMSingle via VMAuth Basic
test_endpoint \
    "VMSingle via VMAuth Basic" \
    "$VM_SINGLE_AUTH_URL" \
    "basic" \
    "$VM_SINGLE_AUTH_USER:$VM_SINGLE_AUTH_PASS"

# Scenario 3: VMSingle Bearer Token
test_endpoint \
    "VMSingle Bearer Token" \
    "$VM_SINGLE_AUTH_URL" \
    "bearer" \
    "$TEST_BEARER_TOKEN"

# Scenario 4: Cluster No Auth - Tenant 0
test_endpoint \
    "Cluster No Auth - Tenant 0" \
    "$VM_CLUSTER_SELECT_TENANT_0" \
    "none" \
    ""

# Scenario 5: Cluster No Auth - Tenant 1011
test_endpoint \
    "Cluster No Auth - Tenant 1011" \
    "$VM_CLUSTER_SELECT_TENANT_1011" \
    "none" \
    ""

# Scenario 6: Cluster No Auth - Multitenant
test_endpoint \
    "Cluster No Auth - Multitenant" \
    "$VM_CLUSTER_SELECT_MULTITENANT" \
    "none" \
    ""

# Scenario 7: Cluster via VMAuth - Tenant 0
test_endpoint \
    "Cluster via VMAuth - Tenant 0" \
    "$VM_AUTH_CLUSTER_URL" \
    "basic" \
    "$VM_AUTH_TENANT_0_USER:$VM_AUTH_TENANT_0_PASS"

# Scenario 8: Cluster via VMAuth - Tenant 1011
test_endpoint \
    "Cluster via VMAuth - Tenant 1011" \
    "$VM_AUTH_CLUSTER_URL" \
    "basic" \
    "$VM_AUTH_TENANT_1011_USER:$VM_AUTH_TENANT_1011_PASS"

# Scenario 9: Cluster via VMAuth - Multitenant
test_endpoint \
    "Cluster via VMAuth - Multitenant" \
    "$VM_AUTH_CLUSTER_URL" \
    "basic" \
    "$VM_AUTH_MULTITENANT_USER:$VM_AUTH_MULTITENANT_PASS"

# Scenario 10: Cluster Bearer Token
test_endpoint \
    "Cluster Bearer Token" \
    "$VM_AUTH_CLUSTER_URL" \
    "bearer" \
    "$TEST_BEARER_TOKEN_CLUSTER"

# Scenario 11: Cluster Custom Header
test_endpoint \
    "Cluster Custom Header" \
    "$VM_AUTH_CLUSTER_URL" \
    "bearer" \
    "$TEST_BEARER_TOKEN_CUSTOM"

# Scenario 12: Full Grafana-like URL
test_endpoint \
    "Full Grafana-like URL" \
    "$VM_CLUSTER_SELECT_TENANT_1011" \
    "none" \
    ""

# Scenario 13: VMAuth Auto-routing
test_endpoint \
    "VMAuth Auto-routing" \
    "$VM_AUTH_CLUSTER_URL" \
    "basic" \
    "$VM_AUTH_TENANT_0_USER:$VM_AUTH_TENANT_0_PASS"

# Summary
echo -e "${BLUE}===============================================================================${NC}"
echo -e "${BLUE}Test Summary${NC}"
echo -e "${BLUE}===============================================================================${NC}"
echo ""
echo -e "Total Tests:  $TOTAL"
echo -e "${GREEN}Passed:       $PASSED${NC}"
if [ $FAILED -gt 0 ]; then
    echo -e "${RED}Failed:       $FAILED${NC}"
else
    echo -e "Failed:       $FAILED"
fi
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}===============================================================================${NC}"
    echo -e "${GREEN}ALL TESTS PASSED${NC}"
    echo -e "${GREEN}===============================================================================${NC}"
    exit 0
else
    echo -e "${RED}===============================================================================${NC}"
    echo -e "${RED}SOME TESTS FAILED${NC}"
    echo -e "${RED}===============================================================================${NC}"
    exit 1
fi

