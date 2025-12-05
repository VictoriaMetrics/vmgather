.PHONY: test test-fast test-llm build build-safe build-all clean fmt lint help test-env test-env-up test-env-down test-env-logs test-scenarios docker-build docker-build-vmgather docker-build-vmimporter

VERSION ?= $(shell git describe --tags --always --dirty)
PKG_TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "latest")
# Platforms supported by VictoriaMetrics (consistent with upstream)
# Platforms supported by VictoriaMetrics (limited by distroless support)
PLATFORMS ?= linux/amd64,linux/arm64,linux/arm

# Go version for build
GO_VERSION ?= 1.22
DOCKER_OUTPUT ?= type=docker
DOCKER_COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")

# Docker registries and namespace (standard across VictoriaMetrics)
# NOTE: quay.io disabled temporarily due to permission issues
DOCKER_REGISTRIES ?= docker.io ghcr.io
# DOCKER_REGISTRIES ?= docker.io quay.io ghcr.io
DOCKER_NAMESPACE ?= victoriametrics

# Default target: show help
.DEFAULT_GOAL := help

# Alias for release publishing (consistent with VM standards)
release: publish-via-docker

# ... (snipped) ...


# Valid target for publish-via-docker
publish-via-docker: publish-vmgather publish-vmimporter

publish-vmgather:
	@echo "Building and pushing vmgather:$(PKG_TAG)"
	@docker buildx build \
		--platform $(PLATFORMS) \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--label "org.opencontainers.image.source=https://github.com/VictoriaMetrics/vmgather" \
		--label "org.opencontainers.image.vendor=VictoriaMetrics" \
		--label "org.opencontainers.image.version=$(PKG_TAG)" \
		--label "org.opencontainers.image.created=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')" \
		-f build/docker/Dockerfile.vmgather \
		$(foreach registry,$(DOCKER_REGISTRIES), \
			--tag $(registry)/$(DOCKER_NAMESPACE)/vmgather:$(PKG_TAG) \
			--tag $(registry)/$(DOCKER_NAMESPACE)/vmgather:latest \
		) \
		--push \
		.

publish-vmimporter:
	@echo "Building and pushing vmimporter:$(PKG_TAG)"
	@docker buildx build \
		--platform $(PLATFORMS) \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--label "org.opencontainers.image.source=https://github.com/VictoriaMetrics/vmgather" \
		--label "org.opencontainers.image.vendor=VictoriaMetrics" \
		--label "org.opencontainers.image.version=$(PKG_TAG)" \
		--label "org.opencontainers.image.created=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')" \
		-f build/docker/Dockerfile.vmimporter \
		$(foreach registry,$(DOCKER_REGISTRIES), \
			--tag $(registry)/$(DOCKER_NAMESPACE)/vmimporter:$(PKG_TAG) \
			--tag $(registry)/$(DOCKER_NAMESPACE)/vmimporter:latest \
		) \
		--push \
		.

# =============================================================================
# HELP - Display available targets
# =============================================================================
help:
	@echo "================================================================================"
	@echo "vmgather - Makefile Commands"
	@echo "================================================================================"
	@echo ""
	@echo "BUILD COMMANDS:"
	@echo "  make build        - Build binary (with automatic tests)"
	@echo "  make build-safe   - Build with full test suite + linting"
	@echo "  make build-all    - Build for all platforms (8 targets)"
	@echo "  make publish-via-docker - Build & Push multi-arch images to registries"
	@echo "  make release      - Alias for publish-via-docker"
	@echo "  make clean        - Clean build artifacts"
	@echo ""
	@echo "TEST COMMANDS:"
	@echo "  make test         - Run all tests (fast mode, no race detector)"
	@echo "  make test-fast    - Run tests with -short flag (skip slow tests)"
	@echo "  make test-full    - Run complete test suite with race detector"
	@echo "  make test-llm     - Run tests with LLM-friendly structured output"
	@echo "  make test-coverage - Generate HTML coverage report"
	@echo ""
	@echo "SPECIFIC TEST TARGETS:"
	@echo "  make test-vm           - Test VM client only"
	@echo "  make test-obfuscation  - Test obfuscation only"
	@echo "  make test-service      - Test services only"
	@echo "  make test-archive      - Test archive writer only"
	@echo "  make test-builder      - Test build system"
	@echo ""
	@echo "DEVELOPMENT:"
	@echo "  make fmt    - Format code"
	@echo "  make lint   - Run linter"
	@echo ""
	@echo "TEST ENVIRONMENT (Docker):"
	@echo "  make test-env-up       - Start all VM test instances"
	@echo "  make test-scenarios    - Test all 14 scenarios"
	@echo "  make test-env          - Full E2E: start + test"
	@echo "  make test-env-down     - Stop test environment"
	@echo "  make test-env-clean    - Stop and remove all data"
	@echo "  make test-env-logs     - Show logs"
	@echo ""
	@echo "================================================================================"

