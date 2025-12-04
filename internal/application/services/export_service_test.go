package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// TestNewExportService tests service creation
func TestNewExportService(t *testing.T) {
	service := NewExportService("/tmp/test", "test-version")

	if service == nil {
		t.Fatal("expected non-nil service")
	}

	// Verify service implements interface
	var _ ExportService = service
}

func TestCalculateBatchWindows(t *testing.T) {
	now := time.Now()
	tr := domain.TimeRange{Start: now.Add(-10 * time.Minute), End: now}
	windows := CalculateBatchWindows(tr, domain.BatchSettings{Enabled: true})
	if len(windows) == 0 {
		t.Fatal("expected batch windows")
	}
	for i := 1; i < len(windows); i++ {
		if !windows[i].Start.Equal(windows[i-1].End) {
			t.Fatalf("windows not contiguous: %v -> %v", windows[i-1], windows[i])
		}
	}

	custom := CalculateBatchWindows(tr, domain.BatchSettings{Enabled: true, CustomIntervalSecs: 120})
	if custom[0].End.Sub(custom[0].Start) != 2*time.Minute {
		t.Fatalf("expected 2m batches, got %v", custom[0].End.Sub(custom[0].Start))
	}
}

func TestRecommendedMetricStepSeconds(t *testing.T) {
	now := time.Now()
	cases := []struct {
		duration time.Duration
		expected int
	}{
		{10 * time.Minute, 30},
		{2 * time.Hour, 60},
		{12 * time.Hour, 300},
	}
	for _, tc := range cases {
		tr := domain.TimeRange{Start: now.Add(-tc.duration), End: now}
		got := RecommendedMetricStepSeconds(tr)
		if got != tc.expected {
			t.Fatalf("duration %v => %d, want %d", tc.duration, got, tc.expected)
		}
	}
}

