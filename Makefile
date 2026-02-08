.PHONY: test test-fast test-llm build build-safe build-all clean fmt lint help pre-push test-env test-env-up test-env-down test-env-clean test-env-logs test-e2e test-all test-all-clean test-scenarios test-config-bootstrap docker-build docker-build-vmgather docker-build-vmimporter manual-env-up manual-env-down manual-env-clean manual-env-logs manual-vmgather-up manual-vmgather-down manual-vmgather-daemon-up manual-vmgather-daemon-down manual-vmgather-daemon-status manual-vmgather-daemon-logs

VERSION ?= $(shell git describe --tags --always --dirty)
PKG_TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "latest")
# Platforms supported by VictoriaMetrics (consistent with upstream)
# Platforms supported by VictoriaMetrics (limited by distroless support)
PLATFORMS ?= linux/amd64,linux/arm64,linux/arm

# Go version for build
GO_VERSION ?= 1.22
DOCKER_OUTPUT ?= type=docker
DOCKER_COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")
TEST_ENV_FILE := local-test-env/.env.dynamic
MANUAL_ENV_FILE := local-test-env/.env.manual
TEST_ENV_PROJECT_FILE_TEST := .compose-project.test
TEST_ENV_PROJECT_FILE_MANUAL := .compose-project.manual

# Docker registries and namespace (standard across VictoriaMetrics)
# GHCR is handled by CI; local `make release` targets public hubs only.
DOCKER_REGISTRIES ?= docker.io quay.io
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
	@echo "  make build-safe   - Build with race detector tests + linting (no Docker)"
	@echo "  make build-all    - Build for all platforms (8 targets)"
	@echo "  make publish-via-docker - Build & Push multi-arch images to registries"
	@echo "  make release      - Alias for publish-via-docker"
	@echo "  make clean        - Clean build artifacts"
	@echo ""
	@echo "TEST COMMANDS:"
	@echo "  make test         - Run all tests (fast mode, no race detector)"
	@echo "  make test-fast    - Run tests with -short flag (skip slow tests)"
	@echo "  make test-unit-full - Run unit tests without -short"
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
	@echo "TESTS WITHOUT DOCKER:"
	@echo "  make test              - Unit tests only (fast, no Docker required)"
	@echo "  make test-fast         - Even faster (skips slow tests)"
	@echo "  make test-coverage     - Generate coverage report"
	@echo ""
	@echo "TESTS WITH DOCKER:"
	@echo "  make test-integration  - Binary tests Docker environment (needs Docker)"
	@echo "  make test-env-up       - Start test environment"
	@echo "  make test-env-down     - Stop test environment"
	@echo "  make test-env-clean    - Stop test environment and remove volumes"
	@echo "  make test-e2e          - Run Playwright suite (requires test-env-up)"
	@echo "  make test-all          - Everything: test-full + test-e2e"
	@echo "  make test-all-clean    - Everything + cleanup (recommended for CI / OrbStack)"
	@echo ""
	@echo "FULL TEST SUITE:"
	@echo "  make test-full         - Everything: unit + Docker scenarios"
	@echo ""
	@echo "TEST CONFIGURATION:"
	@echo "  make test-config-bootstrap - Generate dynamic env file"
	@echo "  make test-config-validate - Validate test configuration"
	@echo "  make test-config-env      - Show config as environment variables"
	@echo "  make test-config-json     - Show config as JSON"
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
	@set -e; \
		tmpfile="$$(mktemp)"; \
		status=0; \
		go test -short -coverprofile=coverage.out ./... >"$$tmpfile" 2>&1 || status=$$?; \
		cat "$$tmpfile" | $(MAKE) --no-print-directory format-test-output; \
		rm -f "$$tmpfile"; \
		exit $$status
	@echo ""
	@$(MAKE) --no-print-directory test-summary

# Fast tests without coverage
test-fast:
	@echo "================================================================================"
	@echo "TEST SUITE: Ultra-Fast Mode (no coverage, skip slow tests)"
	@echo "================================================================================"
	@echo ""
	@set -e; \
		tmpfile="$$(mktemp)"; \
		status=0; \
		go test -short ./... >"$$tmpfile" 2>&1 || status=$$?; \
		cat "$$tmpfile" | $(MAKE) --no-print-directory format-test-output; \
		rm -f "$$tmpfile"; \
		exit $$status
	@echo ""
	@$(MAKE) --no-print-directory test-summary

