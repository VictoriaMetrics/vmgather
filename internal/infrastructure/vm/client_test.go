package vm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/domain"
)

// TestNewClient tests client creation
func TestNewClient(t *testing.T) {
	conn := domain.VMConnection{
		URL:  "http://vmselect:8481",
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if client.httpClient == nil {
		t.Error("expected non-nil HTTP client")
	}

	if client.conn.URL != conn.URL {
		t.Errorf("URL = %v, want %v", client.conn.URL, conn.URL)
	}
}

// TestClient_Query_Success tests successful query execution
func TestClient_Query_Success(t *testing.T) {
	// Create mock server
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request path
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify query parameter
		query := r.URL.Query().Get("query")
		if query == "" {
			t.Error("query parameter is empty")
		}

		// Return mock response
		resp := QueryResult{
			Status: "success",
			Data: QueryData{
				ResultType: "vector",
				Result: []Result{
					{
						Metric: map[string]string{
							"__name__": "vm_app_version",
							"version":  "vmsingle-v1.95.1",
						},
						Value: []interface{}{float64(1699728000), "1"},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}
	client := NewClient(conn)

	// Execute query
	result, err := client.Query(context.Background(), "vm_app_version", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify result
	if result.Status != "success" {
		t.Errorf("Status = %v, want success", result.Status)
	}

	if len(result.Data.Result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Data.Result))
	}

	if result.Data.Result[0].Metric["version"] != "vmsingle-v1.95.1" {
		t.Errorf("unexpected version: %s", result.Data.Result[0].Metric["version"])
	}
}

// TestClient_Query_WithBasicAuth tests query with Basic Auth
func TestClient_Query_WithBasicAuth(t *testing.T) {
	expectedUser := "testuser"
	expectedPass := "testpass"

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Error("basic auth not provided")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if user != expectedUser || pass != expectedPass {
			t.Errorf("wrong credentials: %s:%s", user, pass)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Return success
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
			Type:     domain.AuthTypeBasic,
			Username: expectedUser,
			Password: expectedPass,
		},
	}

	client := NewClient(conn)
	_, err := client.Query(context.Background(), "vm_app_version", time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClient_Query_WithBearerToken tests query with Bearer token
func TestClient_Query_WithBearerToken(t *testing.T) {
	expectedToken := "test-token-123"

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Authorization header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+expectedToken {
			t.Errorf("wrong authorization header: %s", auth)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Return success
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
			Type:  domain.AuthTypeBearer,
			Token: expectedToken,
		},
	}

	client := NewClient(conn)
	_, err := client.Query(context.Background(), "vm_app_version", time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClient_Query_Timeout tests query timeout
func TestClient_Query_Timeout(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	// Use short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.Query(ctx, "vm_app_version", time.Now())

	if err == nil {
		t.Fatal("expected timeout error")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestClient_Query_APIError tests API error response
func TestClient_Query_APIError(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := QueryResult{
			Status: "error",
			Error:  "invalid query syntax",
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)
	_, err := client.Query(context.Background(), "invalid{}", time.Now())

	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "invalid query syntax") {
		t.Errorf("error doesn't contain API message: %v", err)
	}
}

// TestClient_Export_Success tests successful export
func TestClient_Export_Success(t *testing.T) {
	// Mock JSONL response
	mockData := `{"metric":{"__name__":"vm_app_version"},"values":[1],"timestamps":[1699728000000]}
{"metric":{"__name__":"go_goroutines"},"values":[42],"timestamps":[1699728000000]}`

	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path
		if r.URL.Path != "/api/v1/export" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify method
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		// Return mock data
		w.Header().Set("Content-Type", "application/x-json-stream")
		w.Write([]byte(mockData))
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	// Execute export
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	reader, err := client.Export(context.Background(), "{__name__!=\"\"}", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer reader.Close()

	// Read data
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read data: %v", err)
	}

	// Verify data
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(lines))
	}

	// Parse first line to verify format
	var metric ExportedMetric
	if err := json.Unmarshal([]byte(lines[0]), &metric); err != nil {
		t.Errorf("failed to parse metric: %v", err)
	}

	if metric.Metric["__name__"] != "vm_app_version" {
		t.Errorf("unexpected metric name: %s", metric.Metric["__name__"])
	}
}

// TestClient_Export_HTTPError tests export with HTTP error
func TestClient_Export_HTTPError(t *testing.T) {
	server := newIPv4TestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	conn := domain.VMConnection{
		URL:  server.URL,
		Auth: domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	client := NewClient(conn)

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	_, err := client.Export(context.Background(), "{__name__!=\"\"}", start, end)

	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error doesn't mention status code: %v", err)
	}
}
