package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	url := flag.String("url", "", "health endpoint URL")
	timeout := flag.Duration("timeout", 2*time.Second, "request timeout")
	expectedStatus := flag.Int("status", http.StatusOK, "expected HTTP status code")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "healthcheck: -url is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: build request: %v\n", err)
		os.Exit(1)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: request failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != *expectedStatus {
		fmt.Fprintf(os.Stderr, "healthcheck: unexpected status %d (expected %d)\n", resp.StatusCode, *expectedStatus)
		os.Exit(1)
	}
}
