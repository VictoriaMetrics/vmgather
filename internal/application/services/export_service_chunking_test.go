package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/domain"
	"github.com/VictoriaMetrics/VMGather/internal/infrastructure/vm"
)

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
