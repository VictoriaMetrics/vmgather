package vm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VictoriaMetrics/support/internal/domain"
)

// Client is a VictoriaMetrics API client
type Client struct {
	httpClient *http.Client
	conn       domain.VMConnection
}

// QueryResult represents Prometheus-compatible query response
type QueryResult struct {
	Status string    `json:"status"`
	Data   QueryData `json:"data"`
	Error  string    `json:"error,omitempty"`
}

// QueryData contains query result data
type QueryData struct {
	ResultType string   `json:"resultType"`
	Result     []Result `json:"result"`
}

// Result represents a single query result
type Result struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value,omitempty"`  // [timestamp, value]
	Values [][]interface{}   `json:"values,omitempty"` // [[timestamp, value], ...]
}

// ExportedMetric represents a metric in export format (JSONL)
type ExportedMetric struct {
	Metric     map[string]string `json:"metric"`
	Values     []interface{}     `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

// NewClient creates a new VictoriaMetrics client
func NewClient(conn domain.VMConnection) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	// Handle TLS verification skip
	if conn.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		conn: conn,
	}
}

// Query executes an instant PromQL query
func (c *Client) Query(ctx context.Context, query string, ts time.Time) (*QueryResult, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("query", query)
	params.Set("time", fmt.Sprintf("%d", ts.Unix()))

	// Build request
	req, err := c.buildRequest(ctx, http.MethodGet, "/api/v1/query", params)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result QueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check API status
	if result.Status != "success" {
		return nil, fmt.Errorf("API error: %s", result.Error)
	}

	return &result, nil
}

// QueryRange executes a range PromQL query
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*QueryResult, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", start.Unix()))
	params.Set("end", fmt.Sprintf("%d", end.Unix()))
	params.Set("step", fmt.Sprintf("%ds", int(step.Seconds())))

	// Build request
	req, err := c.buildRequest(ctx, http.MethodGet, "/api/v1/query_range", params)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result QueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check API status
	if result.Status != "success" {
		return nil, fmt.Errorf("API error: %s", result.Error)
	}

	return &result, nil
}

// Export executes metrics export via /api/v1/export endpoint
// Returns a reader for streaming JSONL data
func (c *Client) Export(ctx context.Context, selector string, start, end time.Time) (io.ReadCloser, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("match[]", selector)
	params.Set("start", start.Format(time.RFC3339))
	params.Set("end", end.Format(time.RFC3339))

	// Build request
	req, err := c.buildRequest(ctx, http.MethodPost, "/api/v1/export", params)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Set content type for POST
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("export request failed: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Return response body for streaming
	return resp.Body, nil
}

// buildRequest builds an HTTP request with authentication
func (c *Client) buildRequest(ctx context.Context, method, path string, params url.Values) (*http.Request, error) {
	// DEBUG: Log input parameters
	log.Printf("buildRequest called:")
	log.Printf("  Method: %s", method)
	log.Printf("  Path: %s", path)
	log.Printf("  Connection URL: %s", c.conn.URL)
	log.Printf("  Connection ApiBasePath: %s", c.conn.ApiBasePath)
	log.Printf("  Connection FullApiUrl: %s", c.conn.FullApiUrl)
	log.Printf("  Connection TenantId: %s", c.conn.TenantId)

	// Build URL
	// If FullApiUrl is provided, use it as base; otherwise use URL + ApiBasePath
	var baseURL string
	
	// CRITICAL: Detect if this is an /export request
	isExportRequest := strings.Contains(path, "/export")
	log.Printf("  Is Export Request: %v (path: %s)", isExportRequest, path)
	
	if c.conn.FullApiUrl != "" {
		baseURL = c.conn.FullApiUrl
		
		// CRITICAL FIX: /rw/prometheus works for /query but NOT for /export
		// VMAuth routes /rw/prometheus to write endpoints, which don't support /export
		// Convert /rw/prometheus → /prometheus ONLY for /export endpoint
		if isExportRequest && strings.Contains(baseURL, "/rw/prometheus") {
			originalURL := baseURL
			baseURL = strings.Replace(baseURL, "/rw/prometheus", "/prometheus", 1)
			log.Printf("  [WARN] FullApiUrl normalized for /export: %s -> %s", originalURL, baseURL)
		} else {
			log.Printf("  Using FullApiUrl as base: %s", baseURL)
		}
	} else if c.conn.ApiBasePath != "" {
		// NORMALIZE PATH: Support different customer URL formats
		// Some customers use:
		// - /rw/prometheus (write endpoint, works for /query but NOT for /export)
		// - /ui/prometheus (UI endpoint, works for API too)
		// - /prometheus (standard)
		normalizedPath := c.conn.ApiBasePath
		
		// CRITICAL FIX: /rw/prometheus works for /query but NOT for /export
		// VMAuth routes /rw/prometheus to write endpoints, which don't support /export
		// Convert /rw/prometheus → /prometheus ONLY for /export endpoint
		if isExportRequest && strings.Contains(normalizedPath, "/rw/prometheus") {
			originalPath := normalizedPath
			normalizedPath = strings.Replace(normalizedPath, "/rw/prometheus", "/prometheus", 1)
			log.Printf("  [WARN] ApiBasePath normalized for /export: %s -> %s", originalPath, normalizedPath)
		} else {
			log.Printf("  Using ApiBasePath as-is: %s", normalizedPath)
		}
		
		baseURL = c.conn.URL + normalizedPath
		log.Printf("  Using URL + ApiBasePath as base: %s", baseURL)
	} else {
		baseURL = c.conn.URL
		log.Printf("  Using URL as base: %s", baseURL)
	}

	// Append the API endpoint path
	reqURL := baseURL + path
	log.Printf("  Final request URL (before params): %s", reqURL)

	if len(params) > 0 {
		if method == http.MethodGet {
			reqURL += "?" + params.Encode()
		}
	}

	log.Printf("  [OK] Final request URL: %s", reqURL)

	// Create request
	var body io.Reader
	if method == http.MethodPost && params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		log.Printf("  [ERROR] Failed to create request: %v", err)
		return nil, err
	}

	// Apply authentication
	switch c.conn.Auth.Type {
	case domain.AuthTypeBasic:
		req.SetBasicAuth(c.conn.Auth.Username, c.conn.Auth.Password)
	case domain.AuthTypeBearer:
		req.Header.Set("Authorization", "Bearer "+c.conn.Auth.Token)
	case domain.AuthTypeHeader:
		req.Header.Set(c.conn.Auth.HeaderName, c.conn.Auth.HeaderValue)
	case domain.AuthTypeNone:
		// No authentication
	}

	return req, nil
}
