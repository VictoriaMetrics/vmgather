package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type flakyRoundTripper struct {
	mu        sync.Mutex
	callCount int
}

func (rt *flakyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.callCount++
	call := rt.callCount
	rt.mu.Unlock()

	if call == 1 {
		return nil, io.EOF
	}
	now := time.Now().Unix()
	body := fmt.Sprintf(`{"status":"success","data":{"result":[{"value":[%d,"1"]}]}}`, now)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestDoVMQueryWithRetry_RetriesOnTransientError(t *testing.T) {
	rt := &flakyRoundTripper{}
	httpClient := &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: rt,
	}

	err := doVMQueryWithRetry(httpClient, "http://example.com", &AuthConfig{Type: "none"}, "vm_app_version", 2*time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	rt.mu.Lock()
	calls := rt.callCount
	rt.mu.Unlock()
	if calls < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", calls)
	}
}

type contextBlockingRoundTripper struct{}

func (rt *contextBlockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestDoVMQueryWithRetry_HonorsOverallTimeout(t *testing.T) {
	httpClient := &http.Client{
		Timeout:   700 * time.Millisecond,
		Transport: &contextBlockingRoundTripper{},
	}

	start := time.Now()
	err := doVMQueryWithRetry(
		httpClient,
		"http://example.com",
		&AuthConfig{Type: "none"},
		"vm_app_version",
		120*time.Millisecond,
		25*time.Millisecond,
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	// The loop should cap request timeout by remaining time and finish close to
	// caller timeout, not http client/default request timeout.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("retry loop exceeded caller timeout window: elapsed=%v", elapsed)
	}
}
