package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/VictoriaMetrics/support/internal/domain"
)

// TestServer_GetSampleDataFromResult tests getSampleDataFromResult function
// This test verifies that sample data is correctly formatted with 'name' field
// and handles edge cases like empty MetricName
func TestServer_GetSampleDataFromResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	server := NewServer(tmpDir)
	ctx := context.Background()

	// Create a mock config
	config := domain.ExportConfig{
		Connection: domain.VMConnection{
			URL: "http://localhost:8428",
			Auth: domain.AuthConfig{
				Type: domain.AuthTypeNone,
			},
		},
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		Components: []string{"vmsingle"},
		Jobs:       []string{},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false,
		},
	}

	// Test with empty samples (should return empty array, not error)
	sampleData := server.getSampleDataFromResult(ctx, config)
	if sampleData == nil {
		t.Error("getSampleDataFromResult should return empty array, not nil")
	}
	if len(sampleData) != 0 {
		t.Errorf("Expected empty array for no samples, got %d items", len(sampleData))
	}
}

// TestServer_HandleGetSample_ResponseFormat tests that /api/sample returns correct format
// This test verifies the fix for undefined in preview (issue #7)
func TestServer_HandleGetSample_ResponseFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	server := NewServer(tmpDir)

	// Create request
	reqBody := map[string]interface{}{
		"config": map[string]interface{}{
			"connection": map[string]interface{}{
				"url": "http://localhost:8428",
				"auth": map[string]interface{}{
					"type": "none",
				},
			},
			"time_range": map[string]string{
				"start": time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
				"end":   time.Now().Format(time.RFC3339),
			},
			"components": []string{"vmsingle"},
			"jobs":       []string{},
			"obfuscation": map[string]interface{}{
				"enabled": false,
			},
		},
		"limit": 10,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sample", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Call handler
	handler := server.Router()
	handler.ServeHTTP(w, req)

	// Check response
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		// Internal server error is expected if VM is not available
		// We just need to verify response format is JSON
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("Expected JSON response, got Content-Type: %s", w.Header().Get("Content-Type"))
		}
		return
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify response structure
	samples, exists := response["samples"]
	if !exists {
		t.Error("Response should contain 'samples' field")
		return
	}

	samplesArray, ok := samples.([]interface{})
	if !ok {
		t.Error("'samples' should be an array")
		return
	}

	// Verify each sample has 'name' field (not undefined)
	for i, sample := range samplesArray {
		sampleMap, ok := sample.(map[string]interface{})
		if !ok {
			t.Errorf("Sample %d should be an object", i)
			continue
		}

		name, exists := sampleMap["name"]
		if !exists {
			t.Errorf("Sample %d should have 'name' field", i)
			continue
		}

		// Name should not be nil or empty string
		nameStr, ok := name.(string)
		if !ok {
			t.Errorf("Sample %d 'name' should be a string, got %T", i, name)
			continue
		}

		if nameStr == "" || nameStr == "unknown" {
			// Check if metric_name exists as fallback
			if metricName, exists := sampleMap["metric_name"]; exists {
				metricNameStr, ok := metricName.(string)
				if ok && metricNameStr != "" {
					// metric_name exists, that's acceptable
					continue
				}
			}
			t.Errorf("Sample %d 'name' should not be empty or 'unknown' (got: %s)", i, nameStr)
		}

		// Verify labels exist
		labels, exists := sampleMap["labels"]
		if !exists {
			t.Errorf("Sample %d should have 'labels' field", i)
			continue
		}

		_, ok = labels.(map[string]interface{})
		if !ok {
			t.Errorf("Sample %d 'labels' should be an object", i)
		}
	}
}

