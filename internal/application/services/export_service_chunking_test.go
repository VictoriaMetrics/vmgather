package services

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
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

type exportMetadataForTest struct {
	MetricStepSeconds int    `json:"metric_step_seconds"`
	Sampled           bool   `json:"sampled"`
	AdaptiveMode      string `json:"adaptive_mode"`
	AdaptiveDecisions []struct {
		Strategy    string `json:"strategy"`
		StepSeconds int    `json:"step_seconds"`
	} `json:"adaptive_decisions"`
}

func TestExportViaQueryRange_Chunking(t *testing.T) {
	// We want to verify that a large time range is split into 1-hour chunks
	startTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := startTime.Add(3 * time.Hour) // 3 hours total

	// Track requests
	var requests []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a query_range request
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("Expected query_range, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")
		requests = append(requests, fmt.Sprintf("%s-%s", start, end))

		// Parse times to verify chunk size
		sTime, _ := time.Parse(time.RFC3339, start)
		eTime, _ := time.Parse(time.RFC3339, end)
		duration := eTime.Sub(sTime)

		if duration > 1*time.Hour+time.Second { // Allow 1s buffer
			t.Errorf("Chunk duration %v exceeds 1 hour", duration)
		}

		// Return empty result to keep it simple
		response := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result":     []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	// Create service
	svc := &exportServiceImpl{
		clientFactory: vm.NewClient,
	}

	// Create client pointing to test server
	conn := domain.VMConnection{
		URL: ts.URL,
	}
	client := vm.NewClient(conn)

	// Call exportViaQueryRange
	ctx := context.Background()
	tr := domain.TimeRange{Start: startTime, End: endTime}

	reader, err := svc.exportViaQueryRange(ctx, client, "{__name__!=\"\"}", tr, 0)
	if err != nil {
		t.Fatalf("exportViaQueryRange failed: %v", err)
	}

	// Read all data to trigger the streaming
	_, err = io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read stream: %v", err)
	}
	_ = reader.Close()

	// Verify requests
	// Should have at least 3 requests (0-1, 1-2, 2-3)
	if len(requests) < 3 {
		t.Errorf("Expected at least 3 requests, got %d", len(requests))
	}

	t.Logf("Requests made: %v", requests)
}

