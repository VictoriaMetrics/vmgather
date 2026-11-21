// Package snapshot runs periodic exports of MDX metrics into object storage.
package snapshot

import (
	"iter"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// planWindows yields the half-open [Start, End) windows to export, oldest first.
//
// end is aligned down to period. The first window starts at lastEnd when it is known (the end of the
// last uploaded snapshot), else at start, else at end-period. A first window that starts off the period
// grid (an off-grid start, or a period change) ends at the next grid point, so later windows are aligned.
// At most limit windows are yielded; the next run continues from the last one. Windows are computed one
// at a time, so a long backfill with a short period needs no memory for the whole plan.
func planWindows(lastEnd, start, end time.Time, period time.Duration, limit int) iter.Seq[domain.TimeRange] {
	return func(yield func(domain.TimeRange) bool) {
		if period <= 0 {
			return
		}
		end = end.Truncate(period)
		from := lastEnd
		if from.IsZero() {
			from = start
		}
		if from.IsZero() {
			from = end.Add(-period)
		}
		for n := 0; n < limit && from.Before(end); n++ {
			to := from.Truncate(period).Add(period)
			if to.After(end) {
				to = end
			}
			if !yield(domain.TimeRange{Start: from, End: to}) {
				return
			}
			from = to
		}
	}
}
