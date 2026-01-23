package integration

import (
	"os"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func vmSingleNoAuthURL() string {
	return envOrDefault("VM_SINGLE_NOAUTH_URL", "http://localhost:18428")
}

func vmAuthExportURL() string {
	return envOrDefault("VM_AUTH_EXPORT_URL", "http://localhost:8425")
}

func vmAuthExportLegacyAuth() domain.AuthConfig {
	return domain.AuthConfig{
		Type:     "basic",
		Username: envOrDefault("VM_AUTH_EXPORT_LEGACY_USER", "tenant1011-legacy"),
		Password: envOrDefault("VM_AUTH_EXPORT_LEGACY_PASS", "legacy-pass-1011"),
	}
}

func vmAuthExportModernAuth() domain.AuthConfig {
	return domain.AuthConfig{
		Type:     "basic",
		Username: envOrDefault("VM_AUTH_EXPORT_MODERN_USER", "tenant2022-modern"),
		Password: envOrDefault("VM_AUTH_EXPORT_MODERN_PASS", "modern-pass-2022"),
	}
}
