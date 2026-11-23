package snapshot

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/actions"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
)

// fakeVM serves /api/v1/export with VictoriaMetrics semantics: start and end are inclusive.
type fakeVM struct {
	mu       sync.Mutex
	series   []fakeSeries
	failMain bool // fail every export except the vm_app_version priming request
	block    chan struct{}
}

type fakeSeries struct {
	labels  map[string]string
	samples []int64 // timestamps in ms
}

func (f *fakeVM) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	selector := r.Form.Get("match[]")
	start := parseVMTime(r.Form.Get("start"))
	end := parseVMTime(r.Form.Get("end"))
	priming := strings.HasPrefix(selector, "vm_app_version")
	f.mu.Lock()
	failMain, block := f.failMain, f.block
	series := append([]fakeSeries(nil), f.series...)
	f.mu.Unlock()
	if !priming && block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if !priming && failMain {
		http.Error(w, "boom", http.StatusBadRequest)
		return
	}
	for _, s := range series {
		if priming && s.labels["__name__"] != "vm_app_version" {
			continue
		}
		var ts []int64
		for _, t := range s.samples {
			if t >= start && t <= end {
				ts = append(ts, t)
			}
		}
		if len(ts) == 0 {
			continue
		}
		values := make([]float64, len(ts))
		line, _ := json.Marshal(map[string]any{"metric": s.labels, "values": values, "timestamps": ts})
		_, _ = w.Write(append(line, '\n'))
	}
}

func parseVMTime(s string) int64 {
	f, _ := strconv.ParseFloat(s, 64)
	return int64(math.Round(f * 1000))
}

func ms(t time.Time) int64 { return t.UnixMilli() }

type testEnv struct {
	vm     *fakeVM
	server *httptest.Server
	root   string
	dst    string
	cfg    Config
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	base := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	var samples []int64
	for ts := base; ts.Before(base.Add(3 * time.Hour)); ts = ts.Add(30 * time.Minute) {
		samples = append(samples, ms(ts))
	}
	vm := &fakeVM{series: []fakeSeries{
		{labels: map[string]string{"__name__": "vm_app_version", "job": "vmstorage", "instance": "a:8482", "version": "vmstorage-20261106-v1.156.0"}, samples: samples},
		{labels: map[string]string{"__name__": "vm_rows", "job": "vmstorage", "instance": "a:8482"}, samples: samples},
		{labels: map[string]string{"__name__": "node_load1", "job": "vmstorage", "instance": "b:9100"}, samples: samples},
	}}
	srv := httptest.NewServer(http.HandlerFunc(vm.handler))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	env := &testEnv{vm: vm, server: srv, root: root, dst: "fs://" + filepath.Join(root, "bucket", "prefix")}
	env.cfg = Config{
		Export: domain.ExportConfig{
			Connection: domain.VMConnection{URL: srv.URL, Auth: domain.AuthConfig{Type: domain.AuthTypeNone}},
			Jobs:       []string{"vmstorage"},
		},
		Dst:        env.dst,
		Period:     time.Hour,
		Lag:        5 * time.Minute,
		MaxCatchUp: 24,
		WorkDir:    filepath.Join(root, "work"),
		Version:    "test",
	}
	return env
}

func (e *testEnv) runner(t *testing.T, cfg Config, now string) *Runner {
	t.Helper()
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nowT := mustTime(t, now)
	r.now = func() time.Time { return nowT }
	return r
}

func (e *testEnv) state(t *testing.T) state {
	t.Helper()
	fs, err := actions.NewRemoteFS(context.Background(), e.dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.MustStop()
	st, err := stateStore{fs: fs}.load()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// snapshots returns the names of completed snapshot directories.
func (e *testEnv) snapshots(t *testing.T) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(e.root, "bucket", "prefix"))
	var names []string
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(e.root, "bucket", "prefix", en.Name(), "backup_complete.ignore")); err == nil {
			names = append(names, en.Name())
		}
	}
	sort.Strings(names)
	return names
}

// bundleLines reads the bundle of a snapshot through the RemoteFS API and returns its metrics lines.
// It does not use actions.Restore: its final fsync fails on Windows (see TestBackupUploaderRoundTripWithRestore).
func (e *testEnv) bundleLines(t *testing.T, name string) []map[string]any {
	t.Helper()
	bundle := readRemoteFile(t, joinURL(e.dst, name), bundleFilename)
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	for _, f := range zr.File {
		if f.Name != "metrics.jsonl" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(rc)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, m)
		}
		_ = rc.Close()
	}
	return lines
}

