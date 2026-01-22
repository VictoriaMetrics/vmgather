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
	"testing"
	"time"
)

func recentTimestampMs() int64 {
	return time.Now().Add(-time.Minute).UnixMilli()
}

func recentTimeRange() (string, string) {
	start := time.Now().Add(-time.Minute).UTC()
	end := start.Add(5 * time.Minute)
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

func TestHandleUploadSuccess(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			if r.Header.Get("Content-Type") != "application/jsonl" {
				t.Fatalf("unexpected content type %s", r.Header.Get("Content-Type"))
			}
			if r.Header.Get("X-Vm-TenantID") != "42" {
				t.Fatalf("tenant header missing")
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("accepted"))
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"test_metric"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"1y"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	srvImpl := NewServer("test")
	srv := httptest.NewServer(srvImpl.Router())
	defer srv.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, TenantID: "42"}
	configBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(configBytes))
	fileWriter, _ := writer.CreateFormFile("bundle", "test.jsonl")
	fmt.Fprintf(fileWriter, `{"metric":{"__name__":"test_metric","job":"demo"},"values":[1],"timestamps":[%d]}`, recentTimestampMs())
	writer.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	job := waitForJobCompletion(t, srvImpl, created.JobID, 2*time.Second)
	if job.State != jobStateCompleted {
		t.Fatalf("job did not complete: %+v", job)
	}
	if job.Summary == nil || job.Summary.MetricName != "test_metric" {
		t.Fatalf("unexpected summary %+v", job.Summary)
	}
	if job.Summary.SourceBytes == 0 || job.Summary.InflatedBytes == 0 {
		t.Fatalf("expected byte accounting, got %+v", job.Summary)
	}
}

func TestHandleUploadZipChunking(t *testing.T) {
	origChunk := maxImportChunkBytes
	maxImportChunkBytes = 128
	defer func() { maxImportChunkBytes = origChunk }()

	var imports [][]byte

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			body, _ := io.ReadAll(r.Body)
			imports = append(imports, body)
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"demo"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"30d"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	var zipBuffer bytes.Buffer
	zw := zip.NewWriter(&zipBuffer)
	mw, _ := zw.Create("metrics.jsonl")
	ts := recentTimestampMs()
	start, end := recentTimeRange()
	for i := 0; i < 5; i++ {
		fmt.Fprintf(mw, `{"metric":{"__name__":"demo","job":"zip","idx":"%d"},"values":[%d],"timestamps":[%d]}`+"\n", i, i, ts)
	}
	meta, _ := zw.Create("metadata.json")
	meta.Write([]byte(fmt.Sprintf(`{"time_range":{"start":"%s","end":"%s"},"metrics_count":5}`, start, end)))
	zw.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, TenantID: "10"}
	configBytes, _ := json.Marshal(config)
	writer.WriteField("config", string(configBytes))
	fw, _ := writer.CreateFormFile("bundle", "bundle.zip")
	fw.Write(zipBuffer.Bytes())
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv := NewServer("test")
	srv.handleUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if job.State != jobStateCompleted {
		t.Fatalf("job failed: %+v", job)
	}
	if len(imports) < 2 {
		t.Fatalf("expected multiple chunk uploads, got %d", len(imports))
	}
	if bytes.Contains(imports[0], []byte(`"values":["`)) {
		t.Fatalf("expected numeric values, got %s", imports[0])
	}
}

func TestPrepareZipBundleAllowsCustomJsonl(t *testing.T) {
	var zipBuffer bytes.Buffer
	zw := zip.NewWriter(&zipBuffer)
	mw, _ := zw.Create("custom-data.jsonl")
	ts := recentTimestampMs()
	start, end := recentTimeRange()
	fmt.Fprintf(mw, `{"metric":{"__name__":"demo","job":"resume"},"values":[1],"timestamps":[%d]}`+"\n", ts)
	meta, _ := zw.Create("metadata.json")
	meta.Write([]byte(fmt.Sprintf(`{"time_range":{"start":"%s","end":"%s"},"metrics_count":1}`, start, end)))
	zw.Close()

	tmpPath := ensureTestFile(t, "bundle-custom.zip", func(w io.Writer) error {
		_, err := w.Write(zipBuffer.Bytes())
		return err
	})

	bundle, err := prepareZipBundle(tmpPath, int64(len(zipBuffer.Bytes())))
	if err != nil {
		t.Fatalf("expected bundle, got error: %v", err)
	}
	if bundle.MetricsPath == "" {
		t.Fatalf("expected metrics path to be set")
	}
	data, err := os.ReadFile(bundle.MetricsPath)
	if err != nil {
		t.Fatalf("read extracted metrics failed: %v", err)
	}
	if !bytes.Contains(data, []byte(`"demo"`)) {
		t.Fatalf("unexpected metrics content: %s", data)
	}
	if bundle.Cleanup != nil {
		bundle.Cleanup()
	}
}

