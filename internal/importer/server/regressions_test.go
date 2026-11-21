package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeBundleFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeVM struct {
	mu       sync.Mutex
	imported bytes.Buffer
	srv      *httptest.Server
}

func newFakeVM(t *testing.T, retention string) *fakeVM {
	t.Helper()
	f := &fakeVM{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			f.mu.Lock()
			_, _ = io.Copy(&f.imported, r.Body)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			fmt.Fprintf(w, `{"status":"success","data":{"retentionTime":%q}}`, retention)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"m"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeVM) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.imported.String()
}

// "Shift to now" is offered when bundle data is older than the target retention. The shift must be
// applied before retention trimming, and drop_old=false must disable trimming on import like on analyze.
func TestImport_TimeShiftRescuesSamplesOlderThanRetention(t *testing.T) {
	day := int64(24 * time.Hour / time.Millisecond)
	now := time.Now().UnixMilli()
	line := fmt.Sprintf(`{"metric":{"__name__":"m","job":"j"},"values":[1,2],"timestamps":[%d,%d]}`+"\n", now-2*day, now-2*day+60000)

	for _, dropOld := range []bool{true, false} {
		vm := newFakeVM(t, "1d")
		s := newServer("t", "")
		cfg := uploadConfig{Endpoint: vm.srv.URL, DropOld: dropOld, TimeShiftMs: 2 * day}
		job := s.newJob(int64(len(line)))
		s.storeJob(job)
		importURL, queryURL, err := resolveEndpoints(cfg)
		if err != nil {
			t.Fatal(err)
		}
		s.runImportJob(context.Background(), job, cfg, writeBundleFile(t, line), "bundle.jsonl", importURL, queryURL, 0)
		snap, _ := s.getJobSnapshot(job.ID)
		if snap.State != jobStateCompleted || snap.Summary == nil || snap.Summary.Points != 2 {
			points, dropped := -1, -1
			if snap.Summary != nil {
				points, dropped = snap.Summary.Points, snap.Summary.DroppedOld
			}
			t.Errorf("drop_old=%v shift=+2d: want completed with 2 points, got state=%s points=%d droppedOld=%d sent=%q",
				dropOld, snap.State, points, dropped, vm.body())
		}
	}
}

