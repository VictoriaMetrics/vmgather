package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func runHealthcheck(cfg *TestConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	timeout := time.Duration(cfg.HealthcheckTimeout) * time.Second
	interval := time.Duration(cfg.HealthcheckInterval) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}

	httpClient := &http.Client{Timeout: defaultRequestTimeout}

	if err := waitForFreshVMAppVersion(httpClient, cfg.VMSingleNoAuth.URL, nil, timeout, interval); err != nil {
		return err
	}
	if err := waitForFreshVMAppVersion(httpClient, cfg.VMCluster.SelectTenant0, nil, timeout, interval); err != nil {
		return err
	}
	if err := waitForFreshVMAppVersion(httpClient, cfg.VMSelectStandalone.SelectTenant0, nil, timeout, interval); err != nil {
		return err
	}

	// vmauth-export-test: validate tenant 2022 (modern) credentials can query vm_app_version.
	modernAuth := &cfg.VMAuthExport.Modern
	modernAuth.Type = "basic"
	if err := waitForFreshVMAppVersion(httpClient, cfg.VMAuthExport.URL, modernAuth, timeout, interval); err != nil {
		return err
	}
	// Custom selector integration tests depend on vmagent having already scraped and remote-written
	// "test1" samples. Wait for that explicitly to avoid startup race flakes in CI.
	if err := waitForSelectorSeries(httpClient, cfg.VMSingleNoAuth.URL, `{job="test1"}`, timeout, interval); err != nil {
		return err
	}

	return nil
}

func waitForFreshVMAppVersion(httpClient *http.Client, endpoint string, auth *AuthConfig, timeout time.Duration, interval time.Duration) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint is empty")
	}

	fmt.Printf("[healthcheck] waiting for vm_app_version at %s\n", endpoint)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
		resp, _, err := doVMQuery(ctx, httpClient, endpoint, auth, "vm_app_version")
		cancel()
		if err == nil && len(resp.Data.Result) > 0 {
			if ts, ok := extractVMQueryTimestampSeconds(resp); ok && isFreshTimestamp(ts, time.Now()) {
				fmt.Printf("[healthcheck] vm_app_version available (fresh) at %s\n", endpoint)
				return nil
			}
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("vm_app_version not found within %s at %s", timeout, endpoint)
}

func waitForSelectorSeries(httpClient *http.Client, endpoint string, selector string, timeout time.Duration, interval time.Duration) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint is empty")
	}
	if selector == "" {
		return fmt.Errorf("selector is empty")
	}

	query := fmt.Sprintf("count(%s)", selector)
	fmt.Printf("[healthcheck] waiting for selector data (%s) at %s\n", selector, endpoint)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
		resp, _, err := doVMQuery(ctx, httpClient, endpoint, nil, query)
		cancel()
		if err == nil {
			if count, ok := extractVMQueryValueFloat(resp); ok && count > 0 {
				fmt.Printf("[healthcheck] selector data ready (%s count=%.0f) at %s\n", selector, count, endpoint)
				return nil
			}
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("selector data not found within %s for %s at %s", timeout, selector, endpoint)
}
