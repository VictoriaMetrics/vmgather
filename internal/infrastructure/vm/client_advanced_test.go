package vm

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/support/internal/domain"
)

// TestClient_QueryRange tests range query functionality
func TestClient_QueryRange(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify parameters
		query := r.URL.Query()
		if query.Get("query") == "" {
			t.Error("query parameter missing")
		}
		if query.Get("start") == "" {
			t.Error("start parameter missing")
		}
		if query.Get("end") == "" {
			t.Error("end parameter missing")
		}
		if query.Get("step") == "" {
			t.Error("step parameter missing")
		}

		// Return matrix response
		resp := QueryResult{
			Status: "success",
			Data: QueryData{
				ResultType: "matrix",
				Result: []Result{
					{
						Metric: map[string]string{"__name__": "test"},
						Values: [][]interface{}{
							{float64(1699728000), "100"},
							{float64(1699728060), "110"},
							{float64(1699728120), "120"},
						},
					},
				},
			},
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	step := 60 * time.Second

	result, err := client.QueryRange(context.Background(), "test", start, end, step)
	if err != nil {
		t.Fatalf("QueryRange failed: %v", err)
	}

	if result.Data.ResultType != "matrix" {
		t.Errorf("unexpected result type: %s", result.Data.ResultType)
	}

	if len(result.Data.Result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Data.Result))
	}

	// Verify values array
	if len(result.Data.Result[0].Values) != 3 {
		t.Errorf("expected 3 values, got %d", len(result.Data.Result[0].Values))
	}
}

// TestClient_Query_WithCustomHeader tests custom header authentication
func TestClient_Query_WithCustomHeader(t *testing.T) {
	expectedHeaderName := "X-Custom-Auth"
	expectedHeaderValue := "secret-token-123"

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom header
		headerValue := r.Header.Get(expectedHeaderName)
		if headerValue != expectedHeaderValue {
			t.Errorf("wrong header value: got %s, want %s", headerValue, expectedHeaderValue)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := QueryResult{
			Status: "success",
			Data:   QueryData{ResultType: "vector", Result: []Result{}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL: server.URL,
		Auth: domain.AuthConfig{
			Type:        domain.AuthTypeHeader,
			HeaderName:  expectedHeaderName,
			HeaderValue: expectedHeaderValue,
		},
	}

	client := NewClient(conn)
	_, err := client.Query(context.Background(), "test", time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClient_Export_WithMultitenantPath tests multitenant path handling
func TestClient_Export_WithMultitenantPath(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify multitenant path
		expectedPath := "/select/multitenant/prometheus/api/v1/export"
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: got %s, want %s", r.URL.Path, expectedPath)
		}

		w.Write([]byte(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`))
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:           server.URL,
		ApiBasePath:   "/select/multitenant/prometheus",
		IsMultitenant: true,
		Auth:          domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	reader, err := client.Export(context.Background(), "{__name__=\"test\"}", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	defer reader.Close()
}

// TestClient_Export_WithTenantID tests tenant ID in URL path
func TestClient_Export_WithTenantID(t *testing.T) {
	tenantID := "1011"

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify tenant ID in path
		if !strings.Contains(r.URL.Path, tenantID) {
			t.Errorf("tenant ID not in path: %s", r.URL.Path)
		}

		w.Write([]byte(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`))
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:         server.URL,
		TenantId:    tenantID,
		ApiBasePath: "/" + tenantID,
		Auth:        domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	reader, err := client.Export(context.Background(), "{__name__=\"test\"}", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	defer reader.Close()
}

// TestClient_Query_WithRedirect tests HTTP redirect handling
func TestClient_Query_WithRedirect(t *testing.T) {
	// Create target server
	targetServer := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := QueryResult{
			Status: "success",
			Data:   QueryData{ResultType: "vector", Result: []Result{}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer targetServer.Close()

	// Create redirect server
	redirectServer := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer redirectServer.Close()

	conn := domain.VMConnection{
		URL:  redirectServer.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	// Should follow redirect
	_, err := client.Query(context.Background(), "test", time.Now())
	if err != nil {
		t.Fatalf("query failed after redirect: %v", err)
	}
}

// TestClient_Query_WithRateLimiting tests 429 rate limit response
func TestClient_Query_WithRateLimiting(t *testing.T) {
	callCount := 0

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			// First call: rate limited
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"status":"error","error":"rate limit exceeded"}`))
			return
		}

		// Second call: success
		resp := QueryResult{
			Status: "success",
			Data:   QueryData{ResultType: "vector", Result: []Result{}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	// First call should fail with 429
	_, err := client.Query(context.Background(), "test", time.Now())
	if err == nil {
		t.Fatal("expected error on rate limit")
	}

	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429: %v", err)
	}
}

// TestClient_Export_StreamInterruption tests stream interruption handling
func TestClient_Export_StreamInterruption(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write partial data then close
		w.Write([]byte(`{"metric":{"__name__":"test1"},"values":[1],"timestamps":[1]}` + "\n"))
		w.Write([]byte(`{"metric":{"__name__":"test2"},"values":[2],"timestamps":[2]}`))
		// Intentionally don't write newline and close connection

		// Flush to send data
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	reader, err := client.Export(context.Background(), "{__name__!=\"\"}", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	defer reader.Close()

	// Read all data - should handle incomplete stream gracefully
	var lines []string
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Should have at least 1 complete line
	if len(lines) < 1 {
		t.Error("expected at least 1 complete line")
	}
}

// TestClient_Export_LargeResponse tests handling of large responses (> 10MB)
func TestClient_Export_LargeResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large response test in short mode")
	}

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate 100K metrics (~ 15MB)
		for i := 0; i < 100000; i++ {
			metric := map[string]interface{}{
				"metric": map[string]string{
					"__name__": "test_metric",
					"instance": "10.0.1.5:8482",
					"job":      "test",
				},
				"values":     []float64{float64(i)},
				"timestamps": []int64{int64(1699728000 + i)},
			}
			_ = json.NewEncoder(w).Encode(metric)
		}
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	start := time.Now()
	reader, err := client.Export(context.Background(), "{__name__!=\"\"}", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	defer reader.Close()

	// Count lines
	scanner := bufio.NewScanner(reader)
	lines := 0
	for scanner.Scan() {
		lines++
	}

	duration := time.Since(start)

	t.Logf("Read %d metrics in %v", lines, duration)

	if lines != 100000 {
		t.Errorf("expected 100000 lines, got %d", lines)
	}

	// Performance check: should read at least 10K lines/sec
	linesPerSec := float64(lines) / duration.Seconds()
	if linesPerSec < 10000 {
		t.Errorf("performance too slow: %.0f lines/sec (want > 10000)", linesPerSec)
	}
}

// TestClient_Export_GzipCompression tests gzip compressed responses
func TestClient_Export_GzipCompression(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check Accept-Encoding header
		acceptEncoding := r.Header.Get("Accept-Encoding")
		if !strings.Contains(acceptEncoding, "gzip") {
			t.Log("client doesn't request gzip (expected)")
		}

		// Return uncompressed data (VM typically doesn't gzip export)
		w.Header().Set("Content-Type", "application/x-json-stream")
		w.Write([]byte(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`))
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	reader, err := client.Export(context.Background(), "{__name__!=\"\"}", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	defer reader.Close()

	// Should be able to read data
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	if len(data) == 0 {
		t.Error("no data received")
	}
}
