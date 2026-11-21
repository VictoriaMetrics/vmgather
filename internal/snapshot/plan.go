// Package snapshot runs periodic exports of MDX metrics into object storage.
package snapshot

import (
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// planWindows returns the half-open [Start, End) windows to export, oldest first.
//
// end is aligned down to period. The first window starts at lastEnd when it is known (the end of the
// last uploaded snapshot), else at start, else at end-period. A first window that starts off the period
// grid (for example after a period change) ends at the next grid point, so later windows are aligned.
// At most maxCatchUp windows are returned; the next run continues from the last one.
func planWindows(lastEnd, start, end time.Time, period time.Duration, maxCatchUp int) []domain.TimeRange {
	if period <= 0 || maxCatchUp <= 0 {
		return nil
	}
	end = end.Truncate(period)
	from := lastEnd
	if from.IsZero() {
		from = start
	}
	if from.IsZero() {
		from = end.Add(-period)
	}

	var windows []domain.TimeRange
	for from.Before(end) && len(windows) < maxCatchUp {
		to := from.Truncate(period).Add(period)
		if to.After(end) {
			to = end
		}
		windows = append(windows, domain.TimeRange{Start: from, End: to})
		from = to
	}
	return windows
}