# =============================================================================
# TEST TARGETS - Automatic test execution with LLM-friendly output
# =============================================================================

# Fast tests for development (default for build)
test:
	@echo "================================================================================"
	@echo "TEST SUITE: Fast Mode (no race detector, skip slow tests)"
	@echo "================================================================================"
	@echo ""
	@go test -short -coverprofile=coverage.out ./... | $(MAKE) --no-print-directory format-test-output
	@echo ""
	@$(MAKE) --no-print-directory test-summary

# Fast tests without coverage
test-fast:
	@echo "================================================================================"
	@echo "TEST SUITE: Ultra-Fast Mode (no coverage, skip slow tests)"
	@echo "================================================================================"
	@echo ""
	@go test -short ./... | $(MAKE) --no-print-directory format-test-output
	@echo ""
	@$(MAKE) --no-print-directory test-summary

# Full test suite with race detector
test-full:
	@echo "================================================================================"
	@echo "TEST SUITE: Full Mode (race detector + all tests)"
	@echo "================================================================================"
	@echo ""
	@go test -v -race -coverprofile=coverage.out ./... | $(MAKE) --no-print-directory format-test-output
	@echo ""
	@$(MAKE) --no-print-directory test-summary

# LLM-friendly structured output (best for CI and LLM agents)
test-llm:
	@echo "+===============================================================================+"
	@echo "| LLM-FRIENDLY TEST REPORT                                                     |"
	@echo "+===============================================================================+"
	@echo ""
	@echo "+-------------------------------------------------------------------------------+"
	@echo "| TEST EXECUTION START                                                         |"
	@echo "| Timestamp: $$(date '+%Y-%m-%d %H:%M:%S')                                    |"
	@echo "+-------------------------------------------------------------------------------+"
	@echo ""
	@go test -json -short ./... 2>&1 | $(MAKE) --no-print-directory parse-json-output || true
	@echo ""
	@echo "+-------------------------------------------------------------------------------+"
	@echo "| TEST EXECUTION END                                                           |"
	@echo "+-------------------------------------------------------------------------------+"
	@$(MAKE) --no-print-directory test-summary-detailed

# Show test coverage in browser
test-coverage: test
	@echo "Opening coverage report in browser..."
	@go tool cover -html=coverage.out

# Internal target: Format test output for readability
format-test-output:
	@awk '{ \
		if ($$0 ~ /^PASS/) { print "[PASS] " $$0; } \
		else if ($$0 ~ /^FAIL/) { print "[FAIL] " $$0; } \
		else if ($$0 ~ /^ok/) { print "[OK] " $$0; } \
		else if ($$0 ~ /--- PASS:/) { print "  [OK] " $$0; } \
		else if ($$0 ~ /--- FAIL:/) { print "  [FAIL] " $$0; } \
		else if ($$0 ~ /=== RUN/) { print "  [RUN] " $$0; } \
		else { print $$0; } \
	}'

