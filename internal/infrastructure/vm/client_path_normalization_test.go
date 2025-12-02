package vm

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VMGather/internal/domain"
)

// TestBuildRequest_PathNormalization tests the path normalization logic for /export endpoint
func TestBuildRequest_PathNormalization(t *testing.T) {
	tests := []struct {
		name           string
		connection     domain.VMConnection
		requestPath    string
		expectedURL    string
		shouldContain  string
		shouldNotContain string
	}{
		{
			name: "Export with /rw/prometheus in FullApiUrl - should normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:      "/api/v1/export",
			expectedURL:      "https://vm.example.com/1011/prometheus/api/v1/export",
			shouldContain:    "/prometheus/api/v1/export",
			shouldNotContain: "/rw/prometheus",
		},
		{
			name: "Query with /rw/prometheus in FullApiUrl - should NOT normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/query",
			expectedURL:   "https://vm.example.com/1011/rw/prometheus/api/v1/query",
			shouldContain: "/rw/prometheus/api/v1/query",
		},
		{
			name: "Export with /rw/prometheus in ApiBasePath - should normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				ApiBasePath: "/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:      "/api/v1/export",
			expectedURL:      "https://vm.example.com/1011/prometheus/api/v1/export",
			shouldContain:    "/prometheus/api/v1/export",
			shouldNotContain: "/rw/prometheus",
		},
		{
			name: "Query with /rw/prometheus in ApiBasePath - should NOT normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				ApiBasePath: "/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/query",
			expectedURL:   "https://vm.example.com/1011/rw/prometheus/api/v1/query",
			shouldContain: "/rw/prometheus/api/v1/query",
		},
		{
			name: "Export with standard /prometheus path - no normalization needed",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/export",
			expectedURL:   "https://vm.example.com/1011/prometheus/api/v1/export",
			shouldContain: "/prometheus/api/v1/export",
		},
		{
			name: "Export with /ui/prometheus path - no normalization",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/ui/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/export",
			expectedURL:   "https://vm.example.com/1011/ui/prometheus/api/v1/export",
			shouldContain: "/ui/prometheus/api/v1/export",
		},
		{
			name: "Export CSV with /rw/prometheus - should normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:      "/api/v1/export/csv",
			expectedURL:      "https://vm.example.com/1011/prometheus/api/v1/export/csv",
			shouldContain:    "/prometheus/api/v1/export/csv",
			shouldNotContain: "/rw/prometheus",
		},
		{
			name: "Export native with /rw/prometheus - should normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:      "/api/v1/export/native",
			expectedURL:      "https://vm.example.com/1011/prometheus/api/v1/export/native",
			shouldContain:    "/prometheus/api/v1/export/native",
			shouldNotContain: "/rw/prometheus",
		},
		{
			name: "Query range with /rw/prometheus - should NOT normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/query_range",
			expectedURL:   "https://vm.example.com/1011/rw/prometheus/api/v1/query_range",
			shouldContain: "/rw/prometheus/api/v1/query_range",
		},
		{
			name: "Series with /rw/prometheus - should NOT normalize",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/series",
			expectedURL:   "https://vm.example.com/1011/rw/prometheus/api/v1/series",
			shouldContain: "/rw/prometheus/api/v1/series",
		},
		{
			name: "Export with localhost - no normalization needed",
			connection: domain.VMConnection{
				URL: "http://localhost:8428",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/export",
			expectedURL:   "http://localhost:8428/api/v1/export",
			shouldContain: "localhost:8428/api/v1/export",
		},
		{
			name: "Export with cluster path - no normalization needed",
			connection: domain.VMConnection{
				URL:         "http://vmselect:8481",
				ApiBasePath: "/select/0/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath:   "/api/v1/export",
			expectedURL:   "http://vmselect:8481/select/0/prometheus/api/v1/export",
			shouldContain: "/select/0/prometheus/api/v1/export",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.connection)

			// Build request
			req, err := client.buildRequest(context.Background(), "GET", tt.requestPath, url.Values{})
			if err != nil {
				t.Fatalf("buildRequest() error = %v", err)
			}

			// Check URL
			gotURL := req.URL.String()
			if gotURL != tt.expectedURL {
				t.Errorf("buildRequest() URL = %v, want %v", gotURL, tt.expectedURL)
			}

			// Check shouldContain
			if tt.shouldContain != "" && !strings.Contains(gotURL, tt.shouldContain) {
				t.Errorf("buildRequest() URL should contain %q, got %v", tt.shouldContain, gotURL)
			}

			// Check shouldNotContain
			if tt.shouldNotContain != "" && strings.Contains(gotURL, tt.shouldNotContain) {
				t.Errorf("buildRequest() URL should NOT contain %q, got %v", tt.shouldNotContain, gotURL)
			}
		})
	}
}

