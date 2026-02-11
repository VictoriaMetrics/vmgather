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

type selectorCountRoundTripper struct {
	mu     sync.Mutex
	calls  int
	counts []string
}

func (rt *selectorCountRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.calls++
	call := rt.calls
	seq := rt.counts
	rt.mu.Unlock()

	count := "0"
	if len(seq) > 0 {
		idx := call - 1
		if idx >= len(seq) {
			idx = len(seq) - 1
		}
		count = seq[idx]
	}

	now := time.Now().Unix()
	body := fmt.Sprintf(`{"status":"success","data":{"result":[{"value":[%d,%q]}]}}`, now, count)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestWaitForSelectorSeries_SucceedsAfterDataAppears(t *testing.T) {
	rt := &selectorCountRoundTripper{
		counts: []string{"0", "2"},
	}
	httpClient := &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: rt,
	}

	err := waitForSelectorSeries(
		httpClient,
		"http://example.com",
		`{job="test1"}`,
		500*time.Millisecond,
		10*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected selector wait to succeed, got error: %v", err)
	}

	rt.mu.Lock()
	calls := rt.calls
	rt.mu.Unlock()
	if calls < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", calls)
	}
}

func TestWaitForSelectorSeries_FailsWhenDataNeverAppears(t *testing.T) {
	rt := &selectorCountRoundTripper{
		counts: []string{"0"},
	}
	httpClient := &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: rt,
	}

	err := waitForSelectorSeries(
		httpClient,
		"http://example.com",
		`{job="test1"}`,
		80*time.Millisecond,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected selector wait timeout error")
	}
	if !strings.Contains(err.Error(), "selector data not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractVMQueryValueFloat(t *testing.T) {
	tests := []struct {
		name string
		resp *vmQueryResponse
		want float64
		ok   bool
	}{
		{
			name: "string value",
			resp: &vmQueryResponse{Data: struct {
				Result []struct {
					Value []any "json:\"value\""
				} "json:\"result\""
			}{
				Result: []struct {
					Value []any "json:\"value\""
				}{{Value: []any{float64(1), "12"}}},
			}},
			want: 12,
			ok:   true,
		},
		{
			name: "float value",
			resp: &vmQueryResponse{Data: struct {
				Result []struct {
					Value []any "json:\"value\""
				} "json:\"result\""
			}{
				Result: []struct {
					Value []any "json:\"value\""
				}{{Value: []any{float64(1), float64(3)}}},
			}},
			want: 3,
			ok:   true,
		},
		{
			name: "missing value",
			resp: &vmQueryResponse{Data: struct {
				Result []struct {
					Value []any "json:\"value\""
				} "json:\"result\""
			}{
				Result: []struct {
					Value []any "json:\"value\""
				}{{Value: []any{float64(1)}}},
			}},
			ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractVMQueryValueFloat(tt.resp)
			if ok != tt.ok {
				t.Fatalf("unexpected ok=%v, want=%v", ok, tt.ok)
			}
			if tt.ok && got != tt.want {
				t.Fatalf("unexpected value=%v, want=%v", got, tt.want)
			}
		})
	}
}
