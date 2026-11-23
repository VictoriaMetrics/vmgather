package snapshot

import (
	"slices"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	if s == "" {
		return time.Time{}
	}
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad time %q: %v", s, err)
	}
	return v
}

func TestPlanWindows(t *testing.T) {
	f := func(lastEnd, start, end string, period time.Duration, maxCatchUp int, want ...string) {
		t.Helper()
		got := slices.Collect(planWindows(mustTime(t, lastEnd), mustTime(t, start), mustTime(t, end), period, maxCatchUp))
		if len(got) != len(want)/2 {
			t.Fatalf("got %d windows %v; want %d", len(got), got, len(want)/2)
		}
		for i, w := range got {
			wantW := domain.TimeRange{Start: mustTime(t, want[2*i]), End: mustTime(t, want[2*i+1])}
			if !w.Start.Equal(wantW.Start) || !w.End.Equal(wantW.End) {
				t.Fatalf("window %d = [%s, %s); want [%s, %s)", i, w.Start, w.End, wantW.Start, wantW.End)
			}
		}
	}
	h := time.Hour

	// First run without state and start: one full period that ends at the aligned end.
	f("", "", "2026-11-21T10:07:00Z", h, 24, "2026-11-21T09:00:00Z", "2026-11-21T10:00:00Z")
	// Next run continues from the state.
	f("2026-11-21T10:00:00Z", "", "2026-11-21T11:07:00Z", h, 24, "2026-11-21T10:00:00Z", "2026-11-21T11:00:00Z")
	// Nothing to do before the next period is complete.
	f("2026-11-21T11:00:00Z", "", "2026-11-21T11:59:59Z", h, 24)
	f("2026-11-21T11:00:00Z", "", "2026-11-21T11:00:00Z", h, 24)
	// Catch up after downtime, limited per run.
	f("2026-11-21T06:00:00Z", "", "2026-11-21T10:30:00Z", h, 2,
		"2026-11-21T06:00:00Z", "2026-11-21T07:00:00Z",
		"2026-11-21T07:00:00Z", "2026-11-21T08:00:00Z")
	// The state wins over start.
	f("2026-11-21T09:00:00Z", "2026-11-01T00:00:00Z", "2026-11-21T10:00:00Z", h, 24,
		"2026-11-21T09:00:00Z", "2026-11-21T10:00:00Z")
	// Backfill from start to a fixed end.
	f("", "2026-11-20T22:00:00Z", "2026-11-21T01:00:00Z", h, 24,
		"2026-11-20T22:00:00Z", "2026-11-20T23:00:00Z",
		"2026-11-20T23:00:00Z", "2026-11-21T00:00:00Z",
		"2026-11-21T00:00:00Z", "2026-11-21T01:00:00Z")
	// Off-grid start (period changed from 30m to 1h): the first window ends at the next grid point.
	f("2026-11-21T09:30:00Z", "", "2026-11-21T11:10:00Z", h, 24,
		"2026-11-21T09:30:00Z", "2026-11-21T10:00:00Z",
		"2026-11-21T10:00:00Z", "2026-11-21T11:00:00Z")
	// A state in the future (clock moved back) gives no windows instead of a negative range.
	f("2026-11-21T12:00:00Z", "", "2026-11-21T10:10:00Z", h, 24)
	// Invalid settings.
	f("", "", "2026-11-21T10:00:00Z", 0, 24)
	f("", "", "2026-11-21T10:00:00Z", h, 0)
}
