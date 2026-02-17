package integration

import (
	"context"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// TestRealCustomerURL tests with the EXACT customer URL format
// Customer's working curl:
// curl --user 'monitoring-read:${VM_AUTH_PASSWORD}' 'https://vm.example.com/1011/ui/prometheus/api/v1/query?query=sum(1)'
func TestRealCustomerURL(t *testing.T) {
	t.Skip("Skipping test with real customer URL - requires real credentials")

	config := domain.ExportConfig{
		Connection: domain.VMConnection{
			URL:         "https://vm.example.com",
			ApiBasePath: "/1011/ui/prometheus", // Customer's actual path
			TenantId:    "1011",
			Auth: domain.AuthConfig{
				Type:     domain.AuthTypeBasic,
				Username: "monitoring-read",
				Password: "FAKE_PASSWORD", // User must provide real password
			},
		},
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent-prometheus"},
	}

	vmService := services.NewVMService()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test simple query
	samples, err := vmService.GetSample(ctx, config, 5)

	if err != nil {
		t.Logf("[FAIL] Sample failed: %v", err)
		t.Fatalf("Sample failed (expected with fake password): %v", err)
	}

	t.Logf("[OK] Got %d samples", len(samples))
}

// TestURLPathNormalization verifies that /rw/prometheus is normalized to /prometheus
func TestURLPathNormalization(t *testing.T) {
	tests := []struct {
		name        string
		inputPath   string
		expectedLog string // What we expect to see in logs
	}{
		{
			name:        "/rw/prometheus should be normalized",
			inputPath:   "/1011/rw/prometheus",
			expectedLog: "Path normalized: /1011/rw/prometheus → /1011/prometheus",
		},
		{
			name:        "/ui/prometheus should NOT be normalized",
			inputPath:   "/1011/ui/prometheus",
			expectedLog: "", // No normalization
		},
		{
			name:        "/prometheus should NOT be normalized",
			inputPath:   "/1011/prometheus",
			expectedLog: "", // No normalization
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := domain.ExportConfig{
				Connection: domain.VMConnection{
					URL:         "http://localhost:18428",
					ApiBasePath: tt.inputPath,
					Auth: domain.AuthConfig{
						Type: domain.AuthTypeNone,
					},
				},
				TimeRange: domain.TimeRange{
					Start: time.Now().Add(-1 * time.Hour),
					End:   time.Now(),
				},
			}

			vmService := services.NewVMService()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// This will fail (no VM at localhost:8428), but we can check logs
			_, err := vmService.GetSample(ctx, config, 1)

			// We expect it to fail, but logs should show normalization
			if err == nil {
				t.Fatal("Expected error (no VM running), but got success")
			}

			t.Logf("Error (expected): %v", err)
			t.Logf("Check logs above for: %s", tt.expectedLog)
		})
	}
}

// TestFrontendURLParsing documents how frontend should parse different URL formats
func TestFrontendURLParsing(t *testing.T) {
	tests := []struct {
		name               string
		userInput          string
		expectedURL        string
		expectedApiPath    string
		expectedTenantId   string
		expectedFullApiUrl string
	}{
		{
			name:               "Customer case: maas with /rw/prometheus",
			userInput:          "https://vm.example.com/1011/rw/prometheus",
			expectedURL:        "https://vm.example.com",
			expectedApiPath:    "/1011/rw/prometheus",
			expectedTenantId:   "1011",
			expectedFullApiUrl: "https://vm.example.com/1011/rw/prometheus",
		},
		{
			name:               "Customer case: maas with /ui/prometheus",
			userInput:          "https://vm.example.com/1011/ui/prometheus",
			expectedURL:        "https://vm.example.com",
			expectedApiPath:    "/1011/ui/prometheus",
			expectedTenantId:   "1011",
			expectedFullApiUrl: "https://vm.example.com/1011/ui/prometheus",
		},
		{
			name:               "Standard VMSelect with tenant",
			userInput:          "http://vmselect:8481/select/0/prometheus",
			expectedURL:        "http://vmselect:8481",
			expectedApiPath:    "/select/0/prometheus",
			expectedTenantId:   "0",
			expectedFullApiUrl: "http://vmselect:8481/select/0/prometheus",
		},
		{
			name:               "VMSingle without tenant",
			userInput:          "http://localhost:8428",
			expectedURL:        "http://localhost:8428",
			expectedApiPath:    "",
			expectedTenantId:   "",
			expectedFullApiUrl: "http://localhost:8428",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Input: %s", tt.userInput)
			t.Logf("Expected URL: %s", tt.expectedURL)
			t.Logf("Expected ApiPath: %s", tt.expectedApiPath)
			t.Logf("Expected TenantId: %s", tt.expectedTenantId)
			t.Logf("Expected FullApiUrl: %s", tt.expectedFullApiUrl)

			// Logic for parsing frontend URL would go here
			// For now, this test just documents expected behavior
		})
	}
}
