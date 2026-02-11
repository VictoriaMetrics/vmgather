package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	fallbackPortRangeStart = 20000
	fallbackPortRangeEnd   = 45000
	defaultTestHost        = "127.0.0.1"
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
	// Standalone vmselect scenario (single storage node)
	VMSelectStandalone VMSelectStandaloneConfig `json:"vmselect_standalone"`

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

// VMSelectStandaloneConfig holds standalone vmselect configuration
type VMSelectStandaloneConfig struct {
	BaseURL       string `json:"base_url"`
	SelectTenant0 string `json:"select_tenant_0"`
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
	loadEnvFileIfExists(getEnvOrDefault("VMGATHER_ENV_FILE", ".env.dynamic"))
	// Use explicit IPv4 loopback by default to avoid ::1 resolution differences across hosts.
	host := getEnvOrDefault("VM_TEST_HOST", defaultTestHost)
	vmGatherPort := getEnvIntOrDefault("VMGATHER_PORT", 8080)
	vmSingleNoAuthPort := getEnvIntOrDefault("VM_SINGLE_NOAUTH_PORT", 18428)
	vmAuthSinglePort := getEnvIntOrDefault("VM_AUTH_SINGLE_PORT", 8427)
	vmSelectPort := getEnvIntOrDefault("VM_SELECT_PORT", 8481)
	vmAuthClusterPort := getEnvIntOrDefault("VM_AUTH_CLUSTER_PORT", 8426)
	vmAuthExportPort := getEnvIntOrDefault("VM_AUTH_EXPORT_PORT", 8425)
	vmSelectStandalonePort := getEnvIntOrDefault("VMSELECT_STANDALONE_PORT", 8491)

	return &TestConfig{
		VMGatherURL: getEnvOrDefault("VMGATHER_URL", fmt.Sprintf("http://%s:%d", host, vmGatherPort)),

		VMSingleNoAuth: VMEndpoint{
			URL: getEnvOrDefault("VM_SINGLE_NOAUTH_URL", fmt.Sprintf("http://%s:%d", host, vmSingleNoAuthPort)),
		},
		VMSingleAuth: VMEndpoint{
			URL: getEnvOrDefault("VM_SINGLE_AUTH_URL", fmt.Sprintf("http://%s:%d", host, vmAuthSinglePort)),
			Auth: &AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_SINGLE_AUTH_USER", "monitoring-read"),
				Password: getEnvOrDefault("VM_SINGLE_AUTH_PASS", "secret-password-123"),
			},
		},

		VMCluster: VMClusterConfig{
			BaseURL:           getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:%d", host, vmSelectPort)),
			SelectTenant0:     getEnvOrDefault("VM_CLUSTER_SELECT_TENANT_0", fmt.Sprintf("%s/select/0/prometheus", getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:%d", host, vmSelectPort)))),
			SelectTenant1011:  getEnvOrDefault("VM_CLUSTER_SELECT_TENANT_1011", fmt.Sprintf("%s/select/1011/prometheus", getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:%d", host, vmSelectPort)))),
			SelectMultitenant: getEnvOrDefault("VM_CLUSTER_SELECT_MULTITENANT", fmt.Sprintf("%s/select/multitenant/prometheus", getEnvOrDefault("VM_CLUSTER_URL", fmt.Sprintf("http://%s:%d", host, vmSelectPort)))),
		},
		VMSelectStandalone: VMSelectStandaloneConfig{
			BaseURL:       getEnvOrDefault("VMSELECT_STANDALONE_URL", fmt.Sprintf("http://%s:%d", host, vmSelectStandalonePort)),
			SelectTenant0: getEnvOrDefault("VMSELECT_STANDALONE_SELECT_TENANT_0", fmt.Sprintf("%s/select/0/prometheus", getEnvOrDefault("VMSELECT_STANDALONE_URL", fmt.Sprintf("http://%s:%d", host, vmSelectStandalonePort)))),
		},

		VMAuthCluster: VMAuthConfig{
			URL: getEnvOrDefault("VM_AUTH_CLUSTER_URL", fmt.Sprintf("http://%s:%d", host, vmAuthClusterPort)),
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
			URL: getEnvOrDefault("VM_AUTH_EXPORT_URL", fmt.Sprintf("http://%s:%d", host, vmAuthExportPort)),
			Legacy: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_EXPORT_LEGACY_USER", "tenant1011-legacy"),
				Password: getEnvOrDefault("VM_AUTH_EXPORT_LEGACY_PASS", "legacy-pass-1011"),
			},
			Modern: AuthConfig{
				Type:     "basic",
				Username: getEnvOrDefault("VM_AUTH_EXPORT_MODERN_USER", "tenant2022-modern"),
				Password: getEnvOrDefault("VM_AUTH_EXPORT_MODERN_PASS", "modern-pass-2022"),
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
	vmGatherPort := getEnvIntOrDefault("VMGATHER_PORT", 8080)
	vmGatherAddr := getEnvOrDefault("VMGATHER_ADDR", fmt.Sprintf("%s:%d", defaultTestHost, vmGatherPort))
	if parsed, err := url.Parse(c.VMGatherURL); err == nil && parsed.Host != "" {
		vmGatherAddr = parsed.Host
	}

	return fmt.Sprintf(`# Generated test configuration
export VMGATHER_PORT=%d
export VMGATHER_ADDR=%q
export VM_SINGLE_NOAUTH_PORT=%d
export VM_SINGLE_AUTH_PORT=%d
export VM_AUTH_SINGLE_PORT=%d
export VM_SELECT_PORT=%d
export VM_AUTH_CLUSTER_PORT=%d
export VM_AUTH_EXPORT_PORT=%d
export NGINX_PORT=%d
export VMSELECT_STANDALONE_PORT=%d
export VMGATHER_URL=%q
export VM_SINGLE_NOAUTH_URL=%q
export VM_SINGLE_AUTH_URL=%q
export VM_SINGLE_AUTH_USER=%q
export VM_SINGLE_AUTH_PASS=%q
export VM_CLUSTER_URL=%q
export VM_CLUSTER_SELECT_TENANT_0=%q
export VM_CLUSTER_SELECT_TENANT_1011=%q
export VM_CLUSTER_SELECT_MULTITENANT=%q
export VMSELECT_STANDALONE_URL=%q
export VMSELECT_STANDALONE_SELECT_TENANT_0=%q
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
		vmGatherPort,
		vmGatherAddr,
		getEnvIntOrDefault("VM_SINGLE_NOAUTH_PORT", 18428),
		getEnvIntOrDefault("VM_SINGLE_AUTH_PORT", 18429),
		getEnvIntOrDefault("VM_AUTH_SINGLE_PORT", 8427),
		getEnvIntOrDefault("VM_SELECT_PORT", 8481),
		getEnvIntOrDefault("VM_AUTH_CLUSTER_PORT", 8426),
		getEnvIntOrDefault("VM_AUTH_EXPORT_PORT", 8425),
		getEnvIntOrDefault("NGINX_PORT", 8888),
		getEnvIntOrDefault("VMSELECT_STANDALONE_PORT", 8491),
		c.VMGatherURL,
		c.VMSingleNoAuth.URL,
		c.VMSingleAuth.URL,
		c.VMSingleAuth.Auth.Username,
		c.VMSingleAuth.Auth.Password,
		c.VMCluster.BaseURL,
		c.VMCluster.SelectTenant0,
		c.VMCluster.SelectTenant1011,
		c.VMCluster.SelectMultitenant,
		c.VMSelectStandalone.BaseURL,
		c.VMSelectStandalone.SelectTenant0,
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
	case "bootstrap":
		envFile := getEnvOrDefault("VMGATHER_ENV_FILE", ".env.dynamic")
		if err := bootstrapEnvFile(envFile); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Failed to bootstrap env file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[OK] Wrote %s\n", envFile)
	case "project":
		action := "get"
		if len(os.Args) > 2 {
			action = strings.TrimSpace(os.Args[2])
		}
		reset := action == "reset" || action == "new"
		name, err := getOrCreateProjectName(reset)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		fmt.Println(name)
	case "healthcheck":
		if err := runHealthcheck(config); err != nil {
			fmt.Fprintf(os.Stderr, "[healthcheck] ERROR: %v\n", err)
			os.Exit(1)
		}
	case "json":
		jsonStr, err := config.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(jsonStr)
	case "env":
		fmt.Println(config.ToEnv())
	case "scenarios":
		if err := runScenarios(config); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[OK] All scenarios passed")
	case "validate":
		// Validate configuration and exit with proper code
		if err := validateConfig(config); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Configuration validation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[OK] Configuration is valid")
	default:
		fmt.Fprintf(os.Stderr, "Usage: %s [json|env|validate|bootstrap|healthcheck|scenarios|project]\n", os.Args[0])
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

func bootstrapEnvFile(path string) error {
	if path == "" {
		return errors.New("env file path is empty")
	}

	host := getEnvOrDefault("VM_TEST_HOST", defaultTestHost)
	preferDefaultPorts := getEnvOrDefault("VMGATHER_PREFER_DEFAULT_PORTS", "1") != "0"
	env := make(map[string]string)

	portKeys := []struct {
		key         string
		defaultPort int
	}{
		{"VMGATHER_PORT", 8080},
		{"VM_SINGLE_NOAUTH_PORT", 18428},
		{"VM_SINGLE_AUTH_PORT", 18429},
		{"VM_AUTH_SINGLE_PORT", 8427},
		{"VM_STORAGE1_HTTP_PORT", 8482},
		{"VM_STORAGE1_INSERT_PORT", 8400},
		{"VM_STORAGE1_SELECT_PORT", 8401},
		{"VM_STORAGE2_HTTP_PORT", 8483},
		{"VM_STORAGE2_INSERT_PORT", 8402},
		{"VM_STORAGE2_SELECT_PORT", 8403},
		{"VM_INSERT_PORT", 8480},
		{"VM_SELECT_PORT", 8481},
		{"VMSELECT_STANDALONE_PORT", 8491},
		{"VM_AUTH_CLUSTER_PORT", 8426},
		{"VM_AUTH_EXPORT_PORT", 8425},
		{"VM_AGENT_PORT", 8430},
		{"PROMETHEUS_PORT", 9090},
		{"NGINX_PORT", 8888},
	}

	used := map[int]bool{}
	for _, item := range portKeys {
		if val := os.Getenv(item.key); val != "" {
			port, err := strconv.Atoi(val)
			if err == nil {
				if !used[port] && portAvailable(host, port) {
					env[item.key] = strconv.Itoa(port)
					used[port] = true
					continue
				}
			}
		}

		// Prefer deterministic default ports when available. This makes the manual UX much simpler
		// (e.g. vmgather on :8080) and keeps the env file stable across restarts.
		if preferDefaultPorts && !used[item.defaultPort] && portAvailable(host, item.defaultPort) {
			env[item.key] = strconv.Itoa(item.defaultPort)
			used[item.defaultPort] = true
			continue
		}

		port, err := pickFreePort(host, used)
		if err != nil {
			return fmt.Errorf("failed to pick free port for %s: %w", item.key, err)
		}
		env[item.key] = strconv.Itoa(port)
	}

	vmGatherAddr := fmt.Sprintf("%s:%s", host, env["VMGATHER_PORT"])
	env["VMGATHER_ADDR"] = vmGatherAddr
	env["VMGATHER_URL"] = fmt.Sprintf("http://%s", vmGatherAddr)

	env["VM_SINGLE_NOAUTH_URL"] = fmt.Sprintf("http://%s:%s", host, env["VM_SINGLE_NOAUTH_PORT"])
	env["VM_CLUSTER_SELECT_TENANT_0"] = fmt.Sprintf("http://%s:%s/select/0/prometheus", host, env["VM_SELECT_PORT"])
	env["VMSELECT_STANDALONE_URL"] = fmt.Sprintf("http://%s:%s", host, env["VMSELECT_STANDALONE_PORT"])
	env["VMSELECT_STANDALONE_SELECT_TENANT_0"] = fmt.Sprintf("%s/select/0/prometheus", env["VMSELECT_STANDALONE_URL"])

	return writeEnvFile(path, env)
}

func portAvailable(host string, port int) bool {
	// Docker publishes ports on both IPv4 and IPv6. Checking only "localhost" may bind to ::1 and
	// miss an IPv4 conflict (or vice versa). For local usage, check both loopback families.
	hosts := []string{host}
	if host == "" || strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1" {
		hosts = []string{"127.0.0.1", "::1"}
	}
	for _, h := range hosts {
		addr := net.JoinHostPort(h, strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			if shouldIgnoreListenError(h, err) {
				continue
			}
			return false
		}
		_ = listener.Close()
	}
	return true
}

func shouldIgnoreListenError(host string, err error) bool {
	if host != "::1" || err == nil {
		return false
	}
	if errors.Is(err, syscall.EAFNOSUPPORT) ||
		errors.Is(err, syscall.EPROTONOSUPPORT) ||
		errors.Is(err, syscall.EADDRNOTAVAIL) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address family not supported") ||
		strings.Contains(msg, "protocol not supported") ||
		strings.Contains(msg, "cannot assign requested address") ||
		strings.Contains(msg, "can't assign requested address")
}

func pickFreePort(host string, used map[int]bool) (int, error) {
	span := fallbackPortRangeEnd - fallbackPortRangeStart + 1
	if span <= 0 {
		return 0, fmt.Errorf("invalid fallback port range %d..%d", fallbackPortRangeStart, fallbackPortRangeEnd)
	}
	startOffset := (time.Now().Nanosecond() + os.Getpid()) % span
	for i := 0; i < span; i++ {
		port := fallbackPortRangeStart + ((startOffset + i) % span)
		if used[port] {
			continue
		}
		if !portAvailable(host, port) {
			continue
		}
		used[port] = true
		return port, nil
	}
	return 0, fmt.Errorf("unable to find free port in range %d..%d", fallbackPortRangeStart, fallbackPortRangeEnd)
}

func writeEnvFile(path string, env map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Auto-generated by local-test-env/testconfig bootstrap\n")
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(env[key])
		b.WriteString("\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
