package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/archive"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

func fastRetries(t *testing.T) {
	t.Helper()
	orig := transientRetryDelays
	transientRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { transientRetryDelays = orig })
}

func shortIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := vm.ExportIdleTimeout
	vm.ExportIdleTimeout = d
	t.Cleanup(func() { vm.ExportIdleTimeout = orig })
}

func oneHourConfig(url, dir string) domain.ExportConfig {
	cfg := regressionConfig(url, dir)
	cfg.TimeRange.End = cfg.TimeRange.Start.Add(time.Hour)
	cfg.Batching = domain.BatchSettings{Enabled: true}
	return cfg
}

func exportLineFor(r *http.Request) string {
	_ = r.ParseForm()
	return fmt.Sprintf(`{"metric":{"__name__":"vm_x","job":"vmstorage","window":%q},"values":[1],"timestamps":[1]}`+"\n", r.Form.Get("start"))
}

func TestValidateExportConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  domain.ExportConfig
		ok   bool
	}{
		{name: "cluster with jobs", cfg: domain.ExportConfig{Mode: domain.ExportModeCluster, Jobs: []string{"vmagent"}}, ok: true},
		{name: "cluster without jobs", cfg: domain.ExportConfig{Mode: domain.ExportModeCluster}},
		{name: "empty mode without jobs (CLI oneshot)", cfg: domain.ExportConfig{}},
		{name: "blank job names only", cfg: domain.ExportConfig{Jobs: []string{""}}},
		{name: "custom selector", cfg: domain.ExportConfig{Mode: domain.ExportModeCustom, QueryType: domain.QueryModeSelector, Query: `{job="x"}`}, ok: true},
		{name: "custom without query", cfg: domain.ExportConfig{Mode: domain.ExportModeCustom, QueryType: domain.QueryModeSelector, Query: "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateExportConfig(tc.cfg); (err == nil) != tc.ok {
				t.Fatalf("ok=%v, err=%v", tc.ok, err)
			}
		})
	}
}

// A stream that stalls after some data is split by time and the halves succeed.
func TestExport_StalledStreamIsSplitByTime(t *testing.T) {
	shortIdleTimeout(t, 500*time.Millisecond)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end := vmWindow(t, r)
		_, _ = fmt.Fprint(w, exportLineFor(r))
		if end.Sub(start) > 30*time.Minute {
			w.(http.Flusher).Flush()
			<-r.Context().Done() // stall until the client gives up
			return
		}
	}))
	defer srv.Close()

	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), oneHourConfig(srv.URL, t.TempDir()))
	if err != nil {
		t.Fatalf("expected split to succeed, got %v", err)
	}
	if res.MetricsExported != 2 {
		t.Fatalf("want 2 lines from the two 30m halves (stalled attempt discarded), got %d", res.MetricsExported)
	}
}

// A slow but steady stream is longer than the idle timeout in total, but never idle: it must not be cut.
func TestExport_SlowSteadyStreamIsNotCut(t *testing.T) {
	shortIdleTimeout(t, time.Second)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 6; i++ {
			fmt.Fprintf(w, `{"metric":{"__name__":"vm_x","job":"vmstorage","i":"%d"},"values":[1],"timestamps":[1]}`+"\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(250 * time.Millisecond)
		}
	}))
	defer srv.Close()

	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), oneHourConfig(srv.URL, t.TempDir()))
	if err != nil {
		t.Fatalf("steady stream was cut: %v", err)
	}
	if res.MetricsExported != 6 {
		t.Fatalf("want 6 lines, got %d", res.MetricsExported)
	}
}

// No response at all (dead or hung upstream) fails after one idle period, without many splits.
func TestExport_NoResponseFailsFast(t *testing.T) {
	shortIdleTimeout(t, 500*time.Millisecond)
	var calls int32
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = r.ParseForm() // read the body, so the server notices the client hanging up
		<-r.Context().Done()
	}))
	defer srv.Close()

	start := time.Now()
	_, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), oneHourConfig(srv.URL, t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "no response from VictoriaMetrics") {
		t.Fatalf("want a no-response error, got %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("watchdog error must not look like a user cancel: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("want one request and a fast failure, got %d requests in %s", n, time.Since(start))
	}
}

