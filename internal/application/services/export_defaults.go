package services

import "github.com/VictoriaMetrics/vmgather/internal/domain"

// ApplyExportDefaults normalizes export configuration for CLI and server usage.
func ApplyExportDefaults(config *domain.ExportConfig) {
	settings := &config.Batching
	if !settings.Enabled && settings.Strategy == "" && settings.CustomIntervalSecs == 0 {
		settings.Enabled = true
	}
	if settings.Strategy == "" {
		settings.Strategy = "auto"
	}
	if settings.CustomIntervalSecs < 0 {
		settings.CustomIntervalSecs = 0
	}
	minSeconds := MinBatchIntervalSeconds
	maxSeconds := MaxBatchIntervalSeconds
	if settings.CustomIntervalSecs > 0 && settings.CustomIntervalSecs < minSeconds {
		settings.CustomIntervalSecs = minSeconds
	}
	if settings.CustomIntervalSecs > maxSeconds {
		settings.CustomIntervalSecs = maxSeconds
	}
	if config.MetricStepSeconds <= 0 {
		config.MetricStepSeconds = RecommendedMetricStepSeconds(config.TimeRange)
	}
	if !config.Obfuscation.Enabled {
		config.Obfuscation = domain.ObfuscationConfig{DropLabels: config.Obfuscation.DropLabels}
	}
}