// /api/v1/export writes NaN as null and +-Inf as "Infinity"/"-Infinity"; /api/v1/import accepts them.
// One special value must not drop the whole series line.
func TestImport_KeepsSpecialValuesWrittenByExport(t *testing.T) {
	vm := newFakeVM(t, "")
	now := time.Now().UnixMilli()
	var b strings.Builder
	specials := []string{`null`, `"Infinity"`, `"-Infinity"`, `"NaN"`, `3`}
	for i, v := range specials {
		fmt.Fprintf(&b, `{"metric":{"__name__":"m","i":"%d"},"values":[1,%s],"timestamps":[%d,%d]}`+"\n", i, v, now-1000, now)
	}
	s := newServer("t", "")
	_, sum, err := s.streamImport(context.Background(), uploadConfig{}, &bundleInfo{MetricsPath: writeBundleFile(t, b.String())},
		vm.srv.URL+"/api/v1/import", 0, 0, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedLines != 0 || sum.Points != 2*len(specials) {
		t.Fatalf("want 0 skipped and %d points, got skipped=%d points=%d; sent=%q", 2*len(specials), sum.SkippedLines, sum.Points, vm.body())
	}
	// "NaN" and null are the same value for VM import (NaN); export writes null.
	sent := vm.body()
	for i, want := range []string{`,null]`, `,"Infinity"]`, `,"-Infinity"]`, `,null]`, `,3]`} {
		line := fmt.Sprintf(`"i":"%d"},"values":[1%s`, i, want)
		if !strings.Contains(sent, line) {
			t.Errorf("line %d: want values ending with %s; sent=%q", i, want, sent)
		}
	}
}

// Verification reads /api/v1/series for the first imported series. kube-state-metrics style metrics
// easily return more than 64KB there; a valid answer must not be reported as an invalid payload.
func TestImport_VerificationAcceptsLargeSeriesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`{"status":"success","data":[`)
		for i := 0; i < 1000; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"__name__":"kube_pod_status_phase","job":"kube-state-metrics","namespace":"ns-%d","pod":"pod-%04d-abcdefabcdef","phase":"Running"}`, i%50, i)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	s := newServer("t", "")
	sum := importSummary{MetricName: "kube_pod_status_phase", Labels: map[string]string{"job": "kube-state-metrics"}, Start: time.Now().Add(-time.Hour), End: time.Now()}
	res := s.verifyImport(context.Background(), uploadConfig{}, sum, srv.URL+"/api/v1/query")
	if !res.Verified {
		t.Fatalf("want verified, got message: %.200s", res.Message)
	}
}

// A page on another origin can POST multipart/form-data without a CORS preflight. vmimporter must
// reject it, otherwise any web site can push data into a VictoriaMetrics reachable from the browser.
func TestRouter_RejectsCrossOriginUpload(t *testing.T) {
	vm := newFakeVM(t, "")
	s := newServer("t", "")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("config", fmt.Sprintf(`{"endpoint":%q}`, vm.srv.URL))
	fw, _ := mw.CreateFormFile("bundle", "bundle.jsonl")
	fmt.Fprintf(fw, `{"metric":{"__name__":"injected"},"values":[1],"timestamps":[%d]}`+"\n", time.Now().UnixMilli())
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "http://localhost:8081/api/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	s.Router().ServeHTTP(rr, req)
	time.Sleep(300 * time.Millisecond)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin upload: want 403, got %d; data received by VM: %q", rr.Code, vm.body())
	}
}

// BenchmarkStreamImport measures importer throughput on VM-export-like lines (300 samples per line).
func BenchmarkStreamImport(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	now := time.Now().UnixMilli()
	var sb strings.Builder
	for i := 0; sb.Len() < 40<<20; i++ {
		fmt.Fprintf(&sb, `{"metric":{"__name__":"vm_rows_total","job":"vmstorage","instance":"10.0.0.%d:8482","type":"indexdb/inmemory"},"values":[`, i%250)
		for j := 0; j < 300; j++ {
			if j > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%d.%d", 123456+j, j%10)
		}
		sb.WriteString(`],"timestamps":[`)
		for j := 0; j < 300; j++ {
			if j > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%d", now-int64(300-j)*1000)
		}
		sb.WriteString("]}\n")
	}
	p := filepath.Join(b.TempDir(), "bundle.jsonl")
	if err := os.WriteFile(p, []byte(sb.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	s := newServer("t", "")
	b.SetBytes(int64(sb.Len()))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.streamImport(context.Background(), uploadConfig{}, &bundleInfo{MetricsPath: p}, srv.URL+"/api/v1/import", 0, 0, 0, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNormalizeValues(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{in: `1`, want: `1`},
		{in: `-1.5e3`, want: `-1.5e3`},
		{in: `null`, want: `null`},
		{in: `"Infinity"`, want: `"Infinity"`},
		{in: `"-Infinity"`, want: `"-Infinity"`},
		{in: `"+Inf"`, want: `"Infinity"`},
		{in: `"NaN"`, want: `null`},
		{in: `"null"`, want: `null`},
		{in: `"1.25"`, want: `1.25`},
		{in: `true`, want: `1`},
		{in: `false`, want: `0`},
		{in: `"abc"`, err: true},
		{in: `{}`, err: true},
		{in: `[1]`, err: true},
	}
	for _, tc := range cases {
		got, err := normalizeValues([]json.RawMessage{json.RawMessage(tc.in)})
		if tc.err {
			if err == nil {
				t.Errorf("%s: want error, got %s", tc.in, got)
			}
			continue
		}
		if err != nil || len(got) != 1 || string(got[0]) != tc.want {
			t.Errorf("%s: want %s, got %s (err %v)", tc.in, tc.want, got, err)
		}
	}
}

func TestPrepareSeriesSamples(t *testing.T) {
	vals := []json.RawMessage{json.RawMessage("1"), json.RawMessage("2"), json.RawMessage("3")}
	// Seconds are normalized to milliseconds, then shifted, then cut by the retention cutoff. The second
	// sample (060_000 ms) is older than the cutoff (062_000) before the shift and newer after it: it is kept.
	ts, kept, dropped, scaled := prepareSeriesSamples([]int64{1_000_000_000, 1_000_000_060, 1_000_000_120}, vals, 1_000_000_062_000, 5_000)
	if !scaled || dropped != 1 || len(ts) != 2 || ts[0] != 1_000_000_065_000 || string(kept[0]) != "2" {
		t.Fatalf("got ts=%v kept=%s dropped=%d scaled=%v", ts, kept, dropped, scaled)
	}
	// Without a cutoff nothing is dropped.
	if _, kept, dropped, _ := prepareSeriesSamples([]int64{1, 2, 3}, vals, 0, 0); dropped != 0 || len(kept) != 3 {
		t.Fatalf("no cutoff: dropped=%d kept=%d", dropped, len(kept))
	}
}

// Found in live verification: after a shifted import, verification used the bundle's original time range
// from metadata.json and reported "0 matching series" although the data was imported.
func TestImport_PinnedRangeMovesWithShift(t *testing.T) {
	vm := newFakeVM(t, "")
	now := time.Now().UTC().Truncate(time.Second)
	line := fmt.Sprintf(`{"metric":{"__name__":"m"},"values":[1],"timestamps":[%d]}`+"\n", now.Add(-48*time.Hour).UnixMilli())
	bundle := &bundleInfo{MetricsPath: writeBundleFile(t, line), Metadata: &bundleMetadata{}}
	bundle.Metadata.TimeRange.Start = now.Add(-49 * time.Hour).Format(time.RFC3339)
	bundle.Metadata.TimeRange.End = now.Add(-47 * time.Hour).Format(time.RFC3339)

	shift := int64(48 * time.Hour / time.Millisecond)
	s := newServer("t", "")
	_, sum, err := s.streamImport(context.Background(), uploadConfig{}, bundle, vm.srv.URL+"/api/v1/import", 0, 0, shift, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Start.Equal(now.Add(-time.Hour)) || !sum.End.Equal(now.Add(time.Hour)) {
		t.Fatalf("want verification range shifted to [%s, %s], got [%s, %s]", now.Add(-time.Hour), now.Add(time.Hour), sum.Start, sum.End)
	}
}

// Found in live verification: /api/v1/series right after the last import chunk can be empty; the
// series shows up a moment later. An empty answer must be retried, not reported as a failure.
func TestImport_VerificationRetriesEmptyAnswer(t *testing.T) {
	origDelay := verifyRetryDelay
	verifyRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { verifyRetryDelay = origDelay })

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"m"}]}`))
	}))
	defer srv.Close()

	s := newServer("t", "")
	sum := importSummary{MetricName: "m", Labels: map[string]string{}, Start: time.Now().Add(-time.Hour), End: time.Now()}
	if res := s.verifyImport(context.Background(), uploadConfig{}, sum, srv.URL+"/api/v1/query"); !res.Verified {
		t.Fatalf("want verified after the series appears, got %q after %d calls", res.Message, calls)
	}
}