func TestQueryRangeTimeoutSplitsTimeWindow(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		startUnix := time.UnixMilli(parseVMTime(t, r.URL.Query().Get("start")))
		endUnix := time.UnixMilli(parseVMTime(t, r.URL.Query().Get("end")))
		duration := endUnix.Sub(startUnix)
		requests = append(requests, duration.String())
		if duration > 15*time.Second {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{
					map[string]any{
						"metric": map[string]string{
							"__name__": "vm_rows_inserted_total",
							"job":      "vmagent",
						},
						"values": [][]any{{float64(startUnix.Unix()), "1"}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	service := NewExportService(t.TempDir(), "test")
	cfg := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		TimeRange: domain.TimeRange{
			Start: time.Unix(0, 0),
			End:   time.Unix(60, 0),
		},
		Mode:      domain.ExportModeCustom,
		QueryType: domain.QueryModeMetricsQL,
		Query:     `rate(vm_rows_inserted_total[5m])`,
		Batching:  domain.BatchSettings{Enabled: false, Strategy: "manual"},
	}
	ApplyExportDefaults(&cfg)

	result, err := service.ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ExecuteExport failed: %v", err)
	}
	if result.MetricsExported == 0 {
		t.Fatalf("expected exported metrics, got %+v", result)
	}
	if len(requests) < 3 {
		t.Fatalf("expected split query_range requests, got %v", requests)
	}
}

func TestQueryRangeTimeoutStopsAtMinWindow(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`)
	}))
	defer srv.Close()

	service := NewExportService(t.TempDir(), "test")
	cfg := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		TimeRange: domain.TimeRange{
			Start: time.Unix(0, 0),
			End:   time.Unix(60, 0),
		},
		Mode:      domain.ExportModeCustom,
		QueryType: domain.QueryModeMetricsQL,
		Query:     `rate(vm_rows_inserted_total[5m])`,
		Batching:  domain.BatchSettings{Enabled: false, Strategy: "manual"},
		Safety: domain.ExportSafetyConfig{
			AutoSplit:        true,
			SplitByJob:       true,
			MinWindowSeconds: 5,
			MaxSplitDepth:    8,
		},
	}
	ApplyExportDefaults(&cfg)

	_, err := service.ExecuteExport(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected timeout failure")
	}
	if !strings.Contains(err.Error(), "-search.maxQueryDuration") {
		t.Fatalf("expected actionable timeout error, got %v", err)
	}
	if requests == 0 || requests > 64 {
		t.Fatalf("expected bounded retry count, got %d", requests)
	}
}

func TestAutopilotIncreasesMetricStepAfterMinWindow(t *testing.T) {
	var steps []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		step := r.URL.Query().Get("step")
		steps = append(steps, step)
		if step == "30s" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{
					map[string]any{
						"metric": map[string]string{
							"__name__": "vm_rows_inserted_total",
							"job":      "vmagent",
						},
						"values": [][]any{{float64(0), "1"}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	service := NewExportService(t.TempDir(), "test")
	cfg := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		TimeRange: domain.TimeRange{
			Start: time.Unix(0, 0),
			End:   time.Unix(10, 0),
		},
		Mode:      domain.ExportModeCustom,
		QueryType: domain.QueryModeMetricsQL,
		Query:     `rate(vm_rows_inserted_total[5m])`,
		Batching:  domain.BatchSettings{Enabled: false, Strategy: "manual"},
		Safety: domain.ExportSafetyConfig{
			Mode:              domain.ExportAdaptivityAutopilot,
			AutoSplit:         true,
			SplitByJob:        true,
			MinWindowSeconds:  10,
			MaxSplitDepth:     8,
			MaxStepSeconds:    300,
			StepLadderSeconds: []int{30, 60, 300},
		},
	}
	ApplyExportDefaults(&cfg)

	result, err := service.ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ExecuteExport failed: %v", err)
	}
	if result.MetricsExported != 1 {
		t.Fatalf("expected one exported metric, got %d", result.MetricsExported)
	}
	if strings.Join(steps, ",") != "30s,60s" {
		t.Fatalf("expected autopilot to retry with 60s step, got %v", steps)
	}
	metadata := readExportMetadataForTest(t, result.ArchivePath)
	if !metadata.Sampled || metadata.MetricStepSeconds != 60 {
		t.Fatalf("expected sampled metadata at 60s, got %+v", metadata)
	}
	if len(metadata.AdaptiveDecisions) == 0 || metadata.AdaptiveDecisions[len(metadata.AdaptiveDecisions)-1].Strategy != "increase_step" {
		t.Fatalf("expected increase_step decision in metadata, got %+v", metadata.AdaptiveDecisions)
	}
}

func TestAutopilotStopsAtFiveMinuteMetricStep(t *testing.T) {
	var steps []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		steps = append(steps, r.URL.Query().Get("step"))
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`)
	}))
	defer srv.Close()

	service := NewExportService(t.TempDir(), "test")
	cfg := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		TimeRange: domain.TimeRange{
			Start: time.Unix(0, 0),
			End:   time.Unix(10, 0),
		},
		Mode:      domain.ExportModeCustom,
		QueryType: domain.QueryModeMetricsQL,
		Query:     `rate(vm_rows_inserted_total[5m])`,
		Batching:  domain.BatchSettings{Enabled: false, Strategy: "manual"},
		Safety: domain.ExportSafetyConfig{
			Mode:              domain.ExportAdaptivityAutopilot,
			AutoSplit:         true,
			SplitByJob:        true,
			MinWindowSeconds:  10,
			MaxSplitDepth:     8,
			MaxStepSeconds:    300,
			StepLadderSeconds: []int{30, 60, 120, 300, 600},
		},
	}
	ApplyExportDefaults(&cfg)

	_, err := service.ExecuteExport(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected timeout failure at max autopilot step")
	}
	if !strings.Contains(err.Error(), "sampling at 300s") {
		t.Fatalf("expected max-step timeout error, got %v", err)
	}
	got := strings.Join(steps, ",")
	if got != "30s,60s,120s,300s" {
		t.Fatalf("expected retries to stop at 300s, got %v", steps)
	}
}

