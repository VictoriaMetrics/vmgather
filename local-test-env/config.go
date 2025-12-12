package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// TestConfig holds all test environment URLs and credentials
type TestConfig struct {
	// vmgather application
	VMGatherURL string `json:"vmgather_url"`

	// VMSingle instances
	VMSingleNoAuth VMEndpoint `json:"vm_single_noauth"`
	VMSingleAuth   VMEndpoint `json:"vm_single_auth"`

	// VMCluster instances
	VMCluster VMClusterConfig `json:"vm_cluster"`

	// VMAuth proxies
	VMAuthCluster VMAuthConfig `json:"vmauth_cluster"`

	// Export testing
	VMAuthExport VMAuthExportConfig `json:"vmauth_export"`

	// Test tokens
	TestBearerToken        string `json:"test_bearer_token"`
	TestBearerTokenCluster string `json:"test_bearer_token_cluster"`
	TestBearerTokenCustom  string `json:"test_bearer_token_custom"`

	// Timeouts
	TestTimeout         int `json:"test_timeout"`
	HealthcheckTimeout  int `json:"healthcheck_timeout"`
	HealthcheckInterval int `json:"healthcheck_interval"`
}

// VMEndpoint represents a single VM instance
type VMEndpoint struct {
	URL  string      `json:"url"`
	Auth *AuthConfig `json:"auth,omitempty"`
}

// VMClusterConfig holds cluster configuration
type VMClusterConfig struct {
	BaseURL           string `json:"base_url"`
	SelectTenant0     string `json:"select_tenant_0"`
	SelectTenant1011  string `json:"select_tenant_1011"`
	SelectMultitenant string `json:"select_multitenant"`
}

// VMAuthConfig holds VMAuth proxy configuration
type VMAuthConfig struct {
	URL         string     `json:"url"`
	Tenant0     AuthConfig `json:"tenant_0"`
	Tenant1011  AuthConfig `json:"tenant_1011"`
	Multitenant AuthConfig `json:"multitenant"`
}

// VMAuthExportConfig holds export testing configuration
type VMAuthExportConfig struct {
	URL    string     `json:"url"`
	Legacy AuthConfig `json:"legacy"`
	Modern AuthConfig `json:"modern"`
}

