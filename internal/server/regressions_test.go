package server

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func newLoopbackServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: h}}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func waitJobState(t *testing.T, m *ExportJobManager, id string, want ExportJobState) *ExportJobStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, ok := m.GetStatus(id)
		if ok && st.State == want {
			return st
		}
		if ok && (st.State == JobFailed || st.State == JobCompleted || st.State == JobCanceled) && st.State != want {
			t.Fatalf("job %s ended in %s (want %s): %s", id, st.State, want, st.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, want)
	return nil
}

// Batch 1 holds instance A, batch 2 holds instance B. Batch 2 fails with a non-retryable error,
// then the user resumes. The bundle must keep A and B apart and count both lines.
func TestExportJob_ResumeKeepsObfuscationAndMetricsCount(t *testing.T) {
	var failSecondBatch atomic.Bool
	failSecondBatch.Store(true)
	vm := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		inst := "10.0.0.1:8482"
		start, _ := strconv.ParseFloat(r.Form.Get("start"), 64) // unix seconds with milliseconds
		if int64(start)%3600 != 0 {
			inst = "10.0.0.2:8482"
			if failSecondBatch.Load() {
				http.Error(w, "cannot process request", http.StatusBadRequest)
				return
			}
		}
		fmt.Fprintf(w, `{"metric":{"__name__":"vm_x","job":"vmstorage","instance":%q},"values":[1],"timestamps":[%d]}`+"\n", inst, time.Now().UnixMilli())
	}))

	dir := t.TempDir()
	m := NewExportJobManager(services.NewExportService(dir, "test"))
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	cfg := domain.ExportConfig{
		Connection:  domain.VMConnection{URL: vm.URL, Auth: domain.AuthConfig{Type: domain.AuthTypeNone}},
		Mode:        domain.ExportModeCluster,
		Jobs:        []string{"vmstorage"},
		TimeRange:   domain.TimeRange{Start: base, End: base.Add(2 * time.Minute)},
		Batching:    domain.BatchSettings{Enabled: true, CustomIntervalSecs: 60},
		Obfuscation: domain.ObfuscationConfig{Enabled: true, ObfuscateInstance: true},
		StagingDir:  dir,
		StagingFile: filepath.Join(dir, "job.partial.jsonl"),
	}
	services.ApplyExportDefaults(&cfg)

	if _, err := m.StartJob(context.Background(), "job-resume", cfg); err != nil {
		t.Fatal(err)
	}
	waitJobState(t, m, "job-resume", JobFailed)

	failSecondBatch.Store(false)
	if _, err := m.ResumeJob(context.Background(), "job-resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	st := waitJobState(t, m, "job-resume", JobCompleted)

	zr, err := zip.OpenReader(st.Result.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	instances := map[string]bool{}
	lines := 0
	metadataCount := -1
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		switch f.Name {
		case "metrics.jsonl":
			sc := bufio.NewScanner(rc)
			for sc.Scan() {
				var line struct {
					Metric map[string]string `json:"metric"`
				}
				_ = json.Unmarshal(sc.Bytes(), &line)
				instances[line.Metric["instance"]] = true
				lines++
			}
		case "metadata.json":
			var md struct {
				MetricsCount int `json:"metrics_count"`
			}
			_ = json.NewDecoder(rc).Decode(&md)
			metadataCount = md.MetricsCount
		}
		_ = rc.Close()
	}
	if lines != 2 || len(instances) != 2 {
		t.Errorf("want 2 lines with 2 distinct obfuscated instances, got %d lines, instances %v", lines, instances)
	}
	if st.Result.MetricsExported != 2 || metadataCount != 2 {
		t.Errorf("want metrics count 2, got result=%d metadata=%d", st.Result.MetricsExported, metadataCount)
	}
}

// A web page on another origin can send a "simple" cross-origin POST (text/plain) without a CORS
// preflight. vmgather must reject it, otherwise any site can start exports and create directories.
func TestRouter_RejectsCrossOriginPost(t *testing.T) {
	srv := NewServer(t.TempDir(), "test", false)
	body := fmt.Sprintf(`{"connection":{"url":"http://127.0.0.1:1"},"jobs":["x"],"time_range":{"start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:01:00Z"},"staging_dir":%q}`,
		filepath.Join(t.TempDir(), "created-by-remote-page"))
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/export/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: want 403, got %d: %s", rr.Code, rr.Body.String())
	}

	// Same-origin requests from the UI keep working.
	req = httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/export/cancel", strings.NewReader(`{"job_id":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	rr = httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Fatalf("same-origin POST must not be rejected")
	}
}