# Full unit test suite (no -short)
test-unit-full:
	@echo "================================================================================"
	@echo "TEST SUITE: Unit Full Mode (no -short)"
	@echo "================================================================================"
	@echo ""
	@set -e; \
		tmpfile="$$(mktemp)"; \
		status=0; \
		go test -coverprofile=coverage.out ./... >"$$tmpfile" 2>&1 || status=$$?; \
		cat "$$tmpfile" | $(MAKE) --no-print-directory format-test-output; \
		rm -f "$$tmpfile"; \
		exit $$status
	@echo ""
	@$(MAKE) --no-print-directory test-summary

# Full test suite with race detector (unit tests only)
test-race:
	@echo "================================================================================"
	@echo "TEST SUITE: Race detector mode"
	@echo "================================================================================"
	@echo ""
	@set -e; \
		tmpfile="$$(mktemp)"; \
		status=0; \
		go test -v -race -coverprofile=coverage.out ./... >"$$tmpfile" 2>&1 || status=$$?; \
		cat "$$tmpfile" | $(MAKE) --no-print-directory format-test-output; \
		rm -f "$$tmpfile"; \
		exit $$status
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

# Build with race detector tests and linting (no Docker required)
build-safe: test-race lint
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

# Build test config utility
LOCAL_TEST_ENV_GO := $(wildcard local-test-env/*.go)

local-test-env/testconfig: $(LOCAL_TEST_ENV_GO)
	@cd local-test-env && go build -o testconfig .

# Load and validate test configuration
test-config-validate: local-test-env/testconfig
	@echo "Validating test configuration..."
	@cd local-test-env && ./testconfig validate

# Export test configuration as environment variables
test-config-env: local-test-env/testconfig
	@cd local-test-env && ./testconfig env

# Generate dynamic port environment file
test-config-bootstrap: local-test-env/testconfig
	@cd local-test-env && ./testconfig bootstrap

# Show test configuration as JSON
test-config-json: local-test-env/testconfig
	@cd local-test-env && ./testconfig json

# Full clean-slate test environment cycle (cleans up before and after)
test-env-full:
	$(MAKE) test-env-clean
	$(MAKE) test-env-up
	$(MAKE) test
	$(MAKE) test-env-clean

# Local pre-push safety check (kept in Makefile to avoid shell scripts)
pre-push: test-env-full

test-env-up: local-test-env/testconfig
	@echo "================================================================================"
	@echo "Starting Test Environment"
	@echo "================================================================================"
	@if [ ! -d "local-test-env" ]; then \
		echo "[ERROR] Error: local-test-env directory not found!"; \
		echo "This directory is gitignored. Please ensure you have it locally."; \
		exit 1; \
	fi
	@cd local-test-env && ./testconfig bootstrap
	@cd local-test-env && ./testconfig validate
	@echo "Starting Docker test environment (isolated compose project)..."
	@set -e; \
		prev_proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_TEST) ./testconfig project 2>/dev/null || true)"; \
		if [ -n "$$prev_proj" ]; then \
			echo "  [INFO] stopping existing compose project: $$prev_proj"; \
			$(DOCKER_COMPOSE) -p "$$prev_proj" --env-file $(TEST_ENV_FILE) -f local-test-env/docker-compose.test.yml down >/dev/null 2>&1 || true; \
		fi; \
		started=0; \
		attempt=0; \
		max_attempts=3; \
		while [ "$$attempt" -lt "$$max_attempts" ]; do \
			attempt=$$((attempt+1)); \
			proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_TEST) VMGATHER_PROJECT_PREFIX=vmtest ./testconfig project reset)"; \
			echo "  [INFO] compose project: $$proj (attempt $$attempt/$$max_attempts)"; \
			tmpfile="$$(mktemp)"; \
			status=0; \
			$(DOCKER_COMPOSE) -p "$$proj" --env-file $(TEST_ENV_FILE) -f local-test-env/docker-compose.test.yml up -d >"$$tmpfile" 2>&1 || status=$$?; \
			cat "$$tmpfile"; \
			if [ "$$status" -eq 0 ]; then \
				rm -f "$$tmpfile"; \
				started=1; \
				break; \
			fi; \
			if grep -qi "no space left on device" "$$tmpfile"; then \
				echo ""; \
				echo "[WARN] Docker reported 'no space left on device'. Running cleanup and retrying..."; \
				docker builder prune -af || true; \
				docker system prune -af || true; \
				rm -f "$$tmpfile"; \
				continue; \
			fi; \
			if grep -qi "already exists in network" "$$tmpfile"; then \
				echo ""; \
				echo "[WARN] Docker network has stale endpoints. Retrying with a new compose project..."; \
				rm -f "$$tmpfile"; \
				continue; \
			fi; \
			rm -f "$$tmpfile"; \
			exit "$$status"; \
		done; \
		if [ "$$started" -ne 1 ]; then \
			echo "[ERROR] Failed to start the Docker test environment after $$max_attempts attempts"; \
			exit 1; \
		fi
	@echo ""
	@echo "Waiting for services to be ready (30 seconds)..."
	@sleep 30
	@echo ""
	@echo "Running healthcheck..."
	@cd local-test-env && ./testconfig healthcheck
	@echo ""
	@echo "[OK] Test environment is ready!"
	@echo ""
	@cd local-test-env && ./testconfig json | jq -r '"Available instances:\n  - VMGather:             \(.vmgather_url)\n  - VMSingle No Auth:     \(.vm_single_noauth.url)\n  - VMSingle via VMAuth:  \(.vm_single_auth.url)\n  - VM Cluster:           \(.vm_cluster.base_url)\n  - VMSelect standalone:  \(.vmselect_standalone.base_url)\n  - VM Cluster via VMAuth: \(.vmauth_cluster.url)"'
	@echo ""
	@echo "Run 'make test-scenarios' to test all scenarios"
	@echo "Run 'make test-env-logs' to see logs"
	@echo "Run 'make test-env-down' to stop"

# Stop test environment
test-env-down:
	@echo "Stopping Test Environment..."
	@set -e; \
		proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_TEST) ./testconfig project 2>/dev/null || true)"; \
		if [ -z "$$proj" ]; then \
			echo "[WARN] No stored compose project name; nothing to stop"; \
			exit 0; \
		fi; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(TEST_ENV_FILE) -f local-test-env/docker-compose.test.yml down
	@echo "[OK] Test environment stopped"

# Stop and remove all data
test-env-clean:
	@echo "Cleaning Test Environment (including data)..."
	@set -e; \
		proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_TEST) ./testconfig project 2>/dev/null || true)"; \
		if [ -z "$$proj" ]; then \
			echo "[WARN] No stored compose project name; nothing to clean"; \
			exit 0; \
		fi; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(TEST_ENV_FILE) -f local-test-env/docker-compose.test.yml down -v; \
		rm -f "local-test-env/$(TEST_ENV_PROJECT_FILE_TEST)" >/dev/null 2>&1 || true
	@echo "[OK] Test environment cleaned"

# Show logs from test environment
test-env-logs:
	@set -e; \
		proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_TEST) ./testconfig project 2>/dev/null || true)"; \
		if [ -z "$$proj" ]; then \
			echo "[ERROR] No stored compose project name; run 'make test-env-up' first"; \
			exit 1; \
		fi; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(TEST_ENV_FILE) -f local-test-env/docker-compose.test.yml logs -f

# Manual (local) environment for UI testing.
manual-env-up: local-test-env/testconfig
	@echo "================================================================================"
	@echo "Starting Manual Test Environment"
	@echo "================================================================================"
	@cd local-test-env && VMGATHER_ENV_FILE=.env.manual ./testconfig bootstrap
	@cd local-test-env && VMGATHER_ENV_FILE=.env.manual ./testconfig validate
	@set -e; \
		prev_proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_MANUAL) ./testconfig project 2>/dev/null || true)"; \
		if [ -n "$$prev_proj" ]; then \
			echo "  [INFO] stopping existing compose project: $$prev_proj"; \
			$(DOCKER_COMPOSE) -p "$$prev_proj" --env-file $(MANUAL_ENV_FILE) -f local-test-env/docker-compose.test.yml down >/dev/null 2>&1 || true; \
		fi; \
		proj="$$(cd local-test-env && VMGATHER_ENV_FILE=.env.manual VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_MANUAL) VMGATHER_PROJECT_PREFIX=vmmanual ./testconfig project reset)"; \
		echo "  [INFO] compose project: $$proj"; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(MANUAL_ENV_FILE) -f local-test-env/docker-compose.test.yml up -d; \
		echo ""; \
		echo "Waiting for services to be ready (30 seconds)..."; \
		sleep 30; \
		echo ""; \
		echo "Running healthcheck..."; \
		(cd local-test-env && VMGATHER_ENV_FILE=.env.manual ./testconfig healthcheck); \
		echo ""; \
		echo "[OK] Manual test environment is ready!"; \
		echo ""; \
		(cd local-test-env && VMGATHER_ENV_FILE=.env.manual ./testconfig json) | jq -r '"Available instances:\n  - VMSingle No Auth:     \(.vm_single_noauth.url)\n  - VMSingle via VMAuth:  \(.vm_single_auth.url)\n  - VM Cluster:           \(.vm_cluster.base_url)\n  - VMSelect standalone:  \(.vmselect_standalone.base_url)\n  - VM Cluster via VMAuth: \(.vmauth_cluster.url)"'
	@echo ""
	@echo "Run 'make manual-env-logs' to see logs"
	@echo "Run 'make manual-env-down' to stop"

manual-env-down:
	@echo "Stopping Manual Test Environment..."
	@set -e; \
		proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_MANUAL) ./testconfig project 2>/dev/null || true)"; \
		if [ -z "$$proj" ]; then \
			echo "[WARN] No stored compose project name; nothing to stop"; \
			exit 0; \
		fi; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(MANUAL_ENV_FILE) -f local-test-env/docker-compose.test.yml down
	@echo "[OK] Manual test environment stopped"

manual-env-clean:
	@echo "Cleaning Manual Test Environment (including data)..."
	@set -e; \
		proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_MANUAL) ./testconfig project 2>/dev/null || true)"; \
		if [ -z "$$proj" ]; then \
			echo "[WARN] No stored compose project name; nothing to clean"; \
			exit 0; \
		fi; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(MANUAL_ENV_FILE) -f local-test-env/docker-compose.test.yml down -v; \
		rm -f "local-test-env/$(TEST_ENV_PROJECT_FILE_MANUAL)" >/dev/null 2>&1 || true; \
		rm -f "$(MANUAL_ENV_FILE)" >/dev/null 2>&1 || true
	@echo "[OK] Manual test environment cleaned"

manual-env-logs:
	@set -e; \
		proj="$$(cd local-test-env && VMGATHER_PROJECT_FILE=$(TEST_ENV_PROJECT_FILE_MANUAL) ./testconfig project 2>/dev/null || true)"; \
		if [ -z "$$proj" ]; then \
			echo "[ERROR] No stored compose project name; run 'make manual-env-up' first"; \
			exit 1; \
		fi; \
		$(DOCKER_COMPOSE) -p "$$proj" --env-file $(MANUAL_ENV_FILE) -f local-test-env/docker-compose.test.yml logs -f

manual-vmgather-up:
	@echo "Building vmgather..."
	@go build -o vmgather ./cmd/vmgather
	@echo "Starting vmgather UI on http://localhost:8080 ..."
	@./vmgather -addr localhost:8080 -no-browser

manual-vmgather-down:
	@echo "Stopping vmgather UI (port 8080)..."
	@pid="$$(lsof -tiTCP:8080 -sTCP:LISTEN 2>/dev/null || true)"; \
		if [ -z "$$pid" ]; then \
			echo "[WARN] No listener found on :8080"; \
			exit 0; \
		fi; \
		cmd="$$(ps -p "$$pid" -o command= 2>/dev/null || true)"; \
		if echo "$$cmd" | grep -q "[/]vmgather"; then \
			kill "$$pid"; \
		else \
			echo "[ERROR] Refusing to kill pid=$$pid (not vmgather): $$cmd"; \
			exit 1; \
		fi

# Persistent vmgather UI (macOS launchd; survives terminal closes and auto-restarts on crash)
manual-vmgather-daemon-up: local-test-env/testconfig
	@cd local-test-env && ./testconfig ui-daemon install

manual-vmgather-daemon-down: local-test-env/testconfig
	@cd local-test-env && ./testconfig ui-daemon uninstall

manual-vmgather-daemon-status: local-test-env/testconfig
	@cd local-test-env && ./testconfig ui-daemon status

manual-vmgather-daemon-logs: local-test-env/testconfig
	@cd local-test-env && ./testconfig ui-daemon logs

# Integration tests: binary tests Docker environment
test-integration: local-test-env/testconfig
	@echo "================================================================================"
	@echo "Integration Tests: Binary testing Docker environment"
	@echo "================================================================================"
	@cd local-test-env && ./testconfig healthcheck
	@set -e; \
		eval "$$(cd local-test-env && ./testconfig env)"; \
		cd local-test-env && ./testconfig scenarios; \
		cd ..; \
		INTEGRATION_TEST=1 LIVE_VM_URL="$$VM_SINGLE_NOAUTH_URL" go test -tags "integration realdiscovery" ./tests/integration/...

# Alias for backward compatibility
test-scenarios: test-integration

# Full E2E test: start env, test, stop
test-env: test-env-up test-integration
	@echo ""
	@echo "[OK] All E2E tests completed!"
	@echo "Run 'make test-env-down' to stop the environment"

# Playwright tests: require the local docker environment.
test-e2e:
	@echo "================================================================================"
	@echo "Playwright E2E Suite"
	@echo "================================================================================"
	@cd local-test-env && ./testconfig healthcheck
	@echo ""
	@echo "[1/3] Building vmgather..."
	@go build -o vmgather ./cmd/vmgather
	@echo ""
	@echo "[2/3] Ensuring E2E dependencies exist..."
	@if [ ! -d "tests/e2e/node_modules" ]; then \
		echo "  Installing node deps via npm ci..."; \
		cd tests/e2e && npm ci; \
	fi
	@echo ""
	@echo "[3/3] Running Playwright (workers=1)..."
	@set -e; \
		eval "$$(cd local-test-env && ./testconfig env)"; \
		E2E_REAL="$${E2E_REAL:-1}"; \
		LIVE_VM_URL="$${LIVE_VM_URL:-$$VM_SINGLE_NOAUTH_URL}"; \
		export E2E_REAL LIVE_VM_URL; \
		if [ -n "$$VMGATHER_PORT" ] && command -v lsof >/dev/null 2>&1; then \
			pid="$$(lsof -tiTCP:$$VMGATHER_PORT -sTCP:LISTEN || true)"; \
			if [ -n "$$pid" ]; then \
				cmd="$$(ps -p "$$pid" -o command= || true)"; \
				if echo "$$cmd" | grep -q "[/]vmgather" && echo "$$cmd" | grep -q -- "-no-browser"; then \
					echo "  [WARN] Killing stale vmgather web server (pid=$$pid, port=$$VMGATHER_PORT)"; \
					kill "$$pid" || true; \
					sleep 1; \
				fi; \
			fi; \
		fi; \
		cd tests/e2e && npx playwright test --workers=1
	@echo ""
	@echo "================================================================================"
	@echo "[OK] Playwright E2E suite passed!"
	@echo "================================================================================"

# Everything: unit + docker scenarios + Playwright.
test-all: test-full test-e2e

# Everything + cleanup (CI / disk-constrained environments).
test-all-clean:
	@set -e; \
		status=0; \
		$(MAKE) --no-print-directory test-all || status=$$?; \
		echo ""; \
		echo "================================================================================"; \
		echo "Cleaning Docker test environment (including volumes)..."; \
		echo "================================================================================"; \
		$(MAKE) --no-print-directory test-env-clean || true; \
		exit $$status

# Local gate before pushing changes.
pre-push: test-all-clean

# =============================================================================
# FULL TEST SUITE
# =============================================================================

# Complete test cycle: unit tests + integration scenarios
test-full:
	@echo "================================================================================"
	@echo "Full Test Suite"
	@echo "================================================================================"
	@echo ""
	@echo "[1/3] Unit tests (without Docker)..."
	@$(MAKE) test-unit-full
	@echo ""
	@echo "[2/3] Starting Docker test environment..."
	@$(MAKE) test-env-down 2>/dev/null || true
	@$(MAKE) test-env-up
	@echo ""
	@echo "[3/3] Integration tests (binary + Docker)..."
	@$(MAKE) test-integration
	@echo ""
	@echo "================================================================================"
	@echo "[OK] All tests passed!"
	@echo "================================================================================"
	@echo ""
	@echo "Note: Docker environment still running. Stop with 'make test-env-down'"
