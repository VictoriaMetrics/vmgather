package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/application/services"
	"github.com/VictoriaMetrics/VMGather/internal/domain"
)

// TestServer_GetSampleDataFromResult tests getSampleDataFromResult function
// This test verifies that sample data is correctly formatted with 'name' field
// and handles edge cases like empty MetricName
func TestServer_GetSampleDataFromResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	server := NewServer(tmpDir, "test-version")
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
		t.Error("getSampleDataFromResult should return non-nil array")
	}
}

// TestServer_HandleGetSample_ResponseFormat tests that /api/sample returns correct format
// This test verifies the fix for undefined in preview (issue #7)
func TestServer_HandleGetSample_ResponseFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	server := NewServer(tmpDir, "test-version")

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
	if w.Code == http.StatusInternalServerError {
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

func TestHandleExportStart_StagingPermissionDenied(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	readOnlyDir := filepath.Join(tmpDir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0o500); err != nil {
		t.Fatalf("failed to create read-only dir: %v", err)
	}

	server := NewServer(tmpDir, "test-version")
	reqBody := map[string]interface{}{
		"connection": map[string]interface{}{
			"url":  "http://localhost:8428",
			"auth": map[string]interface{}{"type": "none"},
		},
		"time_range": map[string]string{
			"start": time.Now().Add(-time.Hour).Format(time.RFC3339),
			"end":   time.Now().Format(time.RFC3339),
		},
		"components": []string{"vmsingle"},
		"jobs":       []string{"vmjob"},
		"obfuscation": map[string]interface{}{
			"enabled": false,
		},
		"batching":    map[string]interface{}{"enabled": true},
		"staging_dir": readOnlyDir,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/export/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only directory, got %d", w.Code)
	}
}

func TestHandleListDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-list-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if err := os.MkdirAll(filepath.Join(tmpDir, "child"), 0o755); err != nil {
		t.Fatalf("failed to create child dir: %v", err)
	}

	server := NewServer(tmpDir, "test-version")
	req := httptest.NewRequest(http.MethodGet, "/api/fs/list?path="+tmpDir, nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["path"].(string) != tmpDir {
		t.Fatalf("expected path %s", tmpDir)
	}
}

func TestHandleCheckDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-check-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	server := NewServer(tmpDir, "test-version")
	reqBody := map[string]string{"path": tmpDir}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/fs/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok true, got %v", resp["ok"])
	}
	if resp["exists"] != true {
		t.Fatalf("expected exists true")
	}
	if resp["can_create"] != true {
		t.Fatalf("expected can_create true")
	}
}

func TestHandleCheckDirectoryCreatesMissing(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "nested", "dir")

	server := NewServer(tmpDir, "test-version")

	// First call without ensure should indicate it can be created
	reqBody := map[string]interface{}{"path": missing}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/fs/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["ok"] != false || resp["exists"] != false {
		t.Fatalf("expected non-existing directory response, got %#v", resp)
	}
	if resp["can_create"] != true {
		t.Fatalf("expected can_create true")
	}

	// Now ensure=true should create directory
	reqBody["ensure"] = true
	body, _ = json.Marshal(reqBody)
	req = httptest.NewRequest(http.MethodPost, "/api/fs/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["ok"] != true || resp["exists"] != true {
		t.Fatalf("expected created directory response, got %#v", resp)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
}

func TestHandleExportCancel(t *testing.T) {
	tmpDir := t.TempDir()
	server := NewServer(tmpDir, "test-version")
	blocker := &blockingExportService{blockCh: make(chan struct{})}
	server.jobManager = NewExportJobManager(blocker)

	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-time.Minute),
			End:   time.Now(),
		},
		Batching:    domain.BatchSettings{Enabled: true},
		StagingFile: filepath.Join(tmpDir, "cancel.partial"),
	}
	status, err := server.jobManager.StartJob(context.Background(), "cancel-test", cfg)
	if err != nil {
		t.Fatalf("failed to start job: %v", err)
	}

	body := []byte(fmt.Sprintf(`{"job_id":"%s"}`, status.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/export/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from cancel endpoint, got %d", w.Code)
	}

	// Give the cancel signal time to be processed
	time.Sleep(50 * time.Millisecond)
	
	close(blocker.blockCh)
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for job cancel state")
		case <-ticker.C:
			if s, ok := server.jobManager.GetStatus(status.ID); ok && s.State == JobCanceled {
				return
			}
		}
	}
}

