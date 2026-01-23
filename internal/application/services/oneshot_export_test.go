package services

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func baseOneshotConfig(url string) domain.ExportConfig {
	return domain.ExportConfig{
		Connection: domain.VMConnection{URL: url},
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-time.Minute),
			End:   time.Now(),
		},
		Batching: domain.BatchSettings{Enabled: false, Strategy: "manual"},
	}
}

func runExportToWriter(t *testing.T, cfg domain.ExportConfig) (string, int, error) {
	t.Helper()
	ApplyExportDefaults(&cfg)
	var buf bytes.Buffer
	count, err := ExportToWriter(context.Background(), cfg, &buf)
	return buf.String(), count, err
}

func serveExportAndQueryRange(t *testing.T, exportStatus int, exportBody string, queryStatus int, queryBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/export":
			w.WriteHeader(exportStatus)
			_, _ = io.WriteString(w, exportBody)
		case "/api/v1/query_range":
			w.WriteHeader(queryStatus)
			_, _ = io.WriteString(w, queryBody)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
}

// Positive oneshot exports: 3 main + 2 selector
func TestOneshotExport_Positive_ClusterExport(t *testing.T) {
	exportBody := `{"metric":{"__name__":"vm_app_version","job":"test1","instance":"host:1234"},"values":[1],"timestamps":[1]}` + "\n"
	srv := serveExportAndQueryRange(t, http.StatusOK, exportBody, http.StatusOK, "")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	out, count, err := runExportToWriter(t, cfg)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 metric, got %d", count)
	}
	if !strings.Contains(out, "\"vm_app_version\"") {
		t.Fatalf("expected metric in output: %s", out)
	}
}

func TestOneshotExport_Positive_MetricsQLQueryRange(t *testing.T) {
	queryBody := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"vm_rows_inserted_total","job":"test1"},"values":[[1700000000,"1"]]}]}}`
	srv := serveExportAndQueryRange(t, http.StatusOK, "", http.StatusOK, queryBody)
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeMetricsQL
	cfg.Query = `rate(vm_rows_inserted_total[5m])`
	out, count, err := runExportToWriter(t, cfg)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 metric, got %d", count)
	}
	if !strings.Contains(out, "\"vm_rows_inserted_total\"") {
		t.Fatalf("expected metric in output: %s", out)
	}
}

func TestOneshotExport_Positive_MetricsQLDropLabels(t *testing.T) {
	queryBody := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"vm_cache_size_bytes","job":"test1","env":"dev"},"values":[[1700000000,"1"]]}]}}`
	srv := serveExportAndQueryRange(t, http.StatusOK, "", http.StatusOK, queryBody)
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeMetricsQL
	cfg.Query = `avg_over_time(vm_cache_size_bytes[5m])`
	cfg.Obfuscation = domain.ObfuscationConfig{
		Enabled:    false,
		DropLabels: []string{"env"},
	}
	out, count, err := runExportToWriter(t, cfg)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 metric, got %d", count)
	}
	if strings.Contains(out, "\"env\"") {
		t.Fatalf("expected env label to be dropped: %s", out)
	}
}

func TestOneshotExport_Positive_SelectorNoJobFilter(t *testing.T) {
	exportBody := `{"metric":{"__name__":"vm_app_version","job":"test1","instance":"host:1234"},"values":[1],"timestamps":[1]}` + "\n"
	srv := serveExportAndQueryRange(t, http.StatusOK, exportBody, http.StatusOK, "")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeSelector
	cfg.Query = `{job=~"test.*"}`
	out, count, err := runExportToWriter(t, cfg)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 metric, got %d", count)
	}
	if !strings.Contains(out, "\"job\":\"test1\"") {
		t.Fatalf("expected selector output: %s", out)
	}
}

func TestOneshotExport_Positive_SelectorWithJobFilter(t *testing.T) {
	queryBody := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"vm_app_version","job":"test1"},"values":[[1700000000,"1"]]}]}}`
	srv := serveExportAndQueryRange(t, http.StatusOK, "", http.StatusOK, queryBody)
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeSelector
	cfg.Query = `{job=~"test.*"}`
	cfg.Jobs = []string{"test1"}
	out, count, err := runExportToWriter(t, cfg)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 metric, got %d", count)
	}
	if !strings.Contains(out, "\"job\":\"test1\"") {
		t.Fatalf("expected selector output: %s", out)
	}
}

// Negative oneshot exports: 3 main + 2 selector
func TestOneshotExport_Negative_ClusterExportFails(t *testing.T) {
	srv := serveExportAndQueryRange(t, http.StatusBadRequest, "bad request", http.StatusOK, "")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	_, _, err := runExportToWriter(t, cfg)
	if err == nil {
		t.Fatalf("expected export failure")
	}
}

func TestOneshotExport_Negative_MetricsQLQueryRangeFails(t *testing.T) {
	srv := serveExportAndQueryRange(t, http.StatusOK, "", http.StatusBadRequest, "bad request")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeMetricsQL
	cfg.Query = `rate(vm_rows_inserted_total[5m])`
	_, _, err := runExportToWriter(t, cfg)
	if err == nil {
		t.Fatalf("expected query_range failure")
	}
}

func TestOneshotExport_Negative_MetricsQLInvalidJSON(t *testing.T) {
	srv := serveExportAndQueryRange(t, http.StatusOK, "", http.StatusOK, "{not valid")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeMetricsQL
	cfg.Query = `rate(vm_rows_inserted_total[5m])`
	_, _, err := runExportToWriter(t, cfg)
	if err == nil {
		t.Fatalf("expected query_range JSON failure")
	}
}

func TestOneshotExport_Negative_SelectorExportFails(t *testing.T) {
	srv := serveExportAndQueryRange(t, http.StatusBadRequest, "bad request", http.StatusOK, "")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeSelector
	cfg.Query = `{job=~"test.*"}`
	_, _, err := runExportToWriter(t, cfg)
	if err == nil {
		t.Fatalf("expected selector export failure")
	}
}

func TestOneshotExport_Negative_SelectorJobFilterQueryRangeFails(t *testing.T) {
	srv := serveExportAndQueryRange(t, http.StatusOK, "", http.StatusBadRequest, "bad request")
	defer srv.Close()

	cfg := baseOneshotConfig(srv.URL)
	cfg.Mode = domain.ExportModeCustom
	cfg.QueryType = domain.QueryModeSelector
	cfg.Query = `{job=~"test.*"}`
	cfg.Jobs = []string{"test1"}
	_, _, err := runExportToWriter(t, cfg)
	if err == nil {
		t.Fatalf("expected selector query_range failure")
	}
}