# Internal target: Parse JSON test output (for -json flag)
parse-json-output:
	@python3 -c 'import sys, json; \
	test_counts = {"pass": 0, "fail": 0, "skip": 0}; \
	failed_tests = []; \
	for line in sys.stdin: \
		try: \
			obj = json.loads(line.strip()); \
			if obj.get("Action") == "pass" and "Test" in obj: \
				test_counts["pass"] += 1; \
				print(f"  [PASS] {obj.get(\"Package\", \"\")} :: {obj.get(\"Test\", \"\")} ({obj.get(\"Elapsed\", 0):.3f}s)"); \
			elif obj.get("Action") == "fail" and "Test" in obj: \
				test_counts["fail"] += 1; \
				failed_tests.append(f"{obj.get(\"Package\", \"\")} :: {obj.get(\"Test\", \"\")}"); \
				print(f"  [FAIL] {obj.get(\"Package\", \"\")} :: {obj.get(\"Test\", \"\")} ({obj.get(\"Elapsed\", 0):.3f}s)"); \
			elif obj.get("Action") == "skip" and "Test" in obj: \
				test_counts["skip"] += 1; \
				print(f"  [SKIP] {obj.get(\"Package\", \"\")} :: {obj.get(\"Test\", \"\")}");\
		except: pass; \
	print(f"\n+-------------------------------------------------------------------------------+"); \
	print(f"| SUMMARY: {test_counts[\"pass\"]} passed, {test_counts[\"fail\"]} failed, {test_counts[\"skip\"]} skipped"); \
	print(f"+-------------------------------------------------------------------------------+"); \
	if failed_tests: \
		print("\n[FAIL] FAILED TESTS:"); \
		for t in failed_tests: print(f"  - {t}"); \
	sys.exit(test_counts["fail"])' 2>/dev/null || go test -short ./...

# Internal target: Test summary
test-summary:
	@echo "================================================================================"
	@if [ -f coverage.out ]; then \
		echo "Coverage Summary:"; \
		go tool cover -func=coverage.out | tail -1; \
	fi
	@echo "================================================================================"

# Internal target: Detailed test summary
test-summary-detailed:
	@echo ""
	@echo "+===============================================================================+"
	@echo "| DETAILED TEST SUMMARY                                                        |"
	@echo "+===============================================================================+"
	@if [ -f coverage.out ]; then \
		echo ""; \
		echo "Coverage by Package:"; \
		go tool cover -func=coverage.out | grep -v "total:" | awk '{printf "  * %-60s %6s\n", $$1":"$$2, $$3}'; \
		echo ""; \
		echo "Overall Coverage:"; \
		go tool cover -func=coverage.out | grep "total:" | awk '{printf "  Total: %s\n", $$3}'; \
	fi
	@echo ""

# =============================================================================
# SPECIFIC TEST TARGETS
# =============================================================================

test-vm:
	@echo "Testing VM Client..."
	@go test -v ./internal/infrastructure/vm/... | $(MAKE) --no-print-directory format-test-output

test-obfuscation:
	@echo "Testing Obfuscation..."
	@go test -v ./internal/infrastructure/obfuscation/... | $(MAKE) --no-print-directory format-test-output

test-service:
	@echo "Testing Services..."
	@go test -v ./internal/application/services/... | $(MAKE) --no-print-directory format-test-output

test-archive:
	@echo "Testing Archive Writer..."
	@go test -v ./internal/infrastructure/archive/... | $(MAKE) --no-print-directory format-test-output

test-builder:
	@echo "Testing Build System..."
	@go test -v ./build/... | $(MAKE) --no-print-directory format-test-output

test-domain:
	@echo "Testing Domain Layer..."
	@go test -v ./internal/domain/... | $(MAKE) --no-print-directory format-test-output

# =============================================================================
# BUILD TARGETS - With automatic testing
# =============================================================================

# Build with automatic fast tests
build: test-fast
	@echo ""
	@echo "================================================================================"
	@echo "Building binary..."
	@echo "================================================================================"
	@go build -o vmgather ./cmd/vmgather
	@go build -o vmimporter ./cmd/vmimporter
	@echo "[OK] Build complete: ./vmgather"
	@ls -lh vmgather | awk '{print "Size:", $$5}'
	@echo "[OK] Build complete: ./vmimporter"
	@ls -lh vmimporter | awk '{print "Size:", $$5}'

