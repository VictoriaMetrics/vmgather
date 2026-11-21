package services

import "github.com/VictoriaMetrics/vmgather/internal/domain"

const MaxAutopilotMetricStepSeconds = 5 * 60

var defaultAutopilotStepLadderSeconds = []int{30, 60, 120, MaxAutopilotMetricStepSeconds}

// ApplyExportDefaults normalizes export configuration for CLI and server usage.
func ApplyExportDefaults(config *domain.ExportConfig) {
	settings := &config.Batching
	safety := &config.Safety
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
	if safety.Mode == "" {
		safety.Mode = domain.ExportAdaptivityAutopilot
	}
	if safety.Mode != domain.ExportAdaptivityOff && !safety.AutoSplit && !safety.SplitByJob && safety.MinWindowSeconds <= 0 && safety.MaxSplitDepth <= 0 {
		safety.AutoSplit = true
		safety.SplitByJob = true
	}
	if safety.Mode == domain.ExportAdaptivityOff {
		safety.AutoSplit = false
		safety.SplitByJob = false
	}
	if safety.MinWindowSeconds <= 0 {
		safety.MinWindowSeconds = 5
	}
	if safety.MaxSplitDepth <= 0 {
		safety.MaxSplitDepth = 8
	}
	if safety.MaxStepSeconds <= 0 || safety.MaxStepSeconds > MaxAutopilotMetricStepSeconds {
		safety.MaxStepSeconds = MaxAutopilotMetricStepSeconds
	}
	if safety.MaxStepSeconds < MinBatchIntervalSeconds {
		safety.MaxStepSeconds = MinBatchIntervalSeconds
	}
	safety.StepLadderSeconds = normalizeStepLadder(safety.StepLadderSeconds, safety.MaxStepSeconds)
	if config.MetricStepSeconds <= 0 {
		if safety.Mode == domain.ExportAdaptivityAutopilot {
			config.MetricStepSeconds = safety.StepLadderSeconds[0]
		} else {
			config.MetricStepSeconds = RecommendedMetricStepSeconds(config.TimeRange)
		}
	}
	if config.MetricStepSeconds > safety.MaxStepSeconds {
		config.MetricStepSeconds = safety.MaxStepSeconds
	}
	if !config.Obfuscation.Enabled {
		config.Obfuscation = domain.ObfuscationConfig{DropLabels: config.Obfuscation.DropLabels}
	}
}

func normalizeStepLadder(values []int, maxStepSeconds int) []int {
	if len(values) == 0 {
		values = defaultAutopilotStepLadderSeconds
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, step := range values {
		if step < MinBatchIntervalSeconds || step > maxStepSeconds {
			continue
		}
		if _, ok := seen[step]; ok {
			continue
		}
		seen[step] = struct{}{}
		result = append(result, step)
	}
	if len(result) == 0 {
		result = []int{maxStepSeconds}
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}
