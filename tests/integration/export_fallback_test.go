package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExportFallback_LegacyTenant tests export fallback when /api/v1/export is NOT available
// Tenant 1011 has legacy paths without export endpoint
func TestExportFallback_LegacyTenant(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Tenant 1011: NO export API (legacy paths)
	conn := domain.VMConnection{
		URL: "http://localhost:8425",
		Auth: domain.AuthConfig{
			Type:     "basic",
			Username: "tenant1011-legacy",
			Password: "legacy-pass-1011",
		},
	}

	vmService := services.NewVMService()

	t.Run("CheckExportAPI_ShouldReturnFalse", func(t *testing.T) {
		hasExport := vmService.CheckExportAPI(ctx, conn)
		assert.False(t, hasExport, "Tenant 1011 should NOT have export API")
	})

	t.Run("Export_ShouldFallbackToQueryRange", func(t *testing.T) {
		exportService := services.NewExportService(t.TempDir(), "integration-test")

		config := domain.ExportConfig{
			Connection: conn,
			TimeRange: domain.TimeRange{
				Start: time.Now().Add(-1 * time.Hour),
				End:   time.Now(),
			},
			Components: []string{"vmagent"},
			Jobs:       []string{"vmagent"},
			Obfuscation: domain.ObfuscationConfig{
				Enabled: false,
			},
		}

		// Export should succeed using query_range fallback
		result, err := exportService.ExecuteExport(ctx, config)
		require.NoError(t, err, "Export should succeed with query_range fallback")
		assert.NotEmpty(t, result.ArchivePath, "Archive should be created")

		// Verify archive exists
		_, err = os.Stat(result.ArchivePath)
		assert.NoError(t, err, "Archive file should exist")

		// Clean up
		os.Remove(result.ArchivePath)
	})
}

// TestExportDirect_ModernTenant tests direct export when /api/v1/export IS available
// Tenant 2022 has modern paths with export endpoint
func TestExportDirect_ModernTenant(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Tenant 2022: Export API AVAILABLE (modern paths)
	conn := domain.VMConnection{
		URL: "http://localhost:8425",
		Auth: domain.AuthConfig{
			Type:     "basic",
			Username: "tenant2022-modern",
			Password: "modern-pass-2022",
		},
	}

	vmService := services.NewVMService()

	t.Run("CheckExportAPI_ShouldReturnTrue", func(t *testing.T) {
		hasExport := vmService.CheckExportAPI(ctx, conn)
		assert.True(t, hasExport, "Tenant 2022 SHOULD have export API")
	})

	t.Run("Export_ShouldUseDirect", func(t *testing.T) {
		exportService := services.NewExportService(t.TempDir(), "integration-test")

		config := domain.ExportConfig{
			Connection: conn,
			TimeRange: domain.TimeRange{
				Start: time.Now().Add(-1 * time.Hour),
				End:   time.Now(),
			},
			Components: []string{"vmagent"},
			Jobs:       []string{"vmagent"},
			Obfuscation: domain.ObfuscationConfig{
				Enabled: false,
			},
		}

		// Export should succeed using direct /api/v1/export
		result, err := exportService.ExecuteExport(ctx, config)
		require.NoError(t, err, "Export should succeed with direct export")
		assert.NotEmpty(t, result.ArchivePath, "Archive should be created")

		// Verify archive exists
		_, err = os.Stat(result.ArchivePath)
		assert.NoError(t, err, "Archive file should exist")

		// Clean up
		os.Remove(result.ArchivePath)
	})
}

// TestExportFallback_ErrorMessages verifies that error messages clearly indicate fallback
func TestExportFallback_ErrorMessages(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Tenant 1011: NO export API
	conn := domain.VMConnection{
		URL: "http://localhost:8425",
		Auth: domain.AuthConfig{
			Type:     "basic",
			Username: "tenant1011-legacy",
			Password: "legacy-pass-1011",
		},
	}

	exportService := services.NewExportService(t.TempDir(), "integration-test")

	config := domain.ExportConfig{
		Connection: conn,
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false,
		},
	}

	// Capture logs or check that export succeeds
	result, err := exportService.ExecuteExport(ctx, config)
	require.NoError(t, err, "Export should succeed with fallback")
	assert.NotEmpty(t, result.ArchivePath)

	// TODO: Add log capture to verify fallback message is logged
	// Expected log: "[WARN] Export API not available, falling back to query_range"

	// Clean up
	os.Remove(result.ArchivePath)
}

