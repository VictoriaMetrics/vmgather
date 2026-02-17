package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestDefaultConfigUsesIPv4LoopbackByDefault(t *testing.T) {
	t.Setenv("VMGATHER_ENV_FILE", filepath.Join(t.TempDir(), ".env.missing"))
	t.Setenv("VM_TEST_HOST", "")

	cfg := DefaultConfig()
	if !strings.Contains(cfg.VMGatherURL, "127.0.0.1:") {
		t.Fatalf("expected VMGatherURL to use 127.0.0.1 by default, got %q", cfg.VMGatherURL)
	}
	if !strings.Contains(cfg.VMSingleNoAuth.URL, "127.0.0.1:") {
		t.Fatalf("expected VMSingleNoAuth URL to use 127.0.0.1 by default, got %q", cfg.VMSingleNoAuth.URL)
	}
	if !strings.Contains(cfg.VMSelectStandalone.SelectTenant0, "127.0.0.1:") {
		t.Fatalf("expected VMSelect standalone URL to use 127.0.0.1 by default, got %q", cfg.VMSelectStandalone.SelectTenant0)
	}
}

func TestBootstrapEnvFileUsesIPv4LoopbackByDefault(t *testing.T) {
	t.Setenv("VM_TEST_HOST", "")
	tmpFile := filepath.Join(t.TempDir(), ".env.dynamic")
	if err := bootstrapEnvFile(tmpFile); err != nil {
		t.Fatalf("bootstrapEnvFile returned error: %v", err)
	}

	values := readEnvFile(t, tmpFile)
	for _, key := range []string{"VMGATHER_URL", "VM_SINGLE_NOAUTH_URL", "VMSELECT_STANDALONE_URL"} {
		val, ok := values[key]
		if !ok {
			t.Fatalf("expected %s in generated env file", key)
		}
		if !strings.Contains(val, "127.0.0.1:") {
			t.Fatalf("expected %s to use 127.0.0.1, got %q", key, val)
		}
	}
}

func TestPickFreePortUsesFallbackRange(t *testing.T) {
	t.Parallel()

	used := map[int]bool{}
	port, err := pickFreePort("127.0.0.1", used)
	if err != nil {
		t.Fatalf("pickFreePort returned error: %v", err)
	}
	if port < fallbackPortRangeStart || port > fallbackPortRangeEnd {
		t.Fatalf("pickFreePort returned %d outside fallback range %d..%d", port, fallbackPortRangeStart, fallbackPortRangeEnd)
	}
	if !used[port] {
		t.Fatalf("pickFreePort must mark selected port as used: %d", port)
	}
}

func TestPickFreePortSkipsAlreadyUsedPorts(t *testing.T) {
	t.Parallel()

	used := map[int]bool{}
	first, err := pickFreePort("127.0.0.1", used)
	if err != nil {
		t.Fatalf("first pickFreePort returned error: %v", err)
	}

	second, err := pickFreePort("127.0.0.1", used)
	if err != nil {
		t.Fatalf("second pickFreePort returned error: %v", err)
	}
	if second == first {
		t.Fatalf("expected different port after marking first as used, got %d", second)
	}
}

func TestBootstrapEnvFileDisableDefaultPorts(t *testing.T) {
	t.Setenv("VMGATHER_PREFER_DEFAULT_PORTS", "0")
	tmpFile := filepath.Join(t.TempDir(), ".env.dynamic")
	if err := bootstrapEnvFile(tmpFile); err != nil {
		t.Fatalf("bootstrapEnvFile returned error: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed reading generated env file: %v", err)
	}
	var vmGatherPort int
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "VMGATHER_PORT=") {
			continue
		}
		vmGatherPort, err = strconv.Atoi(strings.TrimPrefix(line, "VMGATHER_PORT="))
		if err != nil {
			t.Fatalf("invalid VMGATHER_PORT value: %q", line)
		}
		break
	}
	if vmGatherPort == 0 {
		t.Fatalf("generated env file doesn't contain VMGATHER_PORT: %s", content)
	}
	if vmGatherPort == 8080 {
		t.Fatalf("expected non-default VMGATHER_PORT when defaults are disabled, got %d", vmGatherPort)
	}
	if vmGatherPort < fallbackPortRangeStart || vmGatherPort > fallbackPortRangeEnd {
		t.Fatalf("expected VMGATHER_PORT in fallback range %d..%d, got %d", fallbackPortRangeStart, fallbackPortRangeEnd, vmGatherPort)
	}
}

func TestShouldIgnoreListenErrorForIPv6Unavailable(t *testing.T) {
	t.Parallel()

	err := &net.OpError{Err: syscall.EAFNOSUPPORT}
	if !shouldIgnoreListenError("::1", err) {
		t.Fatalf("expected shouldIgnoreListenError to ignore IPv6 family unsupported error")
	}
	if shouldIgnoreListenError("127.0.0.1", err) {
		t.Fatalf("must not ignore IPv6-specific error for IPv4 host")
	}
}

func TestShouldIgnoreListenErrorDoesNotHideRealConflict(t *testing.T) {
	t.Parallel()

	err := &net.OpError{Err: syscall.EADDRINUSE}
	if shouldIgnoreListenError("::1", err) {
		t.Fatalf("must not ignore address already in use for IPv6 host")
	}
}

func TestResolveEnvFilePathWith_UsesExplicitOverride(t *testing.T) {
	path := resolveEnvFilePathWith("/tmp/custom.env", "/tmp/repo", "/tmp/repo/local-test-env/testconfig")
	if path != "/tmp/custom.env" {
		t.Fatalf("expected explicit env file path, got %q", path)
	}
}

func TestResolveEnvFilePathWith_UsesExecutableDirEnvWhenRunFromRepoRoot(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "local-test-env", "testconfig")
	exeDir := filepath.Dir(exe)
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	envPath := filepath.Join(exeDir, defaultEnvFileName)
	if err := os.WriteFile(envPath, []byte("VMGATHER_PORT=27024\n"), 0o644); err != nil {
		t.Fatalf("write env file failed: %v", err)
	}

	path := resolveEnvFilePathWith("", root, exe)
	if path != envPath {
		t.Fatalf("expected env file from executable dir %q, got %q", envPath, path)
	}
}

func TestResolveEnvFilePathWith_FallsBackToRepoLocalTestEnv(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "bin", "testconfig")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	envPath := filepath.Join(root, "local-test-env", defaultEnvFileName)
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatalf("mkdir local-test-env failed: %v", err)
	}
	if err := os.WriteFile(envPath, []byte("VM_SINGLE_NOAUTH_PORT=18428\n"), 0o644); err != nil {
		t.Fatalf("write env file failed: %v", err)
	}

	path := resolveEnvFilePathWith("", root, exe)
	if path != envPath {
		t.Fatalf("expected env file from repo local-test-env %q, got %q", envPath, path)
	}
}

func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reading env file %s: %v", path, err)
	}

	values := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[parts[0]] = parts[1]
	}
	return values
}
