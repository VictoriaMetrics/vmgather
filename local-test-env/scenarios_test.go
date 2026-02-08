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