// A user cancel during a stalled stream stays a cancel (the job manager marks the job canceled).
func TestExport_UserCancelDuringStallIsCancel(t *testing.T) {
	shortIdleTimeout(t, time.Minute)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, exportLineFor(r))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	_, err := NewExportService(t.TempDir(), "test").ExecuteExport(ctx, oneHourConfig(srv.URL, t.TempDir()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// Transient errors are retried at most len(transientRetryDelays) times, then the export fails with the cause.
func TestExport_TransientRetriesStop(t *testing.T) {
	fastRetries(t)
	var calls int32
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "no healthy upstream", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), oneHourConfig(srv.URL, t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want a 503 error after retries, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 4 {
		t.Fatalf("want 1 attempt + 3 retries, got %d requests", n)
	}
}

// A 503 whose body says the query timed out is a query timeout (split by time), not a transient error.
func TestExport_GatewayTimeoutBodyIsSplit(t *testing.T) {
	fastRetries(t)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end := vmWindow(t, r)
		if end.Sub(start) > 30*time.Minute {
			http.Error(w, "timeout exceeded during query execution: 30.000 seconds", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, exportLineFor(r))
	}))
	defer srv.Close()

	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), oneHourConfig(srv.URL, t.TempDir()))
	if err != nil {
		t.Fatalf("want split to succeed, got %v", err)
	}
	if res.MetricsExported != 2 {
		t.Fatalf("want 2 lines from two halves, got %d", res.MetricsExported)
	}
}

// Cancel during a backoff wait returns quickly as a cancel.
func TestExport_CancelDuringBackoff(t *testing.T) {
	orig := transientRetryDelays
	transientRetryDelays = []time.Duration{time.Hour}
	t.Cleanup(func() { transientRetryDelays = orig })
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := NewExportService(t.TempDir(), "test").ExecuteExport(ctx, oneHourConfig(srv.URL, t.TempDir()))
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("want a fast cancel, got %v after %s", err, time.Since(start))
	}
}

// The connection breaks after one line; the retry must not duplicate that line.
func TestExport_MidStreamBreakIsRetriedWithoutDuplicates(t *testing.T) {
	for _, obfuscate := range []bool{false, true} {
		t.Run(fmt.Sprintf("obfuscation=%v", obfuscate), func(t *testing.T) {
			fastRetries(t)
			var calls int32
			srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"metric":{"__name__":"vm_x","job":"vmstorage","instance":"10.0.0.1:8482"},"values":[1],"timestamps":[1]}`+"\n")
				if atomic.AddInt32(&calls, 1) == 1 {
					w.(http.Flusher).Flush()
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close() // break the chunked stream after one line
					return
				}
				fmt.Fprint(w, `{"metric":{"__name__":"vm_x","job":"vmstorage","instance":"10.0.0.2:8482"},"values":[1],"timestamps":[1]}`+"\n")
			}))
			defer srv.Close()

			cfg := oneHourConfig(srv.URL, t.TempDir())
			cfg.Obfuscation = domain.ObfuscationConfig{Enabled: obfuscate, ObfuscateInstance: true}
			res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
			if err != nil {
				t.Fatalf("export failed: %v", err)
			}
			lines := readArchiveMetrics(t, res.ArchivePath)
			instances := map[interface{}]bool{}
			for _, l := range lines {
				instances[l["metric"].(map[string]interface{})["instance"]] = true
			}
			if len(lines) != 2 || len(instances) != 2 {
				t.Fatalf("want 2 lines with 2 instances, got %d lines %v", len(lines), instances)
			}
		})
	}
}

// All batches are in staging but creating the archive failed. Resume must only build the archive.
func TestExport_ResumeAfterArchiveFailureDoesNotDuplicate(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, exportLineFor(r))
	}))
	defer srv.Close()

	base := t.TempDir()
	outputDir := filepath.Join(base, "out")
	if err := os.WriteFile(outputDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &exportServiceImpl{clientFactory: vm.NewClient, archiveWriter: archive.NewWriter(outputDir), vmGatherVersion: "test"}
	cfg := regressionConfig(srv.URL, t.TempDir()) // 2 batches
	state := &ResumeState{}
	ctx := WithResumeState(context.Background(), state)

	if _, err := svc.ExecuteExport(ctx, cfg); err == nil {
		t.Fatal("expected archive failure")
	}
	if err := os.Remove(outputDir); err != nil {
		t.Fatal(err)
	}
	cfg.ResumeFromBatch = 2 // both batches completed
	res, err := svc.ExecuteExport(ctx, cfg)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if lines := readArchiveMetrics(t, res.ArchivePath); len(lines) != 2 || res.MetricsExported != 2 {
		t.Fatalf("want 2 lines and count 2, got %d lines, count %d", len(lines), res.MetricsExported)
	}
}

// A batch was partly appended to staging (for example the disk filled up). Resume must cut it off.
func TestExport_ResumeTruncatesPartialAppend(t *testing.T) {
	var failSecond atomic.Bool
	failSecond.Store(true)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if start, _ := vmWindow(t, r); start.Minute() != 0 && failSecond.Load() {
			http.Error(w, "cannot process request", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, exportLineFor(r))
	}))
	defer srv.Close()

	svc := NewExportService(t.TempDir(), "test")
	cfg := regressionConfig(srv.URL, t.TempDir())
	state := &ResumeState{}
	ctx := WithResumeState(context.Background(), state)
	if _, err := svc.ExecuteExport(ctx, cfg); err == nil {
		t.Fatal("expected failure on batch 2")
	}
	f, err := os.OpenFile(cfg.StagingFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"metric":{"__name__":"half`)
	_ = f.Close()

	failSecond.Store(false)
	cfg.ResumeFromBatch = 1
	res, err := svc.ExecuteExport(ctx, cfg)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if lines := readArchiveMetrics(t, res.ArchivePath); len(lines) != 2 {
		t.Fatalf("want 2 valid lines, got %d", len(lines))
	}
	// The archive holds everything: a resumed export must not leave its raw staging file behind.
	if _, err := os.Stat(cfg.StagingFile); !os.IsNotExist(err) {
		t.Fatalf("staging file left after the resumed export: %v", err)
	}
}

// The first batch was empty (0 bytes in staging), then a later batch was partly appended.
// Resume must still cut the broken tail.
func TestExport_ResumeTruncatesPartialAppendAfterEmptyBatch(t *testing.T) {
	var failSecond atomic.Bool
	failSecond.Store(true)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if start, _ := vmWindow(t, r); start.Minute() == 0 {
			return // first window has no data
		}
		if failSecond.Load() {
			http.Error(w, "cannot process request", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, exportLineFor(r))
	}))
	defer srv.Close()

	svc := NewExportService(t.TempDir(), "test")
	cfg := regressionConfig(srv.URL, t.TempDir())
	ctx := WithResumeState(context.Background(), &ResumeState{})
	if _, err := svc.ExecuteExport(ctx, cfg); err == nil {
		t.Fatal("expected failure on batch 2")
	}
	f, err := os.OpenFile(cfg.StagingFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"metric":{"__name__":"half`)
	_ = f.Close()

	failSecond.Store(false)
	cfg.ResumeFromBatch = 1
	res, err := svc.ExecuteExport(ctx, cfg)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if lines := readArchiveMetrics(t, res.ArchivePath); len(lines) != 1 {
		t.Fatalf("want 1 valid line, got %d", len(lines))
	}
}

// A proxy (nginx, vmauth) answers 504 when vmselect needs longer than the proxy timeout for a window.
// Retrying the same window cannot help; a smaller window can.
func TestExport_GatewayTimeoutIsSplitByTime(t *testing.T) {
	fastRetries(t)
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end := vmWindow(t, r)
		if end.Sub(start) > 30*time.Minute {
			http.Error(w, "<html><body><h1>504 Gateway Time-out</h1></body></html>", http.StatusGatewayTimeout)
			return
		}
		_, _ = fmt.Fprint(w, exportLineFor(r))
	}))
	defer srv.Close()

	res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), oneHourConfig(srv.URL, t.TempDir()))
	if err != nil {
		t.Fatalf("want split to succeed, got %v", err)
	}
	if res.MetricsExported != 2 {
		t.Fatalf("want 2 lines from two halves, got %d", res.MetricsExported)
	}
}

