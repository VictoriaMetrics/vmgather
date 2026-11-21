package services

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// Regression tests for export failures seen in support tickets (ESUPP-950, ESUPP-1043, ESUPP-1398, ESUPP-902).

func regressionConfig(url, dir string) domain.ExportConfig {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	return domain.ExportConfig{
		Connection:  domain.VMConnection{URL: url, Auth: domain.AuthConfig{Type: domain.AuthTypeNone}},
		Mode:        domain.ExportModeCluster,
		Jobs:        []string{"vmstorage"},
		TimeRange:   domain.TimeRange{Start: base, End: base.Add(2 * time.Minute)},
		Batching:    domain.BatchSettings{Enabled: true, CustomIntervalSecs: 60},
		StagingDir:  dir,
		StagingFile: filepath.Join(dir, "job.partial.jsonl"),
	}
}

func readArchiveMetrics(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = zr.Close() }()
	var out []map[string]interface{}
	for _, f := range zr.File {
		if f.Name != "metrics.jsonl" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open metrics.jsonl: %v", err)
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var m map[string]interface{}
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Fatalf("bad line %q: %v", sc.Text(), err)
			}
			out = append(out, m)
		}
		_ = rc.Close()
	}
	return out
}

// ESUPP-1043, ESUPP-950: a cluster-mode export without jobs must not fall back to {__name__!=""}
// (the whole database). It must fail fast before sending any export request.
func TestExport_ClusterModeWithoutJobsIsRejected(t *testing.T) {
	var mu sync.Mutex
	var selectors []string
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		selectors = append(selectors, r.Form.Get("match[]"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := regressionConfig(srv.URL, t.TempDir())
	cfg.Components = []string{"vmagent"}
	cfg.Jobs = nil

	_, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err == nil {
		t.Fatalf("expected an error for cluster export without jobs, got success; selectors sent: %q", selectors)
	}
	if len(selectors) != 0 {
		t.Fatalf("no export request expected, got selectors %q", selectors)
	}
}

// ESUPP-1398: a vmselect restart returns 502 for one request. The batch must be retried,
// not fail the whole export.
func TestExport_RetriesTransientUpstreamError(t *testing.T) {
	fastRetries(t)
	var calls int32
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "upstream vmselect restarted", http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"metric":{"__name__":"vm_x","job":"vmstorage"},"values":[1],"timestamps":[%d]}`+"\n", time.Now().UnixMilli())
	}))
	defer srv.Close()

	cfg := regressionConfig(srv.URL, t.TempDir())
	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected transient 502 to be retried, got error: %v", err)
	}
	if res.MetricsExported != 2 {
		t.Fatalf("expected 2 exported lines (2 batches), got %d", res.MetricsExported)
	}
}

// Measured on a live vmsingle: 6h of data in 60s windows = 360 requests and 497 MB,
// in 1h windows = 6 requests and 48 MB. The default window must not repeat labels per minute.
func TestBatchWindows_DefaultDoesNotSplitIntoMinutes(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, rangeDur := range []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour} {
		tr := domain.TimeRange{Start: start, End: start.Add(rangeDur)}
		windows := CalculateBatchWindows(tr, domain.BatchSettings{Enabled: true, Strategy: "auto"})
		maxWindows := int((rangeDur + time.Hour - 1) / time.Hour)
		if len(windows) > maxWindows {
			t.Errorf("range %s: got %d windows of %s, want at most %d (1h windows)",
				rangeDur, len(windows), windows[0].End.Sub(windows[0].Start), maxWindows)
		}
	}
}

func queryRangeServer(values []string) *httptest.Server {
	return newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query_range") {
			http.NotFound(w, r)
			return
		}
		start := r.URL.Query().Get("start")
		var sb strings.Builder
		for i, v := range values {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `[%s,"%s"]`, start, v)
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"m","job":"j"},"values":[%s]}]}}`, sb.String())
	}))
}

func metricsQLConfig(url, dir string, rangeDur time.Duration) domain.ExportConfig {
	cfg := regressionConfig(url, dir)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeMetricsQL
	cfg.Query = "rate(m[5m])"
	cfg.Jobs = nil
	cfg.TimeRange.End = cfg.TimeRange.Start.Add(rangeDur)
	cfg.Batching = domain.BatchSettings{Enabled: false}
	return cfg
}

// ESUPP-902 path: the query_range fallback wrote one JSON line per sample, repeating all labels.
// One series in one chunk must be one line.
func TestExport_QueryRangeFallbackWritesOneLinePerSeries(t *testing.T) {
	srv := queryRangeServer([]string{"1", "2", "3", "4", "5"})
	defer srv.Close()

	cfg := metricsQLConfig(srv.URL, t.TempDir(), 30*time.Minute)
	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	lines := readArchiveMetrics(t, res.ArchivePath)
	if len(lines) != 1 {
		t.Fatalf("want 1 line for 1 series, got %d", len(lines))
	}
	if got := len(lines[0]["values"].([]interface{})); got != 5 {
		t.Fatalf("want 5 values in the line, got %d", got)
	}
}

// MetricsQL often returns NaN (division by zero). The fallback must not fail on it and must
// write it the same way /api/v1/export does (null), so vmimporter can import it.
func TestExport_QueryRangeFallbackKeepsSpecialValues(t *testing.T) {
	srv := queryRangeServer([]string{"1", "NaN", "+Inf", "-Inf"})
	defer srv.Close()

	cfg := metricsQLConfig(srv.URL, t.TempDir(), 30*time.Minute)
	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("export with NaN/Inf failed: %v", err)
	}
	lines := readArchiveMetrics(t, res.ArchivePath)
	var values []interface{}
	for _, l := range lines {
		values = append(values, l["values"].([]interface{})...)
	}
	want := []interface{}{float64(1), nil, "Infinity", "-Infinity"}
	if fmt.Sprint(values) != fmt.Sprint(want) {
		t.Fatalf("want values %v, got %v", want, values)
	}
}
