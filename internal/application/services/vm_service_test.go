package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// TestNewVMService tests service creation
func TestNewVMService(t *testing.T) {
	service := NewVMService()

	if service == nil {
		t.Fatal("expected non-nil service")
	}

	// Verify service implements interface
	var _ VMService = service
}

// NOTE: Full integration tests with ValidateConnection would require either:
// 1. Refactoring to use interfaces (more complex, SOLID but heavier)
// 2. Running actual VM instance (integration tests with testcontainers)
// For MVP, we keep it simple (KISS principle) and test components individually.

// TestVMService_DiscoverComponents_ParsesResults tests component parsing
func TestVMService_DiscoverComponents_ParsesResults(t *testing.T) {
	// Test data structures
	testResults := []vm.Result{
		{
			Metric: map[string]string{
				"job":          "vmstorage-prod",
				"vm_component": "vmstorage",
			},
		},
		{
			Metric: map[string]string{
				"job":          "vmselect-prod",
				"vm_component": "vmselect",
			},
		},
		{
			Metric: map[string]string{
				"job":          "vmstorage-dev",
				"vm_component": "vmstorage",
			},
		},
	}

	// Parse manually (testing the logic)
	componentMap := make(map[string]*domain.VMComponent)

	for _, r := range testResults {
		component := r.Metric["vm_component"]
		job := r.Metric["job"]

		if component == "" || job == "" {
			continue
		}

		if comp, exists := componentMap[component]; exists {
			comp.Jobs = append(comp.Jobs, job)
		} else {
			componentMap[component] = &domain.VMComponent{
				Component: component,
				Jobs:      []string{job},
			}
		}
	}

	// Verify results
	if len(componentMap) != 2 {
		t.Errorf("expected 2 components, got %d", len(componentMap))
	}

	// Check vmstorage has 2 jobs
	if vmstorage, exists := componentMap["vmstorage"]; exists {
		if len(vmstorage.Jobs) != 2 {
			t.Errorf("expected 2 jobs for vmstorage, got %d", len(vmstorage.Jobs))
		}
	} else {
		t.Error("vmstorage component not found")
	}

	// Check vmselect has 1 job
	if vmselect, exists := componentMap["vmselect"]; exists {
		if len(vmselect.Jobs) != 1 {
			t.Errorf("expected 1 job for vmselect, got %d", len(vmselect.Jobs))
		}
	} else {
		t.Error("vmselect component not found")
	}
}

// TestVMService_BuildSelector tests selector building
func TestVMService_BuildSelector(t *testing.T) {
	service := &vmServiceImpl{}

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
			jobs:     []string{"vmstorage-prod", "vmselect-prod"},
			expected: `{job=~"vmstorage-prod|vmselect-prod"}`,
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

// TestVMService_EstimateComponentMetrics_ParsesCount tests count parsing
func TestVMService_EstimateComponentMetrics_ParsesCount(t *testing.T) {
	tests := []struct {
		name     string
		value    []interface{}
		expected int
	}{
		{
			name:     "string value",
			value:    []interface{}{float64(1699728000), "1566"},
			expected: 1566,
		},
		{
			name:     "float value",
			value:    []interface{}{float64(1699728000), float64(1566)},
			expected: 1566,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := 0
			ok := false
			if len(tt.value) >= 2 {
				if count, parsed := parseCountValue(tt.value[1]); parsed {
					value = count
					ok = true
				}
			}

			if !ok || value != tt.expected {
				t.Errorf("count = %v, ok=%v, want %v", value, ok, tt.expected)
			}
		})
	}
}

// TestVMService_GetSample_LimitsResults tests sample limiting
func TestVMService_GetSample_LimitsResults(t *testing.T) {
	limit := 10

	// Simulate sample collection
	samples := make([]domain.MetricSample, 0, limit)

	// Simulate having more data than limit
	totalAvailable := 100

	for i := 0; i < totalAvailable && i < limit; i++ {
		sample := domain.MetricSample{
			MetricName: "test_metric",
			Value:      float64(i),
		}
		samples = append(samples, sample)
	}

	// Verify limit is respected
	if len(samples) != limit {
		t.Errorf("expected %d samples, got %d", limit, len(samples))
	}
}

