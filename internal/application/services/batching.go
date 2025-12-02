package services

import (
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/domain"
)

const (
	minBatchInterval = 30 * time.Second
	maxBatchInterval = 24 * time.Hour
)

const (
	MinBatchIntervalSeconds = 30
	MaxBatchIntervalSeconds = 24 * 60 * 60
)

// CalculateBatchWindows splits the requested time range into windows suitable for batched exports.
// When batching is disabled the original range is returned as a single window.
func CalculateBatchWindows(tr domain.TimeRange, settings domain.BatchSettings) []domain.TimeRange {
	if tr.End.Before(tr.Start) || tr.Start.Equal(tr.End) {
		return []domain.TimeRange{tr}
	}

	if !settings.Enabled {
		return []domain.TimeRange{tr}
	}

	window := selectBatchInterval(tr, settings)
	if window <= 0 {
		window = minBatchInterval
	}

	var windows []domain.TimeRange
	current := tr.Start
	for current.Before(tr.End) {
		next := current.Add(window)
		if next.After(tr.End) {
			next = tr.End
		}
		windows = append(windows, domain.TimeRange{Start: current, End: next})
		current = next
	}

	if len(windows) == 0 {
		windows = append(windows, tr)
	}
	return windows
}

func selectBatchInterval(tr domain.TimeRange, settings domain.BatchSettings) time.Duration {
	if settings.CustomIntervalSecs > 0 {
		custom := time.Duration(settings.CustomIntervalSecs) * time.Second
		if custom < minBatchInterval {
			return minBatchInterval
		}
		if custom > maxBatchInterval {
			return maxBatchInterval
		}
		return custom
	}

	return recommendedIntervalForDuration(tr.End.Sub(tr.Start))
}

func recommendedIntervalForDuration(duration time.Duration) time.Duration {
	switch {
	case duration <= 15*time.Minute:
		return 30 * time.Second
	case duration <= 6*time.Hour:
		return time.Minute
	default:
		return 5 * time.Minute
	}
}

// RecommendedMetricStepSeconds returns the default deduplication step for the given range.
func RecommendedMetricStepSeconds(tr domain.TimeRange) int {
	if tr.End.Before(tr.Start) || tr.Start.Equal(tr.End) {
		return MinBatchIntervalSeconds
	}
	return int(recommendedIntervalForDuration(tr.End.Sub(tr.Start)).Seconds())
}