func TestPrepareZipBundleRejectsNonMetricsJsonl(t *testing.T) {
	var zipBuffer bytes.Buffer
	zw := zip.NewWriter(&zipBuffer)
	mw, _ := zw.Create("random.jsonl")
	fmt.Fprintf(mw, `{"not":"metrics"}`+"\n")
	zw.Close()

	tmpPath := ensureTestFile(t, "bundle-random.zip", func(w io.Writer) error {
		_, err := w.Write(zipBuffer.Bytes())
		return err
	})

	_, err := prepareZipBundle(tmpPath, int64(len(zipBuffer.Bytes())))
	if err == nil {
		t.Fatalf("expected error for missing metrics data")
	}
	if !strings.Contains(err.Error(), "missing metrics") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testTempDir(t *testing.T) string {
	t.Helper()
	base := filepath.Join(".", "tmp", "tests")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("failed to create base temp dir: %v", err)
	}
	return base
}

func ensureTestFile(t *testing.T, name string, write func(io.Writer) error) string {
	t.Helper()
	path := filepath.Join(testTempDir(t), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if write != nil {
		if err := write(f); err != nil {
			f.Close()
			t.Fatalf("failed to write test file: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close test file: %v", err)
	}
	return path
}

func waitForJobCompletion(t *testing.T, srv *Server, jobID string, timeout time.Duration) importJob {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last importJob
	for time.Now().Before(deadline) {
		if job, ok := srv.getJobSnapshot(jobID); ok {
			last = job
			if job.State == jobStateCompleted || job.State == jobStateFailed {
				return job
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish: %+v", jobID, last)
	return importJob{}
}

func TestHandleUploadRejectsMissingFile(t *testing.T) {
	srv := NewServer("test")
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", nil)
	srv.handleUpload(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}

func TestHandleCheckEndpoint(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer downstream.Close()

	srv := NewServer("test")
	reqBody := bytes.NewBufferString(fmt.Sprintf(`{"endpoint":"%s","tenant_id":"42"}`, downstream.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/check-endpoint", reqBody)
	recorder := httptest.NewRecorder()

	srv.handleCheckEndpoint(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
}

func TestHandleCheckEndpointFails(t *testing.T) {
	srv := NewServer("test")
	req := httptest.NewRequest(http.MethodPost, "/api/check-endpoint", bytes.NewBufferString(`{"endpoint":"http://localhost:65500"}`))
	recorder := httptest.NewRecorder()

	srv.handleCheckEndpoint(recorder, req)
	if recorder.Code == http.StatusOK {
		t.Fatalf("expected error status")
	}
}

func TestNormalizeStringValuesDuringImport(t *testing.T) {
	origChunk := maxImportChunkBytes
	maxImportChunkBytes = 256
	defer func() { maxImportChunkBytes = origChunk }()

	var payloads [][]byte
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			body, _ := io.ReadAll(r.Body)
			payloads = append(payloads, body)
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"flag"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"90d"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	var buf bytes.Buffer
	ts := recentTimestampMs()
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&buf, `{"metric":{"__name__":"flag","job":"demo","idx":"%d"},"values":["%d"],"timestamps":[%d]}`+"\n", i, i, ts)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL}
	cfgBytes, _ := json.Marshal(config)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "strings.jsonl")
	fw.Write(buf.Bytes())
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv := NewServer("test")
	srv.handleUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if job.State != jobStateCompleted {
		t.Fatalf("job failed: %+v", job)
	}
	if job.Summary == nil || job.Summary.SkippedLines != 0 {
		t.Fatalf("expected zero skipped lines, got %+v", job.Summary)
	}
	for _, p := range payloads {
		if bytes.Contains(p, []byte(`"values":["`)) {
			t.Fatalf("expected normalized numeric payload, got %s", p)
		}
	}
}

func TestResumeImportAfterFailure(t *testing.T) {
	origChunk := maxImportChunkBytes
	maxImportChunkBytes = 128
	defer func() { maxImportChunkBytes = origChunk }()

	var importCalls int
	failOnce := true
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			importCalls++
			if failOnce {
				failOnce = false
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"demo"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"30d"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	var buf bytes.Buffer
	ts := recentTimestampMs()
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&buf, `{"metric":{"__name__":"demo","job":"resume","idx":"%d"},"values":[%d],"timestamps":[%d]}`+"\n", i, i, ts)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL}
	cfgBytes, _ := json.Marshal(config)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "resume.jsonl")
	fw.Write(buf.Bytes())
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv := NewServer("test")
	srv.handleUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if job.State != jobStateFailed || !job.ResumeReady {
		t.Fatalf("expected resumable failure, got %+v", job)
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/import/resume?id="+created.JobID, nil)
	resumeRec := httptest.NewRecorder()
	srv.handleResume(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume failed with %d", resumeRec.Code)
	}

	resumed := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if resumed.State != jobStateCompleted {
		t.Fatalf("expected completion after resume, got %+v", resumed)
	}
	if importCalls < 2 {
		t.Fatalf("expected retry attempts, got %d calls", importCalls)
	}
}

func TestTenantIsolationHeaders(t *testing.T) {
	tenantCalls := make(map[string]int)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			w.WriteHeader(http.StatusNoContent)
			return
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			tenantCalls[r.Header.Get("X-Vm-TenantID")]++
			t.Logf("import request tenant=%q path=%s", r.Header.Get("X-Vm-TenantID"), r.URL.Path)
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"demo"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"400d"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	runUpload := func(tid string) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		cfg := uploadConfig{Endpoint: downstream.URL, TenantID: tid}
		cfgBytes, _ := json.Marshal(cfg)
		writer.WriteField("config", string(cfgBytes))
		fw, _ := writer.CreateFormFile("bundle", "tenant.jsonl")
		fw.Write([]byte(fmt.Sprintf(`{"metric":{"__name__":"demo"},"values":[1],"timestamps":[%d]}`, recentTimestampMs())))
		writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()

		srv := NewServer("test")
		srv.handleUpload(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var created struct {
			JobID string `json:"job_id"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
		t.Logf("job state=%s err=%s msg=%s summary=%+v", job.State, job.Error, job.Message, job.Summary)
	}

	runUpload("101")
	runUpload("202")
	if tenantCalls["101"] == 0 || tenantCalls["202"] == 0 {
		t.Fatalf("expected calls for both tenants, got %+v", tenantCalls)
	}
	if tenantCalls[""] != 0 {
		t.Fatalf("unexpected writes to root tenant: %+v", tenantCalls)
	}
}

func TestRetentionDropsOldPoints(t *testing.T) {
	now := time.Now()
	oldTs := now.Add(-2 * time.Hour).UnixMilli()
	newTs := now.Add(-10 * time.Minute).UnixMilli()

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"demo"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			// 1 hour retention ensures oldTs is dropped, newTs kept
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"1h"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	var buf bytes.Buffer
	fmt.Fprintf(&buf, `{"metric":{"__name__":"demo"},"values":[1],"timestamps":[%d]}`+"\n", oldTs)
	fmt.Fprintf(&buf, `{"metric":{"__name__":"demo"},"values":[2],"timestamps":[%d]}`+"\n", newTs)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	cfg := uploadConfig{Endpoint: downstream.URL}
	cfgBytes, _ := json.Marshal(cfg)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "retention.jsonl")
	fw.Write(buf.Bytes())
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv := NewServer("test")
	srv.handleUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if job.State != jobStateCompleted {
		t.Fatalf("job failed: %+v", job)
	}
	if job.Summary == nil {
		t.Fatalf("missing summary")
	}
	if job.Summary.DroppedOld != 1 {
		t.Fatalf("expected 1 dropped old point, got %d", job.Summary.DroppedOld)
	}
	if job.Summary.Points != 1 {
		t.Fatalf("expected 1 ingested point, got %d", job.Summary.Points)
	}
}

func TestSkipsNonNumericValues(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"demo"}]}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"365d"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	cfg := uploadConfig{Endpoint: downstream.URL}
	cfgBytes, _ := json.Marshal(cfg)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "badvalue.jsonl")
	// "foo" should be skipped; the valid row should pass
	ts := recentTimestampMs()
	fmt.Fprintf(fw, `{"metric":{"__name__":"demo"},"values":["foo"],"timestamps":[%d]}`+"\n", ts)
	fmt.Fprintf(fw, `{"metric":{"__name__":"demo"},"values":[1],"timestamps":[%d]}`+"\n", ts+60000)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv := NewServer("test")
	srv.handleUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if job.State != jobStateCompleted {
		t.Fatalf("job failed: %+v", job)
	}
	if job.Summary == nil {
		t.Fatalf("missing summary")
	}
	if job.Summary.SkippedLines != 1 {
		t.Fatalf("expected 1 skipped line, got %d", job.Summary.SkippedLines)
	}
	if job.Summary.Points != 1 {
		t.Fatalf("expected 1 ingested point, got %d", job.Summary.Points)
	}
}

func TestAnalyzeBundleRetentionAndWarnings(t *testing.T) {
	payload := `{"metric":{"__name__":"demo","job":"preflight"},"values":[1],"timestamps":[1000,2000]}
{"metric":{"__name__":"demo","job":"preflight"},"values":[1],"timestamps":[20000]}`
	tmpPath := ensureTestFile(t, "bundle-preflight.jsonl", func(w io.Writer) error {
		_, err := io.WriteString(w, payload)
		return err
	})

	srv := NewServer("test")
	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: int64(len(payload)), ExtractedBytes: int64(len(payload))}
	summary, err := srv.analyzeBundle(context.Background(), bundle, 5000, 0)
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if summary.DroppedOld == 0 {
		t.Fatalf("expected old samples dropped, got %+v", summary)
	}
	warnings := buildAnalysisWarnings(summary, 5000)
	if len(warnings) == 0 {
		t.Fatalf("expected retention warning")
	}
}

func TestHandleAnalyzeEndpoint(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"success","data":{"retentionTime":"1d"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, TenantID: "", DropOld: true}
	cfgBytes, _ := json.Marshal(config)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "demo.jsonl")
	fmt.Fprintf(fw, `{"metric":{"__name__":"demo"},"values":[1],"timestamps":[0,1,2]}`)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	srv.handleAnalyze(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payloadResp struct {
		Summary struct {
			DroppedOld int `json:"dropped_old"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payloadResp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if payloadResp.Summary.DroppedOld == 0 {
		t.Fatalf("expected dropped old samples due to retention")
	}
}

func TestHandleAnalyzeWithRealZipRetention(t *testing.T) {
	now := time.Now()
	oldTs := now.Add(-2 * time.Hour).UnixMilli()
	newerTs := now.Add(-30 * time.Minute).UnixMilli()
	newestTs := now.Add(-15 * time.Minute).UnixMilli()

	// VM endpoint that returns a 1h retention window
	retentionSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"1h"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer retentionSrv.Close()

	// Build a real zip bundle with metrics.jsonl
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	mw, _ := zw.Create("metrics.jsonl")
	fmt.Fprintf(mw, `{"metric":{"__name__":"demo","job":"zip-real"},"values":[1],"timestamps":[%d]}`+"\n", oldTs)
	fmt.Fprintf(mw, `{"metric":{"__name__":"demo","job":"zip-real"},"values":[2],"timestamps":[%d]}`+"\n", newerTs)
	fmt.Fprintf(mw, `{"metric":{"__name__":"demo","job":"zip-real"},"values":[3],"timestamps":[%d]}`+"\n", newestTs)
	meta, _ := zw.Create("metadata.json")
	meta.Write([]byte(`{"time_range":{"start":"meta-start","end":"meta-end"},"metrics_count":3}`))
	zw.Close()

	zipPath := ensureTestFile(t, "bundle-real.zip", func(w io.Writer) error {
		_, err := w.Write(zipBuf.Bytes())
		return err
	})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	cfg := uploadConfig{Endpoint: retentionSrv.URL, DropOld: true}
	cfgBytes, _ := json.Marshal(cfg)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "bundle-real.zip")
	fw.Write(zipBuf.Bytes())
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv := NewServer("test")
	srv.handleAnalyze(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Summary struct {
			Points     int `json:"points"`
			DroppedOld int `json:"dropped_old"`
		} `json:"summary"`
		RetentionCutoff int64    `json:"retention_cutoff"`
		Warnings        []string `json:"warnings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.RetentionCutoff == 0 {
		t.Fatalf("expected retention cutoff from upstream")
	}
	if resp.Summary.DroppedOld != 1 {
		t.Fatalf("expected 1 old sample dropped, got %d", resp.Summary.DroppedOld)
	}
	if resp.Summary.Points != 2 {
		t.Fatalf("expected 2 kept points, got %d", resp.Summary.Points)
	}
	foundWarn := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "retention") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected retention warning, got %v", resp.Warnings)
	}

	_ = os.Remove(zipPath)
}

func TestStreamImportNoCutoffWhenDisabled(t *testing.T) {
	imports := 0
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/import") {
			imports++
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer downstream.Close()

	tmpPath := ensureTestFile(t, "demo-stream.jsonl", func(w io.Writer) error {
		_, err := fmt.Fprintf(w, `{"metric":{"__name__":"demo"},"values":[1],"timestamps":[1000]}`+"\n")
		return err
	})

	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: 63, ExtractedBytes: 63}
	srv := NewServer("test")
	cfg := uploadConfig{}
	_, summary, err := srv.streamImport(context.Background(), cfg, bundle, downstream.URL+"/api/v1/import", 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("streamImport failed: %v", err)
	}
	if summary.DroppedOld != 0 {
		t.Fatalf("expected no drops with cutoff disabled, got %+v", summary)
	}
	if imports == 0 {
		t.Fatalf("expected import request to be sent")
	}
}

func TestAnalyzeBundleSuggestedShiftAndWarnings(t *testing.T) {
	now := time.Now().UnixMilli()
	old := now - int64(2*time.Hour/time.Millisecond)
	newer := now - int64(30*time.Minute/time.Millisecond)
	payload := fmt.Sprintf(`{"metric":{"__name__":"demo","job":"preflight"},"values":[1,2,3],"timestamps":[%d,%d,%d]}`, old, newer, now)
	tmpPath := ensureTestFile(t, "bundle-shift.jsonl", func(w io.Writer) error {
		_, err := io.WriteString(w, payload)
		return err
	})

	srv := NewServer("test")
	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: int64(len(payload)), ExtractedBytes: int64(len(payload))}
	retentionCutoff := now - int64(1*time.Hour/time.Millisecond)
	summary, err := srv.analyzeBundle(context.Background(), bundle, retentionCutoff, 0)
	if err != nil {
		t.Fatalf("analyzeBundle failed: %v", err)
	}
	if summary.Start.IsZero() || summary.End.IsZero() {
		t.Fatalf("expected start/end set, got %+v", summary)
	}
	if summary.DroppedOld == 0 {
		t.Fatalf("expected drops under retention cutoff, got %+v", summary)
	}
	warnings := buildAnalysisWarnings(summary, retentionCutoff)
	if len(warnings) == 0 {
		t.Fatalf("expected warnings for retention and span")
	}
	if summary.NormalizedTs {
		t.Fatalf("did not expect normalization for already-ms timestamps")
	}
}

func TestAnalyzeBundleSkipsInvalidTimestamps(t *testing.T) {
	payload := `{"metric":{"__name__":"demo"},"values":[1],"timestamps":["bad-ts"]}`
	tmpPath := ensureTestFile(t, "bundle-invalid-ts.jsonl", func(w io.Writer) error {
		_, err := io.WriteString(w, payload)
		return err
	})
	srv := NewServer("test")
	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: int64(len(payload)), ExtractedBytes: int64(len(payload))}
	summary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0)
	if err != nil {
		t.Fatalf("analyzeBundle failed: %v", err)
	}
	if summary.SkippedLines == 0 {
		t.Fatalf("expected invalid timestamp lines to be skipped")
	}
}

func TestBuildAnalysisWarningsSpanExceedsWindow(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-1 * time.Hour)
	summary := importSummary{
		Start: now.Add(-3 * time.Hour),
		End:   now.Add(-30 * time.Minute),
	}
	w := buildAnalysisWarnings(summary, cutoff.UnixMilli())
	if len(w) == 0 {
		t.Fatalf("expected warnings for span exceeding window")
	}
	foundSpan := false
	for _, msg := range w {
		if strings.Contains(msg, "exceeds current retention window") {
			foundSpan = true
		}
	}
	if !foundSpan {
		t.Fatalf("expected span warning, got %v", w)
	}
}

func TestNormalizeTimestampsAutoScale(t *testing.T) {
	seconds := []int64{1700000000, 1700000001}
	millis, scaled := normalizeTimestamps(seconds)
	if !scaled || millis[0] != 1700000000*1000 {
		t.Fatalf("expected seconds -> ms scaling")
	}

	nanos := []int64{1700000000000000000}
	msNanos, scaledNano := normalizeTimestamps(nanos)
	if !scaledNano || msNanos[0] != 1700000000000000000/1_000_000 {
		t.Fatalf("expected nanos -> ms scaling")
	}

	micros := []int64{1700000000000000}
	msMicros, scaledMicro := normalizeTimestamps(micros)
	if !scaledMicro || msMicros[0] != 1700000000000000/1000 {
		t.Fatalf("expected micros -> ms scaling")
	}
}
