//go:build integration && realdiscovery

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// This test hits a real VictoriaMetrics endpoint (default: http://localhost:18428).
// Enable with: go test -tags "integration realdiscovery" ./tests/integration -run TestLiveDiscovery
// Fails fast if vm_app_version data is missing.
func TestLiveDiscovery(t *testing.T) {
	url := os.Getenv("LIVE_VM_URL")
	if url == "" {
		url = "http://localhost:18428"
	}
	vmSvc := services.NewVMService()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tr := domain.TimeRange{
		Start: time.Now().Add(-15 * time.Minute),
		End:   time.Now(),
	}
	conn := domain.VMConnection{
		URL:         url,
		ApiBasePath: os.Getenv("LIVE_VM_API_BASE_PATH"),
		Auth:        domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	comps, err := vmSvc.DiscoverComponents(ctx, conn, tr)
	if err != nil {
		t.Fatalf("discovery failed against %s: %v", url, err)
	}
	if len(comps) == 0 {
		t.Fatalf("no components discovered at %s (vm_app_version missing?)", url)
	}
}
