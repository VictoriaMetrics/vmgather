package integration

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestObfuscation_RealExport tests that obfuscation actually works in real export
func TestObfuscation_RealExport(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Use local VM without auth (from env when available)
	conn := domain.VMConnection{
		URL: vmSingleNoAuthURL(),
	}

	exportService := services.NewExportService(t.TempDir(), "integration-test")

	config := domain.ExportConfig{
		Connection: conn,
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-15 * time.Minute),
			End:   time.Now(),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled:           true,
			ObfuscateInstance: true, // CRITICAL: Must be true
			ObfuscateJob:      true, // CRITICAL: Must be true
			PreserveStructure: true,
		},
	}

	t.Run("Export_WithObfuscation", func(t *testing.T) {
		result, err := exportService.ExecuteExport(ctx, config)
		require.NoError(t, err, "Export should succeed")
		assert.True(t, result.ObfuscationApplied, "Obfuscation should be applied")
		assert.NotEmpty(t, result.ArchivePath, "Archive should be created")

		// Verify archive exists
		_, err = os.Stat(result.ArchivePath)
		require.NoError(t, err, "Archive file should exist")

		// Extract and verify obfuscation
		metrics := extractMetricsFromArchive(t, result.ArchivePath)
		require.NotEmpty(t, metrics, "Should have metrics in archive")

		// Obfuscated values are hex SHA-256 digests, the same as vmagent -remoteWrite.obfuscateLabels.
		isDigest := regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString
		foundInstance := false
		for _, metric := range metrics {
			if instance, ok := metric.Metric["instance"]; ok {
				foundInstance = true
				assert.True(t, isDigest(instance), "instance is not a SHA-256 digest: %s", instance)
			}
			if job, ok := metric.Metric["job"]; ok {
				assert.True(t, isDigest(job), "job is not a SHA-256 digest: %s", job)
			}
		}
		assert.True(t, foundInstance, "Should find obfuscated instance labels")

		// Clean up
		os.Remove(result.ArchivePath)
	})
}

// TestObfuscation_NoObfuscation tests that without obfuscation, real IPs are preserved
func TestObfuscation_NoObfuscation(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := domain.VMConnection{
		URL: vmSingleNoAuthURL(),
	}

	exportService := services.NewExportService(t.TempDir(), "integration-test")

	config := domain.ExportConfig{
		Connection: conn,
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-15 * time.Minute),
			End:   time.Now(),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false, // Disabled
		},
	}

	result, err := exportService.ExecuteExport(ctx, config)
	require.NoError(t, err, "Export should succeed")
	assert.False(t, result.ObfuscationApplied, "Obfuscation should NOT be applied")

	// Extract and verify NO obfuscation
	metrics := extractMetricsFromArchive(t, result.ArchivePath)
	require.NotEmpty(t, metrics, "Should have metrics in archive")

	foundObfuscatedInstance := false

	for _, metric := range metrics {
		if instance, ok := metric.Metric["instance"]; ok {
			// Must not be a SHA-256 digest
			if regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(instance) {
				foundObfuscatedInstance = true
				t.Errorf("Found obfuscated instance when obfuscation disabled: %s", instance)
			}
		}
	}

	assert.False(t, foundObfuscatedInstance, "Should NOT find obfuscated instances when disabled")

	// Clean up
	os.Remove(result.ArchivePath)
}

// TestObfuscation_OnlyInstance tests obfuscating only instance, not job
func TestObfuscation_OnlyInstance(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := domain.VMConnection{
		URL: vmSingleNoAuthURL(),
	}

	exportService := services.NewExportService(t.TempDir(), "integration-test")

	config := domain.ExportConfig{
		Connection: conn,
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-15 * time.Minute),
			End:   time.Now(),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled:           true,
			ObfuscateInstance: true,  // Only instance
			ObfuscateJob:      false, // NOT job
			PreserveStructure: true,
		},
	}

	result, err := exportService.ExecuteExport(ctx, config)
	require.NoError(t, err, "Export should succeed")
	assert.True(t, result.ObfuscationApplied, "Obfuscation should be applied")

	// Extract and verify
	metrics := extractMetricsFromArchive(t, result.ArchivePath)
	require.NotEmpty(t, metrics, "Should have metrics in archive")

	foundObfuscatedInstance := false
	foundOriginalJob := false

	for _, metric := range metrics {
		if instance, ok := metric.Metric["instance"]; ok {
			if regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(instance) {
				foundObfuscatedInstance = true
			}
		}

		if job, ok := metric.Metric["job"]; ok {
			// Job should be original (not a SHA-256 digest)
			if job == "vmagent" {
				foundOriginalJob = true
			}
		}
	}

	assert.True(t, foundObfuscatedInstance, "Instance should be obfuscated (SHA-256 digest)")
	assert.True(t, foundOriginalJob, "Job should NOT be obfuscated")

	// Clean up
	os.Remove(result.ArchivePath)
}

// Helper function to extract metrics from zip archive
func extractMetricsFromArchive(t *testing.T, archivePath string) []vm.ExportedMetric {
	zipReader, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer zipReader.Close()

	var metrics []vm.ExportedMetric

	for _, file := range zipReader.File {
		// Find metrics.jsonl file
		if strings.HasSuffix(file.Name, "metrics.jsonl") {
			rc, err := file.Open()
			require.NoError(t, err)
			defer rc.Close()

			// Read and parse JSONL
			decoder := json.NewDecoder(rc)
			for {
				var metric vm.ExportedMetric
				if err := decoder.Decode(&metric); err == io.EOF {
					break
				} else if err != nil {
					t.Logf("Warning: failed to decode metric: %v", err)
					continue
				}
				metrics = append(metrics, metric)

				// Limit to first 100 for testing
				if len(metrics) >= 100 {
					return metrics
				}
			}
		}
	}

	return metrics
}
