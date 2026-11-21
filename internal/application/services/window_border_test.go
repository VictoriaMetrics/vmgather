package services

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// parseVMTime parses a start/end argument the way VictoriaMetrics does for the forms vmgather sends:
// RFC3339 (optionally with fractional seconds) or unix seconds with an optional fractional part.
func parseVMTime(t *testing.T, s string) int64 {
	t.Helper()
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts.UnixMilli()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Errorf("bad time %q", s) // called from HTTP handlers: no Fatal outside the test goroutine
		return 0
	}
	return int64(f*1000 + 0.5)
}

// queryRangePoint formats a query_range point at tsMs like the Prometheus API: [unix seconds, "value"].
func queryRangePoint(tsMs int64) string {
	return fmt.Sprintf(`[%s,"1"]`, strconv.FormatFloat(float64(tsMs)/1000, 'f', -1, 64))
}

// vmWindow returns the start/end of an /api/v1/export request.
func vmWindow(t *testing.T, r *http.Request) (time.Time, time.Time) {
	t.Helper()
	_ = r.ParseForm()
	return time.UnixMilli(parseVMTime(t, r.Form.Get("start"))).UTC(), time.UnixMilli(parseVMTime(t, r.Form.Get("end"))).UTC()
}

// alignedSamples are 30s samples exactly on minute and hour borders, like vmalert recording rules.
func alignedSamples(start time.Time, d time.Duration) []int64 {
	var out []int64
	for ts := start; !ts.After(start.Add(d)); ts = ts.Add(30 * time.Second) {
		out = append(out, ts.UnixMilli())
	}
	return out
}

// Live on VictoriaMetrics v1.155.0 with aligned timestamps, v1.12.0 exported 347 samples for 228 stored:
// /api/v1/export includes both start and end, so every sample on a window border was exported twice.
func TestExport_SampleOnWindowBorderIsExportedOnce(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	samples := alignedSamples(base, 2*time.Hour)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		from, to := parseVMTime(t, r.Form.Get("start")), parseVMTime(t, r.Form.Get("end"))
		var vals, tss []string
		for _, ts := range samples {
			if ts >= from && ts <= to { // VictoriaMetrics: start and end are inclusive
				vals = append(vals, "1")
				tss = append(tss, strconv.FormatInt(ts, 10))
			}
		}
		if len(tss) > 0 {
			fmt.Fprintf(w, `{"metric":{"__name__":"m","job":"vmstorage"},"values":[%s],"timestamps":[%s]}`+"\n", strings.Join(vals, ","), strings.Join(tss, ","))
		}
	}))
	defer srv.Close()

	for _, window := range []int{60, 3600} {
		t.Run(fmt.Sprintf("window=%ds", window), func(t *testing.T) {
			cfg := regressionConfig(srv.URL, t.TempDir())
			cfg.TimeRange = domain.TimeRange{Start: base, End: base.Add(2 * time.Hour)}
			cfg.Batching = domain.BatchSettings{Enabled: true, CustomIntervalSecs: window}
			res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[float64]int{}
			total := 0
			for _, l := range readArchiveMetrics(t, res.ArchivePath) {
				for _, ts := range l["timestamps"].([]interface{}) {
					seen[ts.(float64)]++
					total++
				}
			}
			if total != len(samples) || len(seen) != len(samples) {
				t.Fatalf("want each of %d samples once, got %d samples (%d distinct)", len(samples), total, len(seen))
			}
		})
	}
}