// TestBuildRequest_PathNormalization_EdgeCases tests edge cases for path normalization
func TestBuildRequest_PathNormalization_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		connection  domain.VMConnection
		requestPath string
		expectedURL string
	}{
		{
			name: "Multiple /rw/prometheus occurrences - only first should be replaced",
			connection: domain.VMConnection{
				URL:         "https://example.com",
				FullApiUrl:  "https://example.com/rw/prometheus/rw/prometheus",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath: "/api/v1/export",
			expectedURL: "https://example.com/prometheus/rw/prometheus/api/v1/export",
		},
		{
			name: "Empty FullApiUrl and ApiBasePath - use base URL",
			connection: domain.VMConnection{
				URL: "http://localhost:8428",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath: "/api/v1/export",
			expectedURL: "http://localhost:8428/api/v1/export",
		},
		{
			name: "Path with trailing slash",
			connection: domain.VMConnection{
				URL:         "https://example.com",
				FullApiUrl:  "https://example.com/1011/rw/prometheus/",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath: "/api/v1/export",
			expectedURL: "https://example.com/1011/prometheus//api/v1/export",
		},
		{
			name: "Case sensitivity - should match /rw/prometheus exactly",
			connection: domain.VMConnection{
				URL:         "https://example.com",
				FullApiUrl:  "https://example.com/1011/RW/PROMETHEUS",
				Auth: domain.AuthConfig{
					Type: domain.AuthTypeNone,
				},
			},
			requestPath: "/api/v1/export",
			// Should NOT normalize because case doesn't match
			expectedURL: "https://example.com/1011/RW/PROMETHEUS/api/v1/export",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.connection)

			req, err := client.buildRequest(context.Background(), "GET", tt.requestPath, url.Values{})
			if err != nil {
				t.Fatalf("buildRequest() error = %v", err)
			}

			gotURL := req.URL.String()
			if gotURL != tt.expectedURL {
				t.Errorf("buildRequest() URL = %v, want %v", gotURL, tt.expectedURL)
			}
		})
	}
}

// TestBuildRequest_Authentication tests that authentication is preserved after normalization
func TestBuildRequest_Authentication(t *testing.T) {
	tests := []struct {
		name       string
		connection domain.VMConnection
		wantHeader string
		wantValue  string
	}{
		{
			name: "Basic auth preserved after normalization",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type:     domain.AuthTypeBasic,
					Username: "test-user",
					Password: "test-pass",
				},
			},
			wantHeader: "Authorization",
			wantValue:  "Basic", // Should start with "Basic"
		},
		{
			name: "Bearer token preserved after normalization",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type:  domain.AuthTypeBearer,
					Token: "test-token-123",
				},
			},
			wantHeader: "Authorization",
			wantValue:  "Bearer test-token-123",
		},
		{
			name: "Custom header preserved after normalization",
			connection: domain.VMConnection{
				URL:         "https://vm.example.com",
				FullApiUrl:  "https://vm.example.com/1011/rw/prometheus",
				Auth: domain.AuthConfig{
					Type:        domain.AuthTypeHeader,
					HeaderName:  "X-Custom-Auth",
					HeaderValue: "custom-value",
				},
			},
			wantHeader: "X-Custom-Auth",
			wantValue:  "custom-value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.connection)

			req, err := client.buildRequest(context.Background(), "GET", "/api/v1/export", url.Values{})
			if err != nil {
				t.Fatalf("buildRequest() error = %v", err)
			}

			// Check authentication header
			gotValue := req.Header.Get(tt.wantHeader)
			if tt.connection.Auth.Type == domain.AuthTypeBasic {
				// For Basic auth, just check it starts with "Basic"
				if !strings.HasPrefix(gotValue, tt.wantValue) {
					t.Errorf("buildRequest() header %q = %v, want to start with %v", tt.wantHeader, gotValue, tt.wantValue)
				}
			} else {
				if gotValue != tt.wantValue {
					t.Errorf("buildRequest() header %q = %v, want %v", tt.wantHeader, gotValue, tt.wantValue)
				}
			}

			// Verify URL was normalized
			gotURL := req.URL.String()
			if strings.Contains(gotURL, "/rw/prometheus") {
				t.Errorf("buildRequest() URL still contains /rw/prometheus after normalization: %v", gotURL)
			}
			if !strings.Contains(gotURL, "/prometheus/api/v1/export") {
				t.Errorf("buildRequest() URL doesn't contain expected normalized path: %v", gotURL)
			}
		})
	}
}

