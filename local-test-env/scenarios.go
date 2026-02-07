package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type scenario struct {
	name     string
	endpoint string
	auth     *AuthConfig
}

func runScenarios(cfg *TestConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}

	scenarios := scenariosFromConfig(cfg)
	if len(scenarios) == 0 {
		return fmt.Errorf("no scenarios configured")
	}

	fmt.Println("===============================================================================")
	fmt.Println("VMGather - Comprehensive Scenario Testing (Go)")
	fmt.Println("===============================================================================")
	fmt.Println()
	fmt.Println("[INFO] Waiting for services to be ready...")
	time.Sleep(5 * time.Second)
	fmt.Println()

	httpClient := &http.Client{Timeout: defaultRequestTimeout}

	passed := 0
	failed := 0
	total := 0

	for _, sc := range scenarios {
		total++
		fmt.Printf("[%d] Testing: %s\n", total, sc.name)
		fmt.Printf("    URL: %s\n", sc.endpoint)

		ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
		_, _, err := doVMQuery(ctx, httpClient, sc.endpoint, sc.auth, "vm_app_version")
		cancel()
		if err != nil {
			fmt.Printf("    [FAIL] %v\n\n", err)
			failed++
			continue
		}

		fmt.Printf("    [PASS]\n\n")
		passed++
	}

	fmt.Println("===============================================================================")
	fmt.Println("Test Summary")
	fmt.Println("===============================================================================")
	fmt.Println()
	fmt.Printf("Total Tests:  %d\n", total)
	fmt.Printf("Passed:       %d\n", passed)
	fmt.Printf("Failed:       %d\n", failed)
	fmt.Println()

	if failed > 0 {
		return fmt.Errorf("%d scenario(s) failed", failed)
	}
	return nil
}

func scenariosFromConfig(cfg *TestConfig) []scenario {
	vmauthURL := cfg.VMAuthCluster.URL
	none := &AuthConfig{Type: "none"}
	vmsingleBasic := &AuthConfig{
		Type:     "basic",
		Username: cfg.VMSingleAuth.Auth.Username,
		Password: cfg.VMSingleAuth.Auth.Password,
	}
	vmsingleBearer := &AuthConfig{Type: "bearer", Token: cfg.TestBearerToken}

	vmauthTenant0 := &AuthConfig{
		Type:     "basic",
		Username: cfg.VMAuthCluster.Tenant0.Username,
		Password: cfg.VMAuthCluster.Tenant0.Password,
	}
	vmauthTenant1011 := &AuthConfig{
		Type:     "basic",
		Username: cfg.VMAuthCluster.Tenant1011.Username,
		Password: cfg.VMAuthCluster.Tenant1011.Password,
	}
	vmauthMultitenant := &AuthConfig{
		Type:     "basic",
		Username: cfg.VMAuthCluster.Multitenant.Username,
		Password: cfg.VMAuthCluster.Multitenant.Password,
	}
	vmauthBearerTenant0 := &AuthConfig{Type: "bearer", Token: cfg.TestBearerTokenCluster}
	vmauthBearerCustom := &AuthConfig{Type: "bearer", Token: cfg.TestBearerTokenCustom}

	return []scenario{
		{
			name:     "VMSingle No Auth",
			endpoint: cfg.VMSingleNoAuth.URL,
			auth:     none,
		},
		{
			name:     "VMSingle via VMAuth Basic",
			endpoint: cfg.VMSingleAuth.URL,
			auth:     vmsingleBasic,
		},
		{
			name:     "VMSingle Bearer Token",
			endpoint: cfg.VMSingleAuth.URL,
			auth:     vmsingleBearer,
		},
		{
			name:     "Cluster No Auth - Tenant 0",
			endpoint: cfg.VMCluster.SelectTenant0,
			auth:     none,
		},
		{
			name:     "Cluster No Auth - Tenant 1011",
			endpoint: cfg.VMCluster.SelectTenant1011,
			auth:     none,
		},
		{
			name:     "Cluster No Auth - Multitenant",
			endpoint: cfg.VMCluster.SelectMultitenant,
			auth:     none,
		},
		{
			name:     "VMSelect standalone - Tenant 0",
			endpoint: cfg.VMSelectStandalone.SelectTenant0,
			auth:     none,
		},
		{
			name:     "Cluster via VMAuth - Tenant 0",
			endpoint: vmauthURL,
			auth:     vmauthTenant0,
		},
		{
			name:     "Cluster via VMAuth - Tenant 1011",
			endpoint: vmauthURL,
			auth:     vmauthTenant1011,
		},
		{
			name:     "Cluster via VMAuth - Multitenant",
			endpoint: vmauthURL,
			auth:     vmauthMultitenant,
		},
		{
			name:     "Cluster Bearer Token",
			endpoint: vmauthURL,
			auth:     vmauthBearerTenant0,
		},
		{
			name:     "Cluster Custom Header",
			endpoint: vmauthURL,
			auth:     vmauthBearerCustom,
		},
		{
			name:     "Full Grafana-like URL",
			endpoint: cfg.VMCluster.SelectTenant1011,
			auth:     none,
		},
		{
			name:     "VMAuth Auto-routing",
			endpoint: vmauthURL,
			auth:     vmauthTenant0,
		},
	}
}