// AuthConfig holds authentication credentials
type AuthConfig struct {
	Type     string `json:"type"` // "basic", "bearer", "header", "none"
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

// DefaultConfig returns default test configuration
func DefaultConfig() *TestConfig {
	// Use localhost by default, can be overridden via env vars
	host := "localhost"

	return &TestConfig{
		VMGatherURL: getEnvOrDefault("VMGATHER_URL", fmt.Sprintf("http://%s:8080", host)),

		VMSingleNoAuth: VMEndpoint{
			URL: getEnvOrDefault("VM_SINGLE_NOAUTH_URL", fmt.Sprintf("http://%s:18428", host)),
		},
		VMSingleAuth: VMEndpoint{
			URL: getEnvOrDefault("VM_SINGLE_AUTH_URL", fmt.Sprintf("http://%s:8427", host)),
			Auth: &AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_SINGLE_AUTH_USER", "monitoring-read"),
				Password: getEnvOrDefault("VM_SINGLE_AUTH_PASS", "secret-password-123"),
			},
		},

		VMCluster: VMClusterConfig{
			BaseURL:           getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:8481", host)),
			SelectTenant0:     getEnvOrDefault("VM_CLUSTER_SELECT_TENANT_0", fmt.Sprintf("%s/select/0/prometheus", getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:8481", host)))),
			SelectTenant1011:  getEnvOrDefault("VM_CLUSTER_SELECT_TENANT_1011", fmt.Sprintf("%s/select/1011/prometheus", getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:8481", host)))),
			SelectMultitenant: getEnvOrDefault("VM_CLUSTER_SELECT_MULTITENANT", fmt.Sprintf("%s/select/multitenant/prometheus", getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:8481", host)))),
		},

		VMAuthCluster: VMAuthConfig{
			URL: getEnvOrDefault("VM_AUTH_CLUSTER_URL", fmt.Sprintf("http://%s:8426", host)),
			Tenant0: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_TENANT_0_USER", "tenant0-user"),
				Password: getEnvOrDefault("VM_AUTH_TENANT_0_PASS", "tenant0-pass"),
			},
			Tenant1011: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_TENANT_1011_USER", "tenant1011-user"),
				Password: getEnvOrDefault("VM_AUTH_TENANT_1011_PASS", "tenant1011-pass"),
			},
			Multitenant: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_MULTITENANT_USER", "admin-multitenant"),
				Password: getEnvOrDefault("VM_AUTH_MULTITENANT_PASS", "admin-multi-pass"),
			},
		},

		VMAuthExport: VMAuthExportConfig{
			URL: getEnvOrDefault("VM_AUTH_EXPORT_URL", fmt.Sprintf("http://%s:8425", host)),
			Legacy: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_EXPORT_LEGACY_USER", "tenant1011-legacy"),
				Password: getEnvOrDefault("VM_AUTH_EXPORT_LEGACY_PASS", "password"),
			},
			Modern: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_EXPORT_MODERN_USER", "tenant2022-modern"),
				Password: getEnvOrDefault("VM_AUTH_EXPORT_MODERN_PASS", "password"),
			},
		},

		TestBearerToken:        getEnvOrDefault("TEST_BEARER_TOKEN", "test-bearer-token-789"),
		TestBearerTokenCluster: getEnvOrDefault("TEST_BEARER_TOKEN_CLUSTER", "bearer-tenant0-token"),
		TestBearerTokenCustom:  getEnvOrDefault("TEST_BEARER_TOKEN_CUSTOM", "custom-header-token-1011"),

		TestTimeout:         getEnvIntOrDefault("TEST_TIMEOUT", 30),
		HealthcheckTimeout:  getEnvIntOrDefault("HEALTHCHECK_TIMEOUT", 60),
		HealthcheckInterval: getEnvIntOrDefault("HEALTHCHECK_INTERVAL", 3),
	}
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *TestConfig {
	return DefaultConfig()
}

// ToJSON exports configuration as JSON for use in other tools
func (c *TestConfig) ToJSON() (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ToEnv exports configuration as environment variables (shell format)
func (c *TestConfig) ToEnv() string {
	return fmt.Sprintf(`# Generated test configuration
export VMGATHER_URL=%q
export VM_SINGLE_NOAUTH_URL=%q
export VM_SINGLE_AUTH_URL=%q
export VM_SINGLE_AUTH_USER=%q
export VM_SINGLE_AUTH_PASS=%q
export VM_CLUSTER_URL=%q
export VM_CLUSTER_SELECT_TENANT_0=%q
export VM_CLUSTER_SELECT_TENANT_1011=%q
export VM_CLUSTER_SELECT_MULTITENANT=%q
export VM_AUTH_CLUSTER_URL=%q
export VM_AUTH_TENANT_0_USER=%q
export VM_AUTH_TENANT_0_PASS=%q
export VM_AUTH_TENANT_1011_USER=%q
export VM_AUTH_TENANT_1011_PASS=%q
export VM_AUTH_MULTITENANT_USER=%q
export VM_AUTH_MULTITENANT_PASS=%q
export VM_AUTH_EXPORT_URL=%q
export VM_AUTH_EXPORT_LEGACY_USER=%q
export VM_AUTH_EXPORT_LEGACY_PASS=%q
export VM_AUTH_EXPORT_MODERN_USER=%q
export VM_AUTH_EXPORT_MODERN_PASS=%q
export TEST_BEARER_TOKEN=%q
export TEST_BEARER_TOKEN_CLUSTER=%q
export TEST_BEARER_TOKEN_CUSTOM=%q
export TEST_TIMEOUT=%d
export HEALTHCHECK_TIMEOUT=%d
export HEALTHCHECK_INTERVAL=%d
`,
		c.VMGatherURL,
		c.VMSingleNoAuth.URL,
		c.VMSingleAuth.URL,
		c.VMSingleAuth.Auth.Username,
		c.VMSingleAuth.Auth.Password,
		c.VMCluster.BaseURL,
		c.VMCluster.SelectTenant0,
		c.VMCluster.SelectTenant1011,
		c.VMCluster.SelectMultitenant,
		c.VMAuthCluster.URL,
		c.VMAuthCluster.Tenant0.Username,
		c.VMAuthCluster.Tenant0.Password,
		c.VMAuthCluster.Tenant1011.Username,
		c.VMAuthCluster.Tenant1011.Password,
		c.VMAuthCluster.Multitenant.Username,
		c.VMAuthCluster.Multitenant.Password,
		c.VMAuthExport.URL,
		c.VMAuthExport.Legacy.Username,
		c.VMAuthExport.Legacy.Password,
		c.VMAuthExport.Modern.Username,
		c.VMAuthExport.Modern.Password,
		c.TestBearerToken,
		c.TestBearerTokenCluster,
		c.TestBearerTokenCustom,
		c.TestTimeout,
		c.HealthcheckTimeout,
		c.HealthcheckInterval,
	)
}

// Helper functions
func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// Main function for CLI usage
func main() {
	config := LoadConfig()

	// Support different output formats
	format := "env"
	if len(os.Args) > 1 {
		format = os.Args[1]
	}

	switch format {
	case "json":
		jsonStr, err := config.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(jsonStr)
	case "env":
		fmt.Println(config.ToEnv())
	case "validate":
		// Validate configuration and exit with proper code
		if err := validateConfig(config); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Configuration validation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[OK] Configuration is valid")
	default:
		fmt.Fprintf(os.Stderr, "Usage: %s [json|env|validate]\n", os.Args[0])
		os.Exit(1)
	}
}

func validateConfig(c *TestConfig) error {
	if c.VMGatherURL == "" {
		return fmt.Errorf("VMGATHER_URL is required")
	}
	if c.VMSingleNoAuth.URL == "" {
		return fmt.Errorf("VM_SINGLE_NOAUTH_URL is required")
	}
	return nil
}
