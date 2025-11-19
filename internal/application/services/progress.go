package services

import (
	"context"
	"time"

	"github.com/VictoriaMetrics/support/internal/domain"
)

type progressKeyType struct{}

var progressKey = progressKeyType{}

// BatchProgress describes the completion of a single batch window.
type BatchProgress struct {
	BatchIndex   int
	TotalBatches int
	TimeRange    domain.TimeRange
	Metrics      int
	Duration     time.Duration
}

// ProgressReporter receives progress events for long-running exports.
type ProgressReporter interface {
	OnBatchComplete(BatchProgress)
}

// WithProgressReporter attaches a reporter to the context so that ExecuteExport can publish progress.
func WithProgressReporter(ctx context.Context, reporter ProgressReporter) context.Context {
	return context.WithValue(ctx, progressKey, reporter)
}

func getProgressReporter(ctx context.Context) ProgressReporter {
	if reporter, ok := ctx.Value(progressKey).(ProgressReporter); ok {
		return reporter
	}
	return nil
}

// ReportBatchProgress delivers progress updates to the reporter stored in the context.
func ReportBatchProgress(ctx context.Context, progress BatchProgress) {
	if reporter := getProgressReporter(ctx); reporter != nil {
		reporter.OnBatchComplete(progress)
	}
}