func TestAdaptiveRetryDoesNotAppendFailedPartialAttempt(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	var largeAttemptFailed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		rangeStart := time.UnixMilli(parseVMTime(t, r.URL.Query().Get("start")))
		rangeEnd := time.UnixMilli(parseVMTime(t, r.URL.Query().Get("end")))
		duration := rangeEnd.Sub(rangeStart)
		if duration == time.Hour && !largeAttemptFailed && rangeStart.Equal(start) {
			largeAttemptFailed = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []any{
						map[string]any{
							"metric": map[string]string{
								"__name__": "vm_rows_inserted_total",
								"job":      "vmagent",
							},
							"values": [][]any{{float64(rangeStart.Unix()), "1"}},
						},
					},
				},
			})
			return
		}
		if duration >= 2*time.Hour || (duration == time.Hour && rangeStart.Equal(start.Add(time.Hour))) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []any{
					map[string]any{
						"metric": map[string]string{
							"__name__": "vm_rows_inserted_total",
							"job":      "vmagent",
						},
						"values": [][]any{{float64(rangeStart.Unix()), "1"}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	outputDir := t.TempDir()
	service := NewExportService(outputDir, "test")
	cfg := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		TimeRange: domain.TimeRange{
			Start: start,
			End:   start.Add(2 * time.Hour),
		},
		Mode:      domain.ExportModeCustom,
		QueryType: domain.QueryModeMetricsQL,
		Query:     `rate(vm_rows_inserted_total[5m])`,
		Batching:  domain.BatchSettings{Enabled: false, Strategy: "manual"},
	}
	ApplyExportDefaults(&cfg)

	result, err := service.ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ExecuteExport failed: %v", err)
	}

	archiveReader, err := zip.OpenReader(result.ArchivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer func() { _ = archiveReader.Close() }()

	lines := 0
	for _, file := range archiveReader.File {
		if file.Name != "metrics.jsonl" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("failed to open metrics.jsonl: %v", err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("failed to read metrics.jsonl: %v", err)
		}
		lines = len(strings.Split(strings.TrimSpace(string(data)), "\n"))
	}
	if lines != 3 {
		t.Fatalf("expected only successful split output in archive, got %d lines", lines)
	}
}

func TestSplitByTimeFailureDoesNotAppendPartialSubSplit(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		rangeStart := time.UnixMilli(parseVMTime(t, r.URL.Query().Get("start")))
		rangeEnd := time.UnixMilli(parseVMTime(t, r.URL.Query().Get("end")))
		duration := rangeEnd.Sub(rangeStart)
		switch {
		case duration == 2*time.Hour:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`)
		case duration == time.Hour && rangeStart.Equal(start):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []any{
						map[string]any{
							"metric": map[string]string{
								"__name__": "vm_rows_inserted_total",
								"job":      "vmagent",
							},
							"values": [][]any{{float64(rangeStart.Unix()), "1"}},
						},
					},
				},
			})
		case duration == time.Hour && rangeStart.Equal(start.Add(time.Hour)):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `bad request`)
		default:
			http.Error(w, fmt.Sprintf("unexpected range %s - %s", rangeStart, rangeEnd), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	outputDir := t.TempDir()
	stagingFile := filepath.Join(outputDir, "failed-time-split.partial.jsonl")
	service := NewExportService(outputDir, "test")
	cfg := domain.ExportConfig{
		Connection: domain.VMConnection{URL: srv.URL},
		TimeRange: domain.TimeRange{
			Start: start,
			End:   start.Add(2 * time.Hour),
		},
		Mode:        domain.ExportModeCustom,
		QueryType:   domain.QueryModeMetricsQL,
		Query:       `rate(vm_rows_inserted_total[5m])`,
		Batching:    domain.BatchSettings{Enabled: false, Strategy: "manual"},
		StagingFile: stagingFile,
	}
	ApplyExportDefaults(&cfg)

	_, err := service.ExecuteExport(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected split failure")
	}
	data, readErr := os.ReadFile(stagingFile)
	if readErr != nil {
		t.Fatalf("failed to read staging file: %v", readErr)
	}
	if strings.TrimSpace(string(data)) != "" {
		t.Fatalf("expected failed time split to leave main staging empty, got %s", string(data))
	}
}

func readExportMetadataForTest(t *testing.T, archivePath string) exportMetadataForTest {
	t.Helper()
	archiveReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer func() { _ = archiveReader.Close() }()
	for _, file := range archiveReader.File {
		if file.Name != "metadata.json" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("failed to open metadata.json: %v", err)
		}
		var metadata exportMetadataForTest
		err = json.NewDecoder(rc).Decode(&metadata)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("failed to decode metadata.json: %v", err)
		}
		return metadata
	}
	t.Fatal("metadata.json not found")
	return exportMetadataForTest{}
}