// VictoriaMetrics v1.155.0 writes up to 8192 rows per line with reduce_mem_usage=1 and prints floats in
// full ('f' format): a series with very large values produced 2.6 MB lines in a live export, which the
// 1 MB line limit rejected with "token too long".
func TestExport_LongLinesFromVictoriaMetrics(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"metric":{"__name__":"m","job":"vmstorage"},"values":[`)
	for i := 0; i < 8192; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(1.2345678901234567e300, 'f', -1, 64))
	}
	b.WriteString(`],"timestamps":[`)
	for i := 0; i < 8192; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(int64(1767261600000+i*1000), 10))
	}
	b.WriteString("]}\n")
	line := b.String()
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, line) }))
	defer srv.Close()

	for _, obfuscate := range []bool{false, true} {
		cfg := oneHourConfig(srv.URL, t.TempDir())
		cfg.Obfuscation = domain.ObfuscationConfig{Enabled: obfuscate, ObfuscateInstance: true}
		res, err := NewExportService(t.TempDir(), "test").ExecuteExport(context.Background(), cfg)
		if err != nil {
			t.Fatalf("obfuscation=%v: a %d-byte line failed: %v", obfuscate, len(line), err)
		}
		if lines := readArchiveMetrics(t, res.ArchivePath); len(lines) != 1 || res.MetricsExported != 1 {
			t.Fatalf("obfuscation=%v: want the long line once, got %d lines, count %d", obfuscate, len(lines), res.MetricsExported)
		}
	}
}

// A resume needs the progress of the failed run; without it the count and the staging file would be wrong.
func TestExport_ResumeWithoutRecordedProgressIsRejected(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, exportLineFor(r)) }))
	defer srv.Close()
	cfg := regressionConfig(srv.URL, t.TempDir())
	cfg.ResumeFromBatch = 1
	for name, ctx := range map[string]context.Context{
		"no state":    context.Background(),
		"empty state": WithResumeState(context.Background(), &ResumeState{}),
	} {
		if _, err := NewExportService(t.TempDir(), "test").ExecuteExport(ctx, cfg); err == nil || !strings.Contains(err.Error(), "no progress was recorded") {
			t.Fatalf("%s: want a resume error, got %v", name, err)
		}
	}
}
