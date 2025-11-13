#!/bin/bash

# 🧪 Comprehensive test script for all VM scenarios
# Tests all 14 scenarios from TEST_SCENARIOS.md

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

PASSED=0
FAILED=0
TOTAL=0

echo -e "${BLUE}═══════════════════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}🧪 VMexporter - Comprehensive Scenario Testing${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════════════════${NC}"
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
            curl_cmd="$curl_cmd -H 'Authorization: Bearer $auth_value'"
            echo -e "    Auth: Bearer"
            ;;
        "header")
            curl_cmd="$curl_cmd -H '$auth_value'"
            echo -e "    Auth: Custom Header"
            ;;
        "none")
            echo -e "    Auth: None"
            ;;
    esac
    
    # Test query endpoint
    local query_url="${url}/api/v1/query?query=vm_app_version"
    
    if eval "$curl_cmd '$query_url'" > /dev/null 2>&1; then
        echo -e "    ${GREEN}✅ PASSED${NC}"
        PASSED=$((PASSED + 1))
    else
        echo -e "    ${RED}❌ FAILED${NC}"
        FAILED=$((FAILED + 1))
    fi
    echo ""
}

# Wait for services to be ready
echo -e "${BLUE}⏳ Waiting for services to be ready...${NC}"
sleep 5

echo -e "${BLUE}═══════════════════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}📊 Starting tests...${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════════════════${NC}"
echo ""

# Scenario 1: VMSingle No Auth
test_endpoint \
    "VMSingle No Auth" \
    "http://localhost:8428" \
    "none" \
    ""

# Scenario 2: VMSingle No Auth + Path
test_endpoint \
    "VMSingle No Auth + Path" \
    "http://localhost:8428/prometheus" \
    "none" \
    ""

# Scenario 3: VMSingle via VMAuth Basic
test_endpoint \
    "VMSingle via VMAuth Basic" \
    "http://localhost:8427" \
    "basic" \
    "monitoring-read:secret-password-123"

# Scenario 4: VMSingle Bearer Token
test_endpoint \
    "VMSingle Bearer Token" \
    "http://localhost:8427" \
    "bearer" \
    "test-bearer-token-789"

# Scenario 5: Cluster No Auth - Tenant 0
test_endpoint \
    "Cluster No Auth - Tenant 0" \
    "http://localhost:8481/select/0/prometheus" \
    "none" \
    ""

# Scenario 6: Cluster No Auth - Tenant 1011
test_endpoint \
    "Cluster No Auth - Tenant 1011" \
    "http://localhost:8481/select/1011/prometheus" \
    "none" \
    ""

# Scenario 7: Cluster No Auth - Multitenant
test_endpoint \
    "Cluster No Auth - Multitenant" \
    "http://localhost:8481/select/multitenant/prometheus" \
    "none" \
    ""

# Scenario 8: Cluster via VMAuth - Tenant 0
test_endpoint \
    "Cluster via VMAuth - Tenant 0" \
    "http://localhost:8426" \
    "basic" \
    "tenant0-user:tenant0-pass"

# Scenario 9: Cluster via VMAuth - Tenant 1011
test_endpoint \
    "Cluster via VMAuth - Tenant 1011" \
    "http://localhost:8426" \
    "basic" \
    "tenant1011-user:tenant1011-pass"

# Scenario 10: Cluster via VMAuth - Multitenant
test_endpoint \
    "Cluster via VMAuth - Multitenant" \
    "http://localhost:8426" \
    "basic" \
    "admin-multitenant:admin-multi-pass"

# Scenario 11: Cluster Bearer Token
test_endpoint \
    "Cluster Bearer Token" \
    "http://localhost:8426" \
    "bearer" \
    "bearer-tenant0-token"

# Scenario 12: Cluster Custom Header (bearer token approach)
test_endpoint \
    "Cluster Custom Header" \
    "http://localhost:8426" \
    "bearer" \
    "custom-header-token-1011"

# Scenario 13: Full Grafana-like URL (Tenant 1011)
test_endpoint \
    "Full Grafana-like URL" \
    "http://localhost:8481/select/1011/prometheus" \
    "none" \
    ""

# Scenario 14: VMAuth without path (vmauth adds it automatically)
test_endpoint \
    "VMAuth Auto-routing" \
    "http://localhost:8426" \
    "basic" \
    "tenant0-user:tenant0-pass"

# Summary
echo -e "${BLUE}═══════════════════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}📊 Test Summary${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════════════════${NC}"
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
    echo -e "${GREEN}═══════════════════════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}✅ ALL TESTS PASSED! 🎉${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════════════════════════════════════${NC}"
    exit 0
else
    echo -e "${RED}═══════════════════════════════════════════════════════════════════════════${NC}"
    echo -e "${RED}❌ SOME TESTS FAILED!${NC}"
    echo -e "${RED}═══════════════════════════════════════════════════════════════════════════${NC}"
    exit 1
fi