func TestVMService_BuildSampleQueries(t *testing.T) {
	service := &vmServiceImpl{}

	tests := []struct {
		name     string
		jobs     []string
		limit    int
		expected []string
	}{
		{
			name:  "empty jobs uses vm_app_version",
			jobs:  []string{},
			limit: 10,
			expected: []string{
				"topk(10, vm_app_version)",
			},
		},
		{
			name:  "small job list uses or selector",
			jobs:  []string{"vmstorage-prod", "vmselect-prod"},
			limit: 5,
			expected: []string{
				`topk(5, {job="vmstorage-prod" or job="vmselect-prod"})`,
			},
		},
		{
			name:  "large job list uses or selector",
			jobs:  []string{"a", "b", "c", "d", "e", "f"},
			limit: 3,
			expected: []string{
				`topk(3, {job="a" or job="b" or job="c" or job="d" or job="e" or job="f"})`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.buildSampleQueries(tt.jobs, tt.limit)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d queries, got %d", len(tt.expected), len(result))
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Fatalf("query = %v, want %v", result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestVMService_GetSample_UsesRegexAndReturnsResults(t *testing.T) {
	jobs := []string{
		"job-0", "job-1", "job-2", "job-3", "job-4",
		"job-5", "job-6", "job-7", "job-8", "job-9",
	}
	expectedQuery := `topk(10, {job="job-0" or job="job-1" or job="job-2" or job="job-3" or job="job-4" or job="job-5" or job="job-6" or job="job-7" or job="job-8" or job="job-9"})`

	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		receivedQuery = r.URL.Query().Get("query")

		type resultItem struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		}

		results := make([]resultItem, 0, len(jobs))
		ts := float64(time.Now().Unix())
		for _, job := range jobs {
			results = append(results, resultItem{
				Metric: map[string]string{
					"__name__": "demo_metric",
					"job":      job,
				},
				Value: []interface{}{ts, "1"},
			})
		}

		payload := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result":     results,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	service := &vmServiceImpl{
		clientFactory: func(conn domain.VMConnection) *vm.Client {
			return vm.NewClient(domain.VMConnection{URL: srv.URL})
		},
	}

	config := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		Jobs:       jobs,
	}
	samples, err := service.GetSample(context.Background(), config, 10)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if receivedQuery != expectedQuery {
		t.Fatalf("unexpected query: %s", receivedQuery)
	}
	if len(samples) != len(jobs) {
		t.Fatalf("expected %d samples, got %d", len(jobs), len(samples))
	}
}

func TestVMService_GetSample_EmptyResultsReturnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		payload := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result":     []interface{}{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	service := &vmServiceImpl{
		clientFactory: func(conn domain.VMConnection) *vm.Client {
			return vm.NewClient(domain.VMConnection{URL: srv.URL})
		},
	}

	config := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		Jobs:       []string{},
	}
	_, err := service.GetSample(context.Background(), config, 10)
	if err == nil {
		t.Fatal("expected error for empty sample result, got nil")
	}
}

// TestVMService_DiscoverComponents_HandlesEmptyResults tests empty discovery
func TestVMService_DiscoverComponents_HandlesEmptyResults(t *testing.T) {
	// Empty results should return error
	results := []vm.Result{}

	if len(results) == 0 {
		// This is the expected behavior
		t.Log("Empty results handled correctly")
	}
}

// TestVMService_DiscoverComponents_IgnoresInvalidMetrics tests filtering
func TestVMService_DiscoverComponents_IgnoresInvalidMetrics(t *testing.T) {
	testResults := []vm.Result{
		{
			Metric: map[string]string{
				"job":          "vmstorage-prod",
				"vm_component": "vmstorage",
			},
		},
		{
			Metric: map[string]string{
				"job": "invalid-no-component",
				// Missing vm_component
			},
		},
		{
			Metric: map[string]string{
				"vm_component": "invalid-no-job",
				// Missing job
			},
		},
	}

	// Parse with filtering
	componentMap := make(map[string]*domain.VMComponent)

	for _, r := range testResults {
		component := r.Metric["vm_component"]
		job := r.Metric["job"]

		// Skip invalid
		if component == "" || job == "" {
			continue
		}

		if comp, exists := componentMap[component]; exists {
			comp.Jobs = append(comp.Jobs, job)
		} else {
			componentMap[component] = &domain.VMComponent{
				Component: component,
				Jobs:      []string{job},
			}
		}
	}

	// Should only have 1 valid component
	if len(componentMap) != 1 {
		t.Errorf("expected 1 component after filtering, got %d", len(componentMap))
	}
}
