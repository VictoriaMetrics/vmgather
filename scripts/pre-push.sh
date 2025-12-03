#!/usr/bin/env bash
set -e

echo "Running full test suite before push..."
echo "This includes cleaning up the test environment, starting it fresh, and running all tests."

# Go to project root
cd "$(dirname "$0")/.."

# Run the full test cycle
make test-env-full

echo "✅ All tests passed! Ready to push."