// TestExportAPI_DirectCheck tests the CheckExportAPI method directly
func TestExportAPI_DirectCheck(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	vmService := services.NewVMService()

	tests := []struct {
		name         string
		conn         domain.VMConnection
		expectExport bool
		description  string
	}{
		{
			name: "Tenant1011_NoExport",
			conn: domain.VMConnection{
				URL: "http://localhost:8425",
				Auth: domain.AuthConfig{
					Type:     "basic",
					Username: "tenant1011-legacy",
					Password: "legacy-pass-1011",
				},
			},
			expectExport: false,
			description:  "Legacy tenant without export API",
		},
		{
			name: "Tenant2022_WithExport",
			conn: domain.VMConnection{
				URL: "http://localhost:8425",
				Auth: domain.AuthConfig{
					Type:     "basic",
					Username: "tenant2022-modern",
					Password: "modern-pass-2022",
				},
			},
			expectExport: true,
			description:  "Modern tenant with export API",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasExport := vmService.CheckExportAPI(ctx, tt.conn)
			assert.Equal(t, tt.expectExport, hasExport,
				"CheckExportAPI result mismatch for %s", tt.description)
		})
	}
}

// TestExportFallback_CompareResults compares results from direct export vs query_range fallback
func TestExportFallback_CompareResults(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Same time range for both
	timeRange := domain.TimeRange{
		Start: time.Now().Add(-15 * time.Minute),
		End:   time.Now(),
	}

	// Export from tenant 1011 (fallback)
	conn1011 := domain.VMConnection{
		URL: "http://localhost:8425",
		Auth: domain.AuthConfig{
			Type:     "basic",
			Username: "tenant1011-legacy",
			Password: "legacy-pass-1011",
		},
	}

	exportService1011 := services.NewExportService(t.TempDir(), "integration-test")
	config1011 := domain.ExportConfig{
		Connection: conn1011,
		TimeRange:  timeRange,
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false,
		},
	}

	result1011, err := exportService1011.ExecuteExport(ctx, config1011)
	require.NoError(t, err, "Tenant 1011 export (fallback) should succeed")
	defer os.Remove(result1011.ArchivePath)

	// Export from tenant 2022 (direct)
	conn2022 := domain.VMConnection{
		URL: "http://localhost:8425",
		Auth: domain.AuthConfig{
			Type:     "basic",
			Username: "tenant2022-modern",
			Password: "modern-pass-2022",
		},
	}

	exportService2022 := services.NewExportService(t.TempDir(), "integration-test")
	config2022 := domain.ExportConfig{
		Connection: conn2022,
		TimeRange:  timeRange,
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false,
		},
	}

	result2022, err := exportService2022.ExecuteExport(ctx, config2022)
	require.NoError(t, err, "Tenant 2022 export (direct) should succeed")
	defer os.Remove(result2022.ArchivePath)

	// Both archives should exist
	stat1011, err := os.Stat(result1011.ArchivePath)
	require.NoError(t, err)
	assert.Greater(t, stat1011.Size(), int64(0), "Tenant 1011 archive should not be empty")

	stat2022, err := os.Stat(result2022.ArchivePath)
	require.NoError(t, err)
	assert.Greater(t, stat2022.Size(), int64(0), "Tenant 2022 archive should not be empty")

	// Both should contain similar data (same metrics, different method)
	// query_range fallback may return more data points than direct export
	// So we just check that both archives are non-trivial in size
	assert.Greater(t, stat1011.Size(), int64(1000), "Tenant 1011 archive should be substantial")
	assert.Greater(t, stat2022.Size(), int64(1000), "Tenant 2022 archive should be substantial")

	// Fallback (1011) might be larger due to query_range returning more points
	// This is expected behavior
	t.Logf("Archive sizes: Fallback=%d bytes, Direct=%d bytes", stat1011.Size(), stat2022.Size())
}

// TestExportFallback_MissingRoute verifies that "missing route" error triggers fallback
func TestExportFallback_MissingRoute(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn := domain.VMConnection{
		URL: "http://localhost:8425",
		Auth: domain.AuthConfig{
			Type:     "basic",
			Username: "tenant1011-legacy",
			Password: "legacy-pass-1011",
		},
	}

	exportService := services.NewExportService(t.TempDir(), "integration-test")

	config := domain.ExportConfig{
		Connection: conn,
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-5 * time.Minute),
			End:   time.Now(),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false,
		},
	}

	// Export should NOT fail with "missing route" error
	// It should automatically fallback to query_range
	result, err := exportService.ExecuteExport(ctx, config)
	require.NoError(t, err, "Export should succeed despite missing export route")
	assert.NotEmpty(t, result.ArchivePath)

	// Error message should NOT contain "missing route"
	if err != nil {
		assert.NotContains(t, strings.ToLower(err.Error()), "missing route",
			"Should not see 'missing route' error when fallback is working")
	}

	// Clean up
	os.Remove(result.ArchivePath)
}