# Build with full test suite and linting (recommended for releases)
build-safe: test-full lint
	@echo ""
	@echo "================================================================================"
	@echo "Building binary (safe mode)..."
	@echo "================================================================================"
	@go build -o vmgather ./cmd/vmgather
	@go build -o vmimporter ./cmd/vmimporter
	@echo "[OK] Build complete (all checks passed): ./vmgather"
	@echo "[OK] Build complete (all checks passed): ./vmimporter"

# Build for all platforms
build-all: test-fast
	@echo ""
	@echo "================================================================================"
	@echo "Building for all platforms..."
	@echo "================================================================================"
	@go run ./build/builder.go

# =============================================================================
# UTILITY TARGETS
# =============================================================================

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -f vmgather vmimporter coverage.out
	@rm -rf dist/
	@echo "[OK] Clean complete"

# =============================================================================
# DOCKER TARGETS
# =============================================================================

docker-build: docker-build-vmgather docker-build-vmimporter



# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@echo "[OK] Format complete"

# Lint code (matches CI environment)
lint:
	@echo "Running linter..."
	@if ! command -v golangci-lint &> /dev/null; then \
		echo "[ERROR] golangci-lint not found. Installing..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin v1.59.1; \
	fi
	@golangci-lint run --timeout=5m
	@echo "[OK] Lint complete"

# =============================================================================
# TEST ENVIRONMENT - Docker-based VM instances for E2E testing
# =============================================================================

# Start test environment with all VM scenarios
# Start test environment with all VM test instances
# Full clean-slate test environment cycle (cleans up before and after)
test-env-full:
	$(MAKE) test-env-down
	docker volume prune -f || true
	$(MAKE) test-env-up
	$(MAKE) test
	$(MAKE) test-env-down

test-env-up:
	@echo "================================================================================"
	@echo "Starting Test Environment"
	@echo "================================================================================"
	@if [ ! -d "local-test-env" ]; then \
		echo "[ERROR] Error: local-test-env directory not found!"; \
		echo "This directory is gitignored. Please ensure you have it locally."; \
		exit 1; \
	fi
	@$(DOCKER_COMPOSE) -f local-test-env/docker-compose.test.yml up -d
	@echo ""
	@echo "Waiting for services to be ready (30 seconds)..."
	@sleep 30
	@echo ""
	@echo "Running healthcheck..."
	@cd local-test-env && ./healthcheck.sh
	@echo ""
	@echo "[OK] Test environment is ready!"
	@echo ""
	@echo "Available instances:"
	@echo "  - VMSingle No Auth:     http://localhost:18428"
	@echo "  - VMSingle via VMAuth:  http://localhost:8427"
	@echo "  - VM Cluster:           http://localhost:8481"
	@echo "  - VM Cluster via VMAuth: http://localhost:8426"
	@echo ""
	@echo "Run 'make test-scenarios' to test all scenarios"
	@echo "Run 'make test-env-logs' to see logs"
	@echo "Run 'make test-env-down' to stop"

# Stop test environment
test-env-down:
	@echo "Stopping Test Environment..."
	@$(DOCKER_COMPOSE) -f local-test-env/docker-compose.test.yml down
	@echo "[OK] Test environment stopped"

# Stop and remove all data
test-env-clean:
	@echo "Cleaning Test Environment (including data)..."
	@$(DOCKER_COMPOSE) -f local-test-env/docker-compose.test.yml down -v
	@echo "[OK] Test environment cleaned"

# Show logs from test environment
test-env-logs:
	@$(DOCKER_COMPOSE) -f local-test-env/docker-compose.test.yml logs -f

# Test all scenarios against running test environment
test-scenarios:
	@echo "================================================================================"
	@echo "Testing All Scenarios"
	@echo "================================================================================"
	@if [ ! -f "local-test-env/test-all-scenarios.sh" ]; then \
		echo "[ERROR] Error: test-all-scenarios.sh not found in local-test-env/"; \
		exit 1; \
	fi
	@cd local-test-env && ./test-all-scenarios.sh

# Full E2E test: start env, test, stop
test-env: test-env-up test-scenarios
	@echo ""
	@echo "[OK] All E2E tests completed!"
	@echo "Run 'make test-env-down' to stop the environment"
