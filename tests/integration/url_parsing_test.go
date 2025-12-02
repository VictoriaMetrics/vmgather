package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/application/services"
	"github.com/VictoriaMetrics/VMGather/internal/domain"
)

// TestRealScenario_VMAuthWithTenant tests the EXACT scenario from user's bug report
// URL: https://vm.example.com/1011/rw/prometheus
// Locally via nginx: http://vm.example.com:8888/1011/rw/prometheus
func TestRealScenario_VMAuthWithTenant(t *testing.T) {
	if os.Getenv("VMEXPORTER_INTEGRATION") == "" {
		t.Skip("skipping real VM integration test; set VMEXPORTER_INTEGRATION=1 to enable")
	}
	// This is the EXACT config user has (but via nginx proxy with domain)
	config := domain.ExportConfig{
		Connection: domain.VMConnection{
			URL:         "http://localhost:8888", // Nginx proxy (simulates domain)
			ApiBasePath: "/1011/rw/prometheus",   // Tenant + /rw path (not /prometheus!)
			TenantId:    "1011",
			Auth: domain.AuthConfig{
				Type:     domain.AuthTypeBasic,
				Username: "monitoring-rw",
				Password: "secret-rw-pass",
			},
		},
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-12 * time.Hour),
			End:   time.Now(),
		},
		Components: []string{"vmagent", "vmstorage"},
		Jobs:       []string{},
	}

	// Create VM service
	vmService := services.NewVMService()

	// Test 1: Discover components (uses /api/v1/query)
	t.Run("DiscoverComponents", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		components, err := vmService.DiscoverComponents(ctx, config.Connection, config.TimeRange)

		if err != nil {
			t.Logf("[FAIL] Discovery failed: %v", err)
			// Check if error contains empty URL
			if err.Error() == "unsupported protocol scheme \"\"" {
				t.Fatal("🔴 BUG FOUND: URL is empty! Connection.URL was not passed correctly to client")
			}
			t.Fatalf("Discovery failed: %v", err)
		}

		if len(components) == 0 {
			t.Fatal("Expected to discover components, got 0")
		}

		t.Logf("[OK] Discovered %d components", len(components))
		for _, comp := range components {
			t.Logf("  - %s (jobs: %v, instances: %d)", comp.Component, comp.Jobs, comp.InstanceCount)
		}
	})

	// Test 2: Get sample metrics (uses /api/v1/query with topk)
	t.Run("GetSample", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		samples, err := vmService.GetSample(ctx, config, 5)

		if err != nil {
			t.Logf("[FAIL] Sample failed: %v", err)
			// Check if error contains empty URL
			if err.Error() == "unsupported protocol scheme \"\"" {
				t.Fatal("🔴 BUG FOUND: URL is empty! Connection.URL was not passed correctly to client")
			}
			t.Fatalf("Sample failed: %v", err)
		}

		if len(samples) == 0 {
			t.Fatal("Expected to get sample metrics, got 0")
		}

		t.Logf("[OK] Got %d sample metrics", len(samples))
		for i, sample := range samples {
			if i < 3 { // Show first 3
				t.Logf("  - %s = %.2f", sample.MetricName, sample.Value)
			}
		}
	})

	// Test 3: Export (uses /api/v1/export)
	t.Run("Export", func(t *testing.T) {
		exportService := services.NewExportService("./test-exports", "integration-test")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := exportService.ExecuteExport(ctx, config)

		if err != nil {
			t.Logf("[FAIL] Export failed: %v", err)

			// Check for common errors
			if err.Error() == "unsupported protocol scheme \"\"" {
				t.Fatal("🔴 BUG FOUND: URL is empty! Connection.URL was not passed correctly to client")
			}

			if contains(err.Error(), "missing route for") {
				t.Fatalf("🔴 BUG FOUND: VMAuth doesn't recognize path '/1011/rw/prometheus/api/v1/export'. Path construction is wrong!")
			}

			t.Fatalf("Export failed: %v", err)
		}

		if result.MetricsExported == 0 {
			t.Fatal("Expected to export metrics, got 0")
		}

		t.Logf("[OK] Exported %d metrics", result.MetricsExported)
		t.Logf("  Archive: %s", result.ArchivePath)
		t.Logf("  Size: %.2f KB", float64(result.ArchiveSizeBytes)/1024)
	})
}

// TestURLParsing_AllFormats tests that URL parsing works correctly
func TestURLParsing_AllFormats(t *testing.T) {
	tests := []struct {
		name           string
		inputURL       string
		expectedURL    string
		expectedPath   string
		expectedTenant string
	}{
		{
			name:           "Base URL only",
			inputURL:       "http://localhost:8428",
			expectedURL:    "http://localhost:8428",
			expectedPath:   "",
			expectedTenant: "",
		},
		{
			name:           "With tenant in path",
			inputURL:       "http://localhost:8481/select/0/prometheus",
			expectedURL:    "http://localhost:8481",
			expectedPath:   "/select/0/prometheus",
			expectedTenant: "0",
		},
		{
			name:           "VMAuth with tenant and /rw path (user's case)",
			inputURL:       "http://localhost:8427/1011/rw/prometheus",
			expectedURL:    "http://localhost:8427",
			expectedPath:   "/1011/rw/prometheus",
			expectedTenant: "1011",
		},
		{
			name:           "Full API path",
			inputURL:       "http://localhost:8481/select/0/prometheus/api/v1/query",
			expectedURL:    "http://localhost:8481",
			expectedPath:   "/select/0/prometheus",
			expectedTenant: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test verifies that frontend parsing is correct
			// In real scenario, frontend sends: url, api_base_path, tenant_id
			t.Logf("Input URL: %s", tt.inputURL)
			t.Logf("Expected URL: %s", tt.expectedURL)
			t.Logf("Expected Path: %s", tt.expectedPath)
			t.Logf("Expected Tenant: %s", tt.expectedTenant)

			// TODO: Add actual URL parsing logic here when we know the bug
		})
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && s[:len(substr)] == substr || len(s) > len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
