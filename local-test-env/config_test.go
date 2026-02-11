package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
