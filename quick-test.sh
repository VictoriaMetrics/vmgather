#!/bin/bash

# Quick test script to verify VMexporter works with local test env

set -e

echo "🧪 Quick Test: VMexporter + Local Test Env"
echo ""

# Check if test env is running
if ! curl -s http://localhost:8428/metrics > /dev/null 2>&1; then
    echo "❌ Test environment not running!"
    echo "Run: make test-env-up"
    exit 1
fi

echo "✅ Test environment is running"
echo ""

# Start VMexporter in background
echo "🚀 Starting VMexporter..."
./vmexporter -no-browser > /tmp/vmexporter.log 2>&1 &
VMEXPORTER_PID=$!
echo "   PID: $VMEXPORTER_PID"

# Wait for it to start
sleep 2

# Check if it's running
if ! ps -p $VMEXPORTER_PID > /dev/null; then
    echo "❌ VMexporter failed to start!"
    cat /tmp/vmexporter.log
    exit 1
fi

echo "✅ VMexporter started on http://localhost:8080"
echo ""

# Test connection validation
echo "🔍 Testing connection validation..."
RESPONSE=$(curl -s -X POST http://localhost:8080/api/validate \
    -H 'Content-Type: application/json' \
    -d '{
        "connection": {
            "url": "http://localhost:8428",
            "auth": {"type": "none"}
        }
    }')

if echo "$RESPONSE" | grep -q '"success":true'; then
    echo "✅ Connection validation works!"
    echo "   Response: $RESPONSE"
else
    echo "❌ Connection validation failed!"
    echo "   Response: $RESPONSE"
    kill $VMEXPORTER_PID
    exit 1
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ Quick test PASSED!"
echo ""
echo "VMexporter is running at: http://localhost:8080"
echo "Server logs: /tmp/vmexporter.log"
echo "PID: $VMEXPORTER_PID"
echo ""
echo "To stop: kill $VMEXPORTER_PID"
echo "To view logs: tail -f /tmp/vmexporter.log"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