// The query_range fallback asked for [start, end] per 1h chunk, so the point on each chunk border came twice.
func TestExport_QueryRangeChunkBorderPointIsExportedOnce(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		start, end := parseVMTime(t, q.Get("start")), parseVMTime(t, q.Get("end"))
		step, _ := time.ParseDuration(q.Get("step"))
		var pts []string
		for ts := start; ts <= end; ts += step.Milliseconds() { // Prometheus API: start and end are inclusive
			pts = append(pts, queryRangePoint(ts))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"m","job":"j"},"values":[%s]}]}}`, strings.Join(pts, ","))
	}))
	defer srv.Close()

	cfg := metricsQLConfig(srv.URL, t.TempDir(), 3*time.Hour)
	cfg.MetricStepSeconds = 60
	cfg.Safety.Mode = domain.ExportAdaptivityOff
	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[float64]int{}
	total := 0
	for _, l := range readArchiveMetrics(t, res.ArchivePath) {
		for _, ts := range l["timestamps"].([]interface{}) {
			seen[ts.(float64)]++
			total++
		}
	}
	if want := 3*60 + 1; total != want || len(seen) != want {
		t.Fatalf("want %d points once each, got %d points (%d distinct)", want, total, len(seen))
	}
}

// query_range sent start and end as whole seconds. With a start at a fraction of a second, the chunk that ends
// 1ms before the border and the next chunk both became the same second, and the border point came twice.
func TestExport_QueryRangeFractionalBorderPointIsExportedOnce(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		start, end := parseVMTime(t, q.Get("start")), parseVMTime(t, q.Get("end"))
		step, _ := time.ParseDuration(q.Get("step"))
		var pts []string
		for ts := start; ts <= end; ts += step.Milliseconds() {
			pts = append(pts, queryRangePoint(ts))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"m","job":"j"},"values":[%s]}]}}`, strings.Join(pts, ","))
	}))
	defer srv.Close()

	cfg := metricsQLConfig(srv.URL, t.TempDir(), 2*time.Hour)
	cfg.TimeRange.Start = cfg.TimeRange.Start.Add(500 * time.Millisecond)
	cfg.MetricStepSeconds = 60
	cfg.Safety.Mode = domain.ExportAdaptivityOff
	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[float64]int{}
	for _, l := range readArchiveMetrics(t, res.ArchivePath) {
		for _, ts := range l["timestamps"].([]interface{}) {
			seen[ts.(float64)]++
		}
	}
	for ts, n := range seen {
		if n > 1 {
			t.Fatalf("timestamp %d exported %d times", int64(ts), n)
		}
	}
}

// After a timeout split in query_range mode the halves must stay on the step grid and must not evaluate the
// border timestamp twice (the window ends 1ms before the next one, which put the midpoint on a fraction of a second).
func TestExport_QueryRangeSplitKeepsStepGrid(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		start, end := parseVMTime(t, q.Get("start")), parseVMTime(t, q.Get("end"))
		step, _ := time.ParseDuration(q.Get("step"))
		if end-start > 1000*1000 { // heavy: VictoriaMetrics times out on windows longer than ~16 minutes
			http.Error(w, `{"status":"error","error":"timeout exceeded during query execution: 30.000 seconds"}`, 422)
			return
		}
		var pts []string
		for ts := start; ts <= end; ts += step.Milliseconds() {
			pts = append(pts, queryRangePoint(ts))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"m","job":"j"},"values":[%s]}]}}`, strings.Join(pts, ","))
	}))
	defer srv.Close()

	cfg := metricsQLConfig(srv.URL, t.TempDir(), 2*time.Hour)
	cfg.Batching = domain.BatchSettings{Enabled: true, CustomIntervalSecs: 3600}
	cfg.MetricStepSeconds = 30
	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := cfg.TimeRange.Start.Unix()
	seen := map[int64]int{}
	for _, l := range readArchiveMetrics(t, res.ArchivePath) {
		for _, ts := range l["timestamps"].([]interface{}) {
			sec := int64(ts.(float64)) / 1000
			seen[sec]++
			if (sec-base)%30 != 0 {
				t.Errorf("timestamp %d is off the 30s step grid", sec-base)
			}
		}
	}
	for sec, n := range seen {
		if n > 1 {
			t.Errorf("timestamp +%ds exported %d times", sec-base, n)
		}
	}
	if want := 2*120 + 1; len(seen) != want {
		t.Errorf("want %d distinct points, got %d", want, len(seen))
	}
}