// Resume offsets must count the real line terminator: with CRLF lines the offset drifted by 1 byte per
// line and the resumed import re-sent part of the file.
func TestImport_ResumeOffsetWithCRLF(t *testing.T) {
	orig := maxImportChunkBytes
	maxImportChunkBytes = 1 // one line per chunk
	t.Cleanup(func() { maxImportChunkBytes = orig })

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.Copy(io.Discard, r.Body)
		if calls == 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	now := time.Now().UnixMilli()
	var b strings.Builder
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, `{"metric":{"__name__":"m","i":"%d"},"values":[1],"timestamps":[%d]}`+"\r\n", i, now)
	}
	content := b.String()
	s := newServer("t", "")
	_, sum, err := s.streamImport(context.Background(), uploadConfig{}, &bundleInfo{MetricsPath: writeBundleFile(t, content)}, srv.URL+"/api/v1/import", 0, 0, 0, 0, nil)
	if err == nil {
		t.Fatal("expected the third chunk to fail")
	}
	want := int64(strings.Index(content, `{"metric":{"__name__":"m","i":"2"}`))
	if sum.ProcessedBytes != want {
		t.Fatalf("resume offset %d, want %d (start of the first not imported line)", sum.ProcessedBytes, want)
	}
}

// Label values with quotes or backslashes (Windows paths, regexps) produced an invalid verification
// matcher, so verification failed after a good import.
func TestBuildLabelMatcher_EscapesValues(t *testing.T) {
	got := buildLabelMatcher(importSummary{MetricName: "m", Labels: map[string]string{"path": `C:\data\"x"`}})
	want := `{__name__="m",path="C:\\data\\\"x\""}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// drop_old=false: analyze keeps samples older than retention, so import must keep them too.
func TestImport_DropOldFalseIsHonored(t *testing.T) {
	day := int64(24 * time.Hour / time.Millisecond)
	now := time.Now().UnixMilli()
	line := fmt.Sprintf(`{"metric":{"__name__":"m","job":"j"},"values":[1,2],"timestamps":[%d,%d]}`+"\n", now-2*day, now)
	vm := newFakeVM(t, "1d")
	s := newServer("t", "")
	cfg := uploadConfig{Endpoint: vm.srv.URL, DropOld: false}
	job := s.newJob(int64(len(line)))
	s.storeJob(job)
	importURL, queryURL, err := resolveEndpoints(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.runImportJob(context.Background(), job, cfg, writeBundleFile(t, line), "bundle.jsonl", importURL, queryURL, 0)
	snap, _ := s.getJobSnapshot(job.ID)
	if snap.Summary == nil || snap.Summary.Points != 2 || snap.Summary.DroppedOld != 0 {
		t.Fatalf("drop_old=false: want 2 points and 0 dropped, got %+v", snap.Summary)
	}
}

func vmimportTempFiles(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(os.TempDir(), "vmimport-*"))
	return files
}

// isolateTempDir points os.TempDir at a private directory, so other processes (a running vmimporter,
// parallel test packages) cannot change the file counts these tests compare.
func isolateTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(env, dir)
	}
}

func zipBundle(t *testing.T, lines string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("metrics.jsonl")
	_, _ = io.WriteString(w, lines)
	_ = zw.Close()
	_ = f.Close()
	return p
}

// A zip bundle is read in place (no extracted copy in the temp dir) and resume skips the already
// imported bytes of the uncompressed stream.
func TestImport_ZipIsReadInPlaceAndResumes(t *testing.T) {
	now := time.Now().UnixMilli()
	var b strings.Builder
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, `{"metric":{"__name__":"m","i":"%d"},"values":[1],"timestamps":[%d]}`+"\n", i, now)
	}
	content := b.String()
	path := zipBundle(t, content)

	isolateTempDir(t)
	before := len(vmimportTempFiles(t))
	bundle, err := prepareBundle(path, "no-extension-on-resume", 0) // detected by content, not by name
	if err != nil {
		t.Fatal(err)
	}
	if bundle.MetricsPath != path || bundle.ZipEntry != "metrics.jsonl" || len(vmimportTempFiles(t)) != before {
		t.Fatalf("zip must be read in place: %+v", bundle)
	}

	vm := newFakeVM(t, "")
	offset := int64(strings.Index(content, `{"metric":{"__name__":"m","i":"1"}`))
	s := newServer("t", "")
	if _, _, err := s.streamImport(context.Background(), uploadConfig{}, bundle, vm.srv.URL+"/api/v1/import", offset, 0, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if sent := vm.body(); strings.Contains(sent, `"i":"0"`) || !strings.Contains(sent, `"i":"1"`) || !strings.Contains(sent, `"i":"2"`) {
		t.Fatalf("resume from offset %d sent %q", offset, sent)
	}
}

// A failed import keeps its upload for resume, but only until the job expires.
func TestImport_FailedJobUploadIsRemovedOnExpiry(t *testing.T) {
	orig := jobRetention
	jobRetention = 100 * time.Millisecond
	t.Cleanup(func() { jobRetention = orig })

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	upload := filepath.Join(os.TempDir(), fmt.Sprintf("vmimport-upload-test-%d", time.Now().UnixNano()))
	if err := os.WriteFile(upload, []byte(fmt.Sprintf(`{"metric":{"__name__":"m"},"values":[1],"timestamps":[%d]}`+"\n", time.Now().UnixMilli())), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newServer("t", "")
	cfg := uploadConfig{Endpoint: failing.URL}
	importURL, queryURL, _ := resolveEndpoints(cfg)
	job := s.newJob(1)
	s.storeJob(job)
	s.runImportJob(context.Background(), job, cfg, upload, "bundle.jsonl", importURL, queryURL, 0)
	if snap, _ := s.getJobSnapshot(job.ID); snap.State != jobStateFailed || !snap.ResumeReady {
		t.Fatalf("want a failed, resumable job, got %s", snap.State)
	}
	if _, err := os.Stat(upload); err != nil {
		t.Fatalf("upload must be kept for resume: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(upload); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload must be removed after expiry")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := s.getJobSnapshot(job.ID); ok {
		t.Fatal("expired job must be removed")
	}
}

// An upload rejected after the bundle was stored (bad endpoint) must not leave the bundle in the temp dir.
func TestUpload_RejectedRequestLeavesNoTempFile(t *testing.T) {
	isolateTempDir(t)
	before := len(vmimportTempFiles(t))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("config", `{"endpoint":"not a url"}`)
	fw, _ := mw.CreateFormFile("bundle", "bundle.jsonl")
	_, _ = io.WriteString(fw, `{"metric":{"__name__":"m"},"values":[1],"timestamps":[1]}`+"\n")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	newServer("t", "").handleUpload(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if after := len(vmimportTempFiles(t)); after != before {
		t.Fatalf("temp files: before %d, after %d", before, after)
	}
}

// On shutdown the uploads kept for resume are removed: jobs do not survive a restart.
func TestServerClose_RemovesKeptUploads(t *testing.T) {
	upload := filepath.Join(t.TempDir(), "vmimport-upload-kept")
	if err := os.WriteFile(upload, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newServer("t", "")
	job := s.newJob(1)
	job.State, job.ResumeReady, job.BundlePath = jobStateFailed, true, upload
	s.storeJob(job)
	s.Close()
	if _, err := os.Stat(upload); !os.IsNotExist(err) {
		t.Fatalf("kept upload must be removed on Close, stat err=%v", err)
	}
}

// A target without /api/v1/series (vmagent, vminsert, an auth proxy) answers 4xx at once. Verification must not
// keep the job in "verifying" for half a minute retrying a request that cannot succeed.
func TestImport_VerificationDoesNotRetryClientErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer srv.Close()
	s := newServer("t", "")
	sum := importSummary{MetricName: "m", Labels: map[string]string{}, Start: time.Now().Add(-time.Hour), End: time.Now()}
	start := time.Now()
	res := s.verifyImport(context.Background(), uploadConfig{}, sum, srv.URL+"/api/v1/query")
	if res.Verified || calls != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("want one call and a fast unverified result, got verified=%v calls=%d in %s", res.Verified, calls, time.Since(start))
	}
}

// A zip with two metrics.jsonl entries is read from the same (first) entry that was chosen during preparation.
func TestPrepareZipBundle_DuplicateMetricsEntries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dup.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for _, body := range []string{`{"metric":{"__name__":"first"},"values":[1],"timestamps":[1]}`, `{"metric":{"__name__":"second"},"values":[1],"timestamps":[1]}`} {
		w, _ := zw.Create("metrics.jsonl")
		_, _ = io.WriteString(w, body+"\n")
	}
	_ = zw.Close()
	_ = f.Close()

	bundle, err := prepareBundle(p, "dup.zip", 0)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := bundle.open(0)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !strings.Contains(string(data), `"first"`) {
		t.Fatalf("want the first entry, got %s", data)
	}
}