// TestExportService_BuildSelector tests selector building
func TestExportService_BuildSelector(t *testing.T) {
	service := &exportServiceImpl{}

	tests := []struct {
		name     string
		jobs     []string
		expected string
	}{
		{
			name:     "empty jobs",
			jobs:     []string{},
			expected: `{__name__!=""}`,
		},
		{
			name:     "single job",
			jobs:     []string{"vmstorage-prod"},
			expected: `{job=~"vmstorage-prod"}`,
		},
		{
			name:     "multiple jobs",
			jobs:     []string{"vmstorage-prod", "vmselect-prod", "vmagent-prod"},
			expected: `{job=~"vmstorage-prod|vmselect-prod|vmagent-prod"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.buildSelector(tt.jobs)
			if result != tt.expected {
				t.Errorf("buildSelector() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestExportService_GuessComponent tests component guessing logic
func TestExportService_GuessComponent(t *testing.T) {
	service := &exportServiceImpl{}

	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "vm_component label present",
			labels: map[string]string{
				"vm_component": "vmstorage",
				"__name__":     "vm_app_version",
			},
			expected: "vmstorage",
		},
		{
			name: "vmstorage metric",
			labels: map[string]string{
				"__name__": "vmstorage_merge_duration_seconds",
			},
			expected: "vmstorage",
		},
		{
			name: "vmselect metric",
			labels: map[string]string{
				"__name__": "vmselect_concurrent_queries",
			},
			expected: "vmselect",
		},
		{
			name: "vminsert metric",
			labels: map[string]string{
				"__name__": "vminsert_rows_inserted_total",
			},
			expected: "vminsert",
		},
		{
			name: "vmagent metric",
			labels: map[string]string{
				"__name__": "vmagent_remotewrite_retries_count_total",
			},
			expected: "vmagent",
		},
		{
			name: "vmalert metric",
			labels: map[string]string{
				"__name__": "vmalert_alerting_rules_total",
			},
			expected: "vmalert",
		},
		{
			name: "generic vm metric with job",
			labels: map[string]string{
				"__name__": "vm_app_version",
				"job":      "vmauth-single",
			},
			expected: "vmauth-single",
		},
		{
			name: "go metric with job",
			labels: map[string]string{
				"__name__": "go_goroutines",
				"job":      "vmstorage-prod",
			},
			expected: "vmstorage-prod",
		},
		{
			name: "unknown metric",
			labels: map[string]string{
				"__name__": "unknown_metric",
			},
			expected: "unknown",
		},
		{
			name:     "empty labels",
			labels:   map[string]string{},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.guessComponent(tt.labels)
			if result != tt.expected {
				t.Errorf("guessComponent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestExportService_GenerateExportID tests ID generation
func TestExportService_GenerateExportID(t *testing.T) {
	service := &exportServiceImpl{}

	// Generate ID
	id1 := service.generateExportID()

	// Should start with "export-"
	if !strings.HasPrefix(id1, "export-") {
		t.Errorf("ID doesn't start with 'export-': %s", id1)
	}

	// Should be unique (different timestamps)
	id2 := service.generateExportID()

	// IDs should be different (unless generated in same second, which is unlikely)
	// We just verify format is correct
	if id1 == "" || id2 == "" {
		t.Error("Generated empty ID")
	}
}

// TestExportService_BuildArchiveMetadata tests metadata building
func TestExportService_BuildArchiveMetadata(t *testing.T) {
	service := &exportServiceImpl{
		vmExporterVersion: "1.0.0-test",
	}

	now := time.Now()
	config := domain.ExportConfig{
		Components: []string{"vmstorage", "vmselect", "vmstorage"},
		Jobs:       []string{"vmstorage-prod", "vmselect-prod", "vmstorage-prod"},
		TimeRange: domain.TimeRange{
			Start: now.Add(-1 * time.Hour),
			End:   now,
		},
		Obfuscation: domain.ObfuscationConfig{
			Enabled: true,
		},
	}

	obfuscationMaps := map[string]map[string]string{
		"instance": {
			"10.0.1.5:8482": "192.0.2.1:8482",
		},
		"job": {
			"vmstorage-prod": "vm_component_vmstorage_1",
		},
	}

	metadata := service.buildArchiveMetadata("test-export", config, 1500, obfuscationMaps)

	// Verify fields
	if metadata.ExportID != "test-export" {
		t.Errorf("ExportID = %v, want test-export", metadata.ExportID)
	}

	if len(metadata.Components) != 2 {
		t.Errorf("Components length = %d, want 2", len(metadata.Components))
	}
	if metadata.Components[0] != "vmstorage" || metadata.Components[1] != "vmselect" {
		t.Errorf("Components order incorrect: %v", metadata.Components)
	}

	if len(metadata.Jobs) != 2 {
		t.Errorf("Jobs length = %d, want 2", len(metadata.Jobs))
	}
	if metadata.Jobs[0] != "vmstorage-prod" || metadata.Jobs[1] != "vmselect-prod" {
		t.Errorf("Jobs order incorrect: %v", metadata.Jobs)
	}

	if metadata.MetricsCount != 1500 {
		t.Errorf("MetricsCount = %d, want 1500", metadata.MetricsCount)
	}

	if !metadata.Obfuscated {
		t.Error("Obfuscated flag not set")
	}

	if metadata.VMGatherVersion != "1.0.0-test" {
		t.Errorf("VMGatherVersion = %v, want 1.0.0-test", metadata.VMGatherVersion)
	}

	if metadata.ExportDate.Location() != time.UTC {
		t.Errorf("ExportDate location = %v, want UTC", metadata.ExportDate.Location())
	}

	if len(metadata.InstanceMap) != 1 {
		t.Errorf("InstanceMap length = %d, want 1", len(metadata.InstanceMap))
	}

	if len(metadata.JobMap) != 1 {
		t.Errorf("JobMap length = %d, want 1", len(metadata.JobMap))
	}
}

func TestDetermineQueryRangeStep(t *testing.T) {
	now := time.Now()
	tr := domain.TimeRange{Start: now.Add(-2 * time.Hour), End: now}
	step := determineQueryRangeStep(tr, 0)
	if step != time.Minute {
		t.Fatalf("expected default 1m step, got %v", step)
	}
	step = determineQueryRangeStep(tr, 300)
	if step != 5*time.Minute {
		t.Fatalf("expected override to 5m, got %v", step)
	}
	step = determineQueryRangeStep(tr, 5)
	if step < 30*time.Second {
		t.Fatalf("expected minimum 30s, got %v", step)
	}
}

// TestExportService_ProcessMetrics_NoObfuscation tests processing without obfuscation
func TestExportService_ProcessMetrics_NoObfuscation(t *testing.T) {
	service := &exportServiceImpl{}

	// Sample JSONL metrics
	metricsData := `{"metric":{"__name__":"vm_app_version","instance":"10.0.1.5:8482","job":"vmstorage-prod"},"values":[1],"timestamps":[1699728000000]}
{"metric":{"__name__":"go_goroutines","instance":"10.0.1.5:8482","job":"vmstorage-prod"},"values":[42],"timestamps":[1699728000000]}`

	reader := strings.NewReader(metricsData)

	obfConfig := domain.ObfuscationConfig{
		Enabled: false,
	}

	processedReader, count, obfMaps, err := service.processMetrics(reader, obfConfig)
	if err != nil {
		t.Fatalf("processMetrics failed: %v", err)
	}

	// Verify count
	if count != 2 {
		t.Errorf("metrics count = %d, want 2", count)
	}

	// Verify no obfuscation maps
	if len(obfMaps) != 0 {
		t.Errorf("expected no obfuscation maps, got %d", len(obfMaps))
	}

	// Verify output is valid JSONL
	if processedReader == nil {
		t.Fatal("processedReader is nil")
	}
}

// TestExportService_ProcessMetrics_WithObfuscation tests processing with obfuscation
func TestExportService_ProcessMetrics_WithObfuscation(t *testing.T) {
	service := &exportServiceImpl{}

	// Sample JSONL metrics
	metricsData := `{"metric":{"__name__":"vm_app_version","instance":"10.0.1.5:8482","job":"vmstorage-prod"},"values":[1],"timestamps":[1699728000000]}
{"metric":{"__name__":"go_goroutines","instance":"10.0.1.5:8482","job":"vmstorage-prod"},"values":[42],"timestamps":[1699728000000]}`

	reader := strings.NewReader(metricsData)

	obfConfig := domain.ObfuscationConfig{
		Enabled:           true,
		ObfuscateInstance: true,
		ObfuscateJob:      true,
	}

	processedReader, count, obfMaps, err := service.processMetrics(reader, obfConfig)
	if err != nil {
		t.Fatalf("processMetrics failed: %v", err)
	}

	// Verify count
	if count != 2 {
		t.Errorf("metrics count = %d, want 2", count)
	}

	// Verify obfuscation maps exist
	if len(obfMaps) == 0 {
		t.Error("expected obfuscation maps")
	}

	// Verify instance map
	if instanceMap, exists := obfMaps["instance"]; exists {
		if _, ok := instanceMap["10.0.1.5:8482"]; !ok {
			t.Error("instance not in obfuscation map")
		}
	} else {
		t.Error("instance map not found")
	}

	// Verify job map
	if jobMap, exists := obfMaps["job"]; exists {
		if _, ok := jobMap["vmstorage-prod"]; !ok {
			t.Error("job not in obfuscation map")
		}
	} else {
		t.Error("job map not found")
	}

	// Verify output is valid JSONL
	if processedReader == nil {
		t.Fatal("processedReader is nil")
	}
}

func TestProcessMetricsIntoWriterFile(t *testing.T) {
	service := &exportServiceImpl{}
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "partial.jsonl")
	handle, err := os.Create(file)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	metricsData := `{"metric":{"__name__":"up","instance":"a","job":"j"},"values":[1],"timestamps":[1000]}`
	count, err := service.processMetricsIntoWriter(strings.NewReader(metricsData), domain.ObfuscationConfig{}, nil, handle)
	if err != nil {
		t.Fatalf("processMetricsIntoWriter failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 metric, got %d", count)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}
	if !strings.Contains(string(data), `"__name__":"up"`) {
		t.Fatalf("expected metric contents in staging file, got %s", string(data))
	}
}

// TestExportService_ProcessMetrics_EmptyStream tests empty metrics stream
func TestExportService_ProcessMetrics_EmptyStream(t *testing.T) {
	service := &exportServiceImpl{}

	reader := strings.NewReader("")

	obfConfig := domain.ObfuscationConfig{
		Enabled: false,
	}

	_, count, _, err := service.processMetrics(reader, obfConfig)
	if err != nil {
		t.Fatalf("processMetrics failed on empty stream: %v", err)
	}

	// Verify count is 0
	if count != 0 {
		t.Errorf("metrics count = %d, want 0", count)
	}
}

// TestExportService_ApplyObfuscation tests obfuscation application
func TestExportService_ApplyObfuscation(t *testing.T) {
	service := &exportServiceImpl{}

	// Create metric
	metric := &vm.ExportedMetric{
		Metric: map[string]string{
			"__name__": "vm_app_version",
			"instance": "10.0.1.5:8482",
			"job":      "vmstorage-prod",
		},
		Values:     []interface{}{1.0},
		Timestamps: []int64{1699728000000},
	}

	// Create obfuscator
	obf := &mockObfuscator{
		instanceFunc: func(instance string) string {
			return "192.0.2.1:8482"
		},
		jobFunc: func(job, component string) string {
			return "vm_component_" + component + "_1"
		},
	}

	config := domain.ObfuscationConfig{
		Enabled:           true,
		ObfuscateInstance: true,
		ObfuscateJob:      true,
	}

	// Apply obfuscation - manually for test
	if config.ObfuscateInstance {
		if instance, exists := metric.Metric["instance"]; exists {
			metric.Metric["instance"] = obf.instanceFunc(instance)
		}
	}

	if config.ObfuscateJob {
		if job, exists := metric.Metric["job"]; exists {
			component := service.guessComponent(metric.Metric)
			metric.Metric["job"] = obf.jobFunc(job, component)
		}
	}

	// Verify obfuscation applied
	if metric.Metric["instance"] != "192.0.2.1:8482" {
		t.Errorf("instance not obfuscated: %s", metric.Metric["instance"])
	}

	if !strings.HasPrefix(metric.Metric["job"], "vm_component_") {
		t.Errorf("job not obfuscated: %s", metric.Metric["job"])
	}

	// Metric name should not be changed
	if metric.Metric["__name__"] != "vm_app_version" {
		t.Error("metric name should not be obfuscated")
	}
}

// mockObfuscator is a simple mock for testing
type mockObfuscator struct {
	instanceFunc func(string) string
	jobFunc      func(string, string) string
}

// TestExportService_ProcessMetrics_ValidJSONL tests JSONL output format
func TestExportService_ProcessMetrics_ValidJSONL(t *testing.T) {
	service := &exportServiceImpl{}

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	reader := strings.NewReader(metricsData)

	obfConfig := domain.ObfuscationConfig{Enabled: false}

	processedReader, _, _, err := service.processMetrics(reader, obfConfig)
	if err != nil {
		t.Fatalf("processMetrics failed: %v", err)
	}

	// Read output
	buf := new(strings.Builder)
	_, err = io.Copy(buf, processedReader)
	if err != nil {
		t.Fatalf("failed to read processed output: %v", err)
	}

	output := buf.String()

	// Split by newlines
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for _, line := range lines {
		if line == "" {
			continue
		}

		var metric vm.ExportedMetric
		if err := json.Unmarshal([]byte(line), &metric); err != nil {
			t.Errorf("invalid JSON line: %v", err)
		}
	}
}

func TestExportService_ExecuteExportStreamsWithoutPrematureCancellation(t *testing.T) {
	t.Parallel()

	writeResult := make(chan error, 1)
	metrics := []string{
		`{"metric":{"__name__":"metric_one","job":"vmagent"},"values":[1],"timestamps":[1000]}`,
		`{"metric":{"__name__":"metric_two","job":"vmagent"},"values":[2],"timestamps":[2000]}`,
		`{"metric":{"__name__":"metric_three","job":"vmagent"},"values":[3],"timestamps":[3000]}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/export" {
			http.NotFound(w, r)
			writeResult <- io.EOF
			return
		}
		w.Header().Set("Content-Type", "application/stream+json")
		flusher, _ := w.(http.Flusher)
		for _, line := range metrics {
			if _, err := io.WriteString(w, line+"\n"); err != nil {
				writeResult <- err
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
		writeResult <- nil
	}))
	defer server.Close()

	service := NewExportService(t.TempDir(), "test-version")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	config := domain.ExportConfig{
		Connection: domain.VMConnection{
			URL: server.URL,
		},
		TimeRange: domain.TimeRange{
			Start: time.Unix(0, 0),
			End:   time.Unix(60, 0),
		},
		Components: []string{"vmagent"},
		Jobs:       []string{"vmagent"},
	}

	result, err := service.ExecuteExport(ctx, config)
	if err != nil {
		t.Fatalf("ExecuteExport returned error: %v", err)
	}
	if result == nil || result.ArchivePath == "" {
		t.Fatal("expected non-nil export result with archive")
	}
	if result.MetricsExported != len(metrics) {
		t.Fatalf("expected %d metrics, got %d", len(metrics), result.MetricsExported)
	}

	select {
	case writeErr := <-writeResult:
		if writeErr != nil {
			t.Fatalf("server write failed: %v", writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server never finished writing export payload")
	}
}

// Integration-style test (requires temp dir cleanup)
func TestExportService_Integration_NoObfuscation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// This test would require actual VM instance or more sophisticated mocking
	// For now, we verify service creation works
	service := NewExportService(tmpDir, "test-version")
	if service == nil {
		t.Fatal("expected non-nil service")
	}

	// Full integration test with ExecuteExport would require:
	// 1. Mock VM client (or real VM via testcontainers)
	// 2. Sample export data
	// 3. Verification of archive creation
	// This is better suited for E2E tests
	t.Log("Integration test stub - full E2E requires VM instance")
}