func timestampsOf(lines []map[string]any, name string) []int64 {
	var out []int64
	for _, l := range lines {
		if l["metric"].(map[string]any)["__name__"] != name {
			continue
		}
		for _, ts := range l["timestamps"].([]any) {
			out = append(out, int64(ts.(float64)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestRunnerScheduledRuns(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	if err := e.runner(t, e.cfg, "2026-11-21T10:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.runner(t, e.cfg, "2026-11-21T11:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// A run before the next window is complete does nothing.
	if err := e.runner(t, e.cfg, "2026-11-21T11:30:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	got := e.snapshots(t)
	want := []string{"20261121T090000Z_20261121T100000Z", "20261121T100000Z_20261121T110000Z"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("snapshots = %v; want %v", got, want)
	}
	if st := e.state(t); !st.LastEnd.Equal(mustTime(t, "2026-11-21T11:00:00Z")) || st.LastSnapshot != want[1] {
		t.Fatalf("state = %+v", st)
	}

	// Half-open windows: the sample at exactly 10:00:00 is only in the second snapshot.
	first := e.bundleLines(t, want[0])
	second := e.bundleLines(t, want[1])
	t9 := ms(mustTime(t, "2026-11-21T09:00:00Z"))
	t930 := ms(mustTime(t, "2026-11-21T09:30:00Z"))
	t10 := ms(mustTime(t, "2026-11-21T10:00:00Z"))
	t1030 := ms(mustTime(t, "2026-11-21T10:30:00Z"))
	if got := timestampsOf(first, "vm_rows"); fmt.Sprint(got) != fmt.Sprint([]int64{t9, t930}) {
		t.Fatalf("first window vm_rows timestamps = %v", got)
	}
	if got := timestampsOf(second, "vm_rows"); fmt.Sprint(got) != fmt.Sprint([]int64{t10, t1030}) {
		t.Fatalf("second window vm_rows timestamps = %v", got)
	}
	// The MDX filter dropped the non-VictoriaMetrics instance of the same job.
	for _, l := range append(first, second...) {
		labels := l["metric"].(map[string]any)
		if labels["instance"] != "a:8482" || labels["victoriametrics_app"] != "true" {
			t.Fatalf("unexpected series in snapshot: %v", labels)
		}
	}
}

func TestRunnerFailedWindowKeepsState(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	if err := e.runner(t, e.cfg, "2026-11-21T10:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	e.vm.mu.Lock()
	e.vm.failMain = true
	e.vm.mu.Unlock()
	if err := e.runner(t, e.cfg, "2026-11-21T11:07:00Z").RunOnce(ctx); err == nil {
		t.Fatal("want an error from the failing export")
	}
	if st := e.state(t); !st.LastEnd.Equal(mustTime(t, "2026-11-21T10:00:00Z")) {
		t.Fatalf("state moved after a failed window: %+v", st)
	}
	if got := e.snapshots(t); len(got) != 1 {
		t.Fatalf("snapshots = %v; want only the first one", got)
	}
	e.vm.mu.Lock()
	e.vm.failMain = false
	e.vm.mu.Unlock()
	if err := e.runner(t, e.cfg, "2026-11-21T11:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.snapshots(t); len(got) != 2 {
		t.Fatalf("the failed window was not retried: %v", got)
	}
}

func TestRunnerCanceledRunLeavesNoSnapshot(t *testing.T) {
	e := newTestEnv(t)
	e.vm.block = make(chan struct{})
	defer close(e.vm.block)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	if err := e.runner(t, e.cfg, "2026-11-21T10:07:00Z").RunOnce(ctx); err == nil {
		t.Fatal("want an error from the canceled run")
	}
	if got := e.snapshots(t); len(got) != 0 {
		t.Fatalf("canceled run left completed snapshots %v", got)
	}
	if st := e.state(t); !st.LastEnd.IsZero() {
		t.Fatalf("canceled run wrote state %+v", st)
	}
	// RunOnce empties the whole work dir, also after a cancel.
	if entries, err := os.ReadDir(e.cfg.WorkDir); err != nil || len(entries) != 0 {
		t.Fatalf("work dir after cancel: %v entries, err %v", len(entries), err)
	}
}

func TestRunnerBackfillDoesNotTouchState(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	if err := e.runner(t, e.cfg, "2026-11-21T12:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	before := e.state(t)

	cfg := e.cfg
	cfg.Start = mustTime(t, "2026-11-21T09:00:00Z")
	cfg.End = mustTime(t, "2026-11-21T11:00:00Z")
	cfg.MaxCatchUp = 1 // ignored by a backfill: the whole range is exported in one run
	if err := e.runner(t, cfg, "2026-11-21T12:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after := e.state(t)
	if !after.LastEnd.Equal(before.LastEnd) || after.LastSnapshot != before.LastSnapshot {
		t.Fatalf("backfill changed the state: before %+v, after %+v", before, after)
	}
	got := e.snapshots(t)
	want := []string{"20261121T090000Z_20261121T100000Z", "20261121T100000Z_20261121T110000Z", "20261121T110000Z_20261121T120000Z"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("snapshots = %v; want %v", got, want)
	}
}

func TestRunnerDiscoversJobsPerWindow(t *testing.T) {
	e := newTestEnv(t)
	cfg := e.cfg
	cfg.Export.Jobs = nil
	r := e.runner(t, cfg, "2026-11-21T10:07:00Z")
	var gotRange domain.TimeRange
	r.discoverJobs = func(_ context.Context, _ domain.VMConnection, tr domain.TimeRange) ([]string, []string, error) {
		gotRange = tr
		return []string{"vmstorage"}, []string{"vmstorage"}, nil
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !gotRange.Start.Equal(mustTime(t, "2026-11-21T09:00:00Z")) || !gotRange.End.Equal(mustTime(t, "2026-11-21T10:00:00Z")) {
		t.Fatalf("discovery used %v; want the window", gotRange)
	}
	if len(e.snapshots(t)) != 1 {
		t.Fatalf("no snapshot after discovery")
	}
}

func TestRunnerCleansLeftovers(t *testing.T) {
	e := newTestEnv(t)
	leftover := filepath.Join(e.cfg.WorkDir, "20261121090000-18EA0BC37D938C91", "vmgather.zip")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("partial"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := e.runner(t, e.cfg, "2026-11-21T10:07:00Z").RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover of a killed run was not removed: %v", err)
	}
}

// Windows without MDX series (no components yet, or no data) are not uploaded, but they must not block
// later windows.
func TestRunnerEmptyWindowsDoNotBlock(t *testing.T) {
	e := newTestEnv(t)
	cfg := e.cfg
	cfg.Export.Jobs = nil
	cfg.Start = mustTime(t, "2026-11-21T07:00:00Z") // the fake VM has data from 09:00
	r := e.runner(t, cfg, "2026-11-21T10:07:00Z")
	r.discoverJobs = func(_ context.Context, _ domain.VMConnection, tr domain.TimeRange) ([]string, []string, error) {
		if tr.Start.Before(mustTime(t, "2026-11-21T09:00:00Z")) {
			return nil, nil, nil
		}
		return []string{"vmstorage"}, []string{"vmstorage"}, nil
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.snapshots(t); fmt.Sprint(got) != fmt.Sprint([]string{"20261121T090000Z_20261121T100000Z"}) {
		t.Fatalf("snapshots = %v; want only the window with data", got)
	}
	if st := e.state(t); !st.LastEnd.Equal(mustTime(t, "2026-11-21T10:00:00Z")) {
		t.Fatalf("state = %+v; want it past the empty windows", st)
	}

	// Explicit jobs and a window without samples behave the same way.
	cfg = e.cfg
	cfg.Dst = e.dst + "-jobs"
	cfg.Start = mustTime(t, "2026-11-21T08:00:00Z")
	cfg.End = mustTime(t, "2026-11-21T09:00:00Z")
	if err := e.runner(t, cfg, "2026-11-21T10:07:00Z").RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "bucket", "prefix-jobs", "20261121T080000Z_20261121T090000Z")); !os.IsNotExist(err) {
		t.Fatalf("an empty window was uploaded: %v", err)
	}
}

func TestRunnerRejectsStateInTheFuture(t *testing.T) {
	e := newTestEnv(t)
	if err := e.runner(t, e.cfg, "2026-11-21T12:07:00Z").RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := e.runner(t, e.cfg, "2026-11-21T09:30:00Z").RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "after the current time") {
		t.Fatalf("want a clock error, got %v", err)
	}
}

// A failed backfill is restarted from the first window that is not uploaded yet.
func TestRunnerBackfillRestartSkipsUploadedWindows(t *testing.T) {
	e := newTestEnv(t)
	cfg := e.cfg
	cfg.Start = mustTime(t, "2026-11-21T09:00:00Z")
	cfg.End = mustTime(t, "2026-11-21T11:00:00Z")
	r := e.runner(t, cfg, "2026-11-21T12:07:00Z")
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.vm.mu.Lock()
	e.vm.failMain = true // a rerun that exported again would fail now
	e.vm.mu.Unlock()
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("restart re-exported uploaded windows: %v", err)
	}
}

func TestRunnerRejectsBackfillEndInTheFuture(t *testing.T) {
	e := newTestEnv(t)
	cfg := e.cfg
	cfg.Start = mustTime(t, "2026-11-21T09:00:00Z")
	cfg.End = mustTime(t, "2026-11-21T11:00:00Z")
	if err := e.runner(t, cfg, "2026-11-21T11:03:00Z").RunOnce(context.Background()); err == nil {
		t.Fatal("want an error for an end closer to now than the lag")
	}
}

func TestConfigValidate(t *testing.T) {
	ok := Config{Dst: "fs:///tmp/x", Period: time.Hour, MaxCatchUp: 1, WorkDir: "/tmp/w"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Config{
		{Period: time.Hour, MaxCatchUp: 1, WorkDir: "/w"},
		{Dst: "fs:///x", MaxCatchUp: 1, WorkDir: "/w"},
		// Snapshot names have second precision: shorter or fractional periods would collide.
		{Dst: "fs:///x", Period: 100 * time.Millisecond, MaxCatchUp: 1, WorkDir: "/w"},
		{Dst: "fs:///x", Period: 30 * time.Second, MaxCatchUp: 1, WorkDir: "/w"},
		{Dst: "fs:///x", Period: 90*time.Second + 500*time.Millisecond, MaxCatchUp: 1, WorkDir: "/w"},
		{Dst: "fs:///x", Period: time.Hour, WorkDir: "/w"},
		{Dst: "fs:///x", Period: time.Hour, MaxCatchUp: 1, WorkDir: "/w", End: time.Now()},
		{Dst: "fs:///x", Period: time.Hour, MaxCatchUp: 1, WorkDir: "/w", Start: time.Now(), End: time.Now().Add(-time.Hour)},
		{Dst: "fs:///x", Period: time.Hour, MaxCatchUp: 1, WorkDir: "/w", Export: domain.ExportConfig{Mode: domain.ExportModeCustom}},
		{Dst: "fs:///x", Period: time.Hour, WorkDir: "/w", Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 1, 30, 0, 0, time.UTC)},
	}
	for i, c := range bad {
		if err := c.validate(); err == nil {
			t.Fatalf("config %d must be rejected: %+v", i, c)
		}
	}
}

// Retention deletes old snapshots, complete or not, and keeps recent ones and the state.
func TestRunnerRetention(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	cfg := e.cfg
	cfg.Start = mustTime(t, "2026-11-21T09:00:00Z")
	if err := e.runner(t, cfg, "2026-11-21T11:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// An upload that never finished: parts without backup_complete.ignore.
	orphan := filepath.Join(e.root, "bucket", "prefix", "20261121T050000Z_20261121T060000Z", "vmgather.zip")
	if err := os.MkdirAll(orphan, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "0000000000000003_0000000000000000_0000000000000003"), []byte("abc"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got := len(e.snapshots(t)); got != 2 {
		t.Fatalf("setup: %d snapshots", got)
	}

	cfg.Retention = 90 * time.Minute // keeps windows that ended after 11:37 at 13:07
	if err := e.runner(t, cfg, "2026-11-21T13:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(e.root, "bucket", "prefix"))
	var left []string
	for _, en := range entries {
		left = append(left, en.Name())
	}
	// 12:00-13:00 has no samples in the fake VM, so it is not uploaded at all.
	want := []string{"20261121T110000Z_20261121T120000Z", stateFilename}
	if fmt.Sprint(left) != fmt.Sprint(want) {
		t.Fatalf("left after retention: %v; want %v", left, want)
	}

	// A backfill never deletes anything.
	bf := e.cfg
	bf.Retention = time.Minute
	bf.Start = mustTime(t, "2026-11-21T09:00:00Z")
	bf.End = mustTime(t, "2026-11-21T10:00:00Z")
	if err := e.runner(t, bf, "2026-11-21T13:07:00Z").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(e.snapshots(t)); got != 2 {
		t.Fatalf("backfill applied retention: %d snapshots left", got)
	}
}

// A failed run must not leave files on the local disk (server mode or a host without emptyDir).
func TestRunnerFailedRunLeavesNoLocalFiles(t *testing.T) {
	e := newTestEnv(t)
	e.vm.mu.Lock()
	e.vm.failMain = true
	e.vm.mu.Unlock()
	if err := e.runner(t, e.cfg, "2026-11-21T10:07:00Z").RunOnce(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	var files []string
	_ = filepath.Walk(e.cfg.WorkDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if len(files) != 0 {
		t.Fatalf("files left after a failed run: %v", files)
	}
}

// Snapshot mode uses the obfuscation of the config. The MDX filter decides on the original labels, so
// obfuscation does not change which series are kept. Instance and job are digests in the metric labels and
// in metadata.json; component types (vmstorage, vmselect) are not obfuscated and are not checked here.
func TestRunnerObfuscatesLabels(t *testing.T) {
	e := newTestEnv(t)
	cfg := e.cfg
	cfg.Export.Obfuscation = domain.ObfuscationConfig{Enabled: true, ObfuscateInstance: true, ObfuscateJob: true}
	if err := e.runner(t, cfg, "2026-11-21T10:07:00Z").RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	name := "20261121T090000Z_20261121T100000Z"
	lines := e.bundleLines(t, name)
	if len(lines) == 0 {
		t.Fatal("empty bundle")
	}
	names := map[string]bool{}
	for _, l := range lines {
		m := l["metric"].(map[string]any)
		names[m["__name__"].(string)] = true
		if m["instance"] != obfuscation.Value("a:8482") || m["job"] != obfuscation.Value("vmstorage") {
			t.Fatalf("labels not obfuscated: %v", m)
		}
	}
	if !names["vm_rows"] || names["node_load1"] {
		t.Fatalf("MDX filter with obfuscation kept %v; want vm_rows and no node_load1 of b:9100", names)
	}
	bundle := readRemoteFile(t, joinURL(e.dst, name), bundleFilename)
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		for _, raw := range []string{"a:8482", `"vmstorage"`} {
			if strings.Contains(string(data), raw) {
				t.Fatalf("%s contains the real value %s", f.Name, raw)
			}
		}
	}
}

// A first scheduled run without state and without -snapshot.start reads the newest complete window. If it
// is empty, the setup is most likely wrong (URL, tenant, jobs): the run fails and the state stays empty.
// With -snapshot.start an empty first window is normal (data starts later) and the state moves on.
func TestRunnerFirstRunWithoutDataFails(t *testing.T) {
	e := newTestEnv(t)
	cfg := e.cfg
	cfg.Export.Jobs = nil
	r := e.runner(t, cfg, "2026-11-21T10:07:00Z")
	r.discoverJobs = func(context.Context, domain.VMConnection, domain.TimeRange) ([]string, []string, error) {
		return nil, nil, nil // wrong tenant: no vm_app_version at all
	}
	err := r.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no MDX series in the newest complete window") {
		t.Fatalf("want an error for an empty first window, got %v", err)
	}
	if st := e.state(t); !st.LastEnd.IsZero() {
		t.Fatalf("state was saved: %+v", st)
	}

	cfg.Start = mustTime(t, "2026-11-21T09:00:00Z")
	r = e.runner(t, cfg, "2026-11-21T10:07:00Z")
	r.discoverJobs = func(context.Context, domain.VMConnection, domain.TimeRange) ([]string, []string, error) {
		return nil, nil, nil
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("with -snapshot.start an empty window must not fail: %v", err)
	}
	if st := e.state(t); !st.LastEnd.Equal(mustTime(t, "2026-11-21T10:00:00Z")) {
		t.Fatalf("state = %+v; want it past the empty window", st)
	}
}