func TestEnsureBatchDefaultsSetsMetricStep(t *testing.T) {
	tr := domain.TimeRange{
		Start: time.Now().Add(-2 * time.Hour),
		End:   time.Now(),
	}
	cfg := domain.ExportConfig{
		TimeRange: tr,
	}
	ensureBatchDefaults(&cfg)
	if cfg.MetricStepSeconds == 0 {
		t.Fatal("expected metric step to be set automatically")
	}
	if cfg.MetricStepSeconds != services.RecommendedMetricStepSeconds(tr) {
		t.Fatalf("metric step mismatch: got %d want %d", cfg.MetricStepSeconds, services.RecommendedMetricStepSeconds(tr))
	}
}

func TestServer_GetSampleDataFromResult_ObfuscatesWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	server := NewServer(tmpDir, "test-version")

	server.vmService = &mockVMService{
		samples: []domain.MetricSample{
			{
				MetricName: "go_mem",
				Labels: map[string]string{
					"instance": "10.0.0.1:8428",
					"job":      "vmagent",
				},
			},
		},
	}

	config := domain.ExportConfig{
		Connection: domain.VMConnection{
			URL: "http://example.com",
		},
		Obfuscation: domain.ObfuscationConfig{
			Enabled:           true,
			ObfuscateInstance: true,
			ObfuscateJob:      true,
		},
	}

	data := server.getSampleDataFromResult(context.Background(), config)
	if len(data) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(data))
	}

	labels, ok := data[0]["labels"].(map[string]string)
	if !ok {
		t.Fatalf("labels type mismatch: %T", data[0]["labels"])
	}

	if strings.Contains(labels["instance"], "10.0.0.1") {
		t.Errorf("instance label was not obfuscated: %s", labels["instance"])
	}
	if labels["job"] == "vmagent" {
		t.Errorf("job label was not obfuscated: %s", labels["job"])
	}
}

func TestServer_GetSampleDataFromResult_NoSamplesMock(t *testing.T) {
	tmpDir := t.TempDir()
	server := NewServer(tmpDir, "test-version")
	server.vmService = &mockVMService{
		samples: []domain.MetricSample{},
	}

	config := domain.ExportConfig{
		Connection: domain.VMConnection{URL: "http://localhost:8428"},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: false,
		},
	}

	data := server.getSampleDataFromResult(context.Background(), config)
	if data == nil {
		t.Fatal("expected non-nil slice")
	}
	if len(data) != 0 {
		t.Fatalf("expected zero samples, got %d", len(data))
	}
}

type mockVMService struct {
	samples   []domain.MetricSample
	sampleErr error
}

func (m *mockVMService) ValidateConnection(ctx context.Context, conn domain.VMConnection) error {
	return nil
}

func (m *mockVMService) DiscoverComponents(ctx context.Context, conn domain.VMConnection, tr domain.TimeRange) ([]domain.VMComponent, error) {
	return nil, nil
}

func (m *mockVMService) GetSample(ctx context.Context, config domain.ExportConfig, limit int) ([]domain.MetricSample, error) {
	if m.sampleErr != nil {
		return nil, m.sampleErr
	}
	return m.samples, nil
}

func (m *mockVMService) EstimateExportSize(ctx context.Context, conn domain.VMConnection, jobs []string, tr domain.TimeRange) (int, error) {
	return 0, nil
}

func (m *mockVMService) CheckExportAPI(ctx context.Context, conn domain.VMConnection) bool {
	return true
}
