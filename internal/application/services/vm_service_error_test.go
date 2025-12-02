package services

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/domain"
)

// TestVMService_DiscoverComponents_NetworkError tests discovery with network errors
func TestVMService_DiscoverComponents_NetworkError(t *testing.T) {
	// Create server that closes connection immediately
	server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close connection without response
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server doesn't support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()

	service := NewVMService()
	conn := domain.VMConnection{
		URL: server.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := service.DiscoverComponents(ctx, conn, domain.TimeRange{
		Start: time.Now().Add(-1 * time.Hour),
		End:   time.Now(),
	})

	if err == nil {
		t.Error("expected error on network failure")
	}

	t.Logf("Got expected error: %v", err)
}

// TestVMService_DiscoverComponents_InvalidJSON tests discovery with invalid JSON response
func TestVMService_DiscoverComponents_InvalidJSON(t *testing.T) {
	server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	service := NewVMService()
	conn := domain.VMConnection{
		URL: server.URL,
	}

	ctx := context.Background()

	_, err := service.DiscoverComponents(ctx, conn, domain.TimeRange{
		Start: time.Now().Add(-1 * time.Hour),
		End:   time.Now(),
	})

	if err == nil {
		t.Error("expected error on invalid JSON")
	}

	t.Logf("Got expected error: %v", err)
}

// TestVMService_DiscoverComponents_HTTPError tests discovery with HTTP error codes
func TestVMService_DiscoverComponents_HTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"status":"error","errorType":"unauthorized","error":"invalid credentials"}`,
		},
		{
			name:       "forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"status":"error","errorType":"forbidden","error":"access denied"}`,
		},
		{
			name:       "not_found",
			statusCode: http.StatusNotFound,
			body:       `{"status":"error","errorType":"not_found","error":"endpoint not found"}`,
		},
		{
			name:       "internal_server_error",
			statusCode: http.StatusInternalServerError,
			body:       `{"status":"error","errorType":"internal","error":"internal server error"}`,
		},
		{
			name:       "service_unavailable",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"status":"error","errorType":"unavailable","error":"service temporarily unavailable"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			service := NewVMService()
			conn := domain.VMConnection{
				URL: server.URL,
			}

			ctx := context.Background()

			_, err := service.DiscoverComponents(ctx, conn, domain.TimeRange{
				Start: time.Now().Add(-1 * time.Hour),
				End:   time.Now(),
			})

			if err == nil {
				t.Errorf("expected error for HTTP %d", tt.statusCode)
			}

			t.Logf("Got expected error: %v", err)
		})
	}
}

// TestVMService_DiscoverComponents_ContextCancellation tests discovery with cancelled context
func TestVMService_DiscoverComponents_ContextCancellation(t *testing.T) {
	// Create slow server
	server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Slow response
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	service := NewVMService()
	conn := domain.VMConnection{
		URL: server.URL,
	}

	// Create context that cancels immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := service.DiscoverComponents(ctx, conn, domain.TimeRange{
		Start: time.Now().Add(-1 * time.Hour),
		End:   time.Now(),
	})

	if err == nil {
		t.Error("expected error on cancelled context")
	}

	t.Logf("Got expected error: %v", err)
}

// TestVMService_DiscoverComponents_EmptyResponse tests discovery with empty result set
func TestVMService_DiscoverComponents_EmptyResponse(t *testing.T) {
	server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	service := NewVMService()
	conn := domain.VMConnection{
		URL: server.URL,
	}

	ctx := context.Background()

	_, err := service.DiscoverComponents(ctx, conn, domain.TimeRange{
		Start: time.Now().Add(-1 * time.Hour),
		End:   time.Now(),
	})

	if err == nil {
		t.Error("expected error when no components discovered")
	}

	if err.Error() != "no VM components discovered" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestVMService_DiscoverComponents_Timeout tests discovery with timeout
func TestVMService_DiscoverComponents_Timeout(t *testing.T) {
	// Create slow server
	server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // Very slow response
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	service := NewVMService()
	conn := domain.VMConnection{
		URL: server.URL,
	}

	// Short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := service.DiscoverComponents(ctx, conn, domain.TimeRange{
		Start: time.Now().Add(-1 * time.Hour),
		End:   time.Now(),
	})
	duration := time.Since(start)

	if err == nil {
		t.Error("expected timeout error")
	}

	// Should timeout quickly (within 200ms)
	if duration > 200*time.Millisecond {
		t.Errorf("timeout took too long: %v", duration)
	}

	t.Logf("Timed out as expected in %v: %v", duration, err)
}

// TestVMService_GetSample_NetworkError tests sample retrieval with network errors
func TestVMService_GetSample_NetworkError(t *testing.T) {
	server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close connection
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server doesn't support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()

	service := NewVMService()
	config := domain.ExportConfig{
		Connection: domain.VMConnection{
			URL: server.URL,
		},
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		Jobs: []string{"test-job"},
	}

	ctx := context.Background()

	_, err := service.GetSample(ctx, config, 10)

	if err == nil {
		t.Error("expected error on network failure")
	}

	t.Logf("Got expected error: %v", err)
}

// TestVMService_ValidateConnection_InvalidURL tests validation with invalid URL
func TestVMService_ValidateConnection_InvalidURL(t *testing.T) {
	service := NewVMService()

	tests := []struct {
		name string
		url  string
	}{
		{"empty_url", ""},
		{"invalid_scheme", "ftp://invalid.com"},
		{"malformed_url", "://malformed"},
		{"nonexistent_host", "http://nonexistent-host-12345.invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := domain.VMConnection{
				URL: tt.url,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := service.ValidateConnection(ctx, conn)

			if err == nil {
				t.Errorf("expected error for invalid URL: %s", tt.url)
			}

			t.Logf("Got expected error: %v", err)
		})
	}
}
