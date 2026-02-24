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

func TestRedactURLForLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty endpoint",
			in:   "",
			want: "unknown-endpoint",
		},
		{
			name: "invalid endpoint",
			in:   "://bad",
			want: "invalid-url",
		},
		{
			name: "no user info",
			in:   "https://example.com/api/v1/import",
			want: "https://example.com/api/v1/import",
		},
		{
			name: "username only",
			in:   "https://alice@example.com/api/v1/import",
			want: "https://alice@example.com/api/v1/import",
		},
		{
			name: "username and password",
			in:   "https://alice:secret@example.com/api/v1/import",
			want: "https://alice:xxxxx@example.com/api/v1/import",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := redactURLForLog(tt.in)
			if got != tt.want {
				t.Fatalf("unexpected redacted URL: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecentProfilesRoundTripAndSecretStripping(t *testing.T) {
	t.Parallel()

	profilesPath := filepath.Join(t.TempDir(), "recent-profiles.json")
	srv := newServer("test", profilesPath)

	srv.saveRecentProfile(uploadConfig{
		Endpoint:          "https://user:secret@vm.example.com/metrics/insert/prometheus",
		TenantID:          "1042",
		AuthType:          "basic",
		Username:          "monitoring-1042",
		Password:          "super-secret-password",
		CustomHeaderName:  "X-API-Key",
		CustomHeaderValue: "must-not-be-stored",
		SkipTLSVerify:     true,
		MetricStepSeconds: 300,
		BatchWindowSecs:   300,
		DropOld:           true,
		TimeShiftMs:       120000,
		MaxLabelsOverride: 45,
		DropLabels:        []string{"cluster", "job", "cluster"},
	})
	srv.saveRecentProfile(uploadConfig{
		Endpoint:          "https://vm.example.com/metrics/insert/prometheus",
		TenantID:          "1042",
		AuthType:          "bearer",
		Password:          "bearer-secret",
		SkipTLSVerify:     false,
		MetricStepSeconds: 60,
		BatchWindowSecs:   60,
		DropOld:           true,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/recent", nil)
	rec := httptest.NewRecorder()
	srv.handleRecentProfiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, "super-secret-password") || strings.Contains(body, "bearer-secret") || strings.Contains(body, "must-not-be-stored") {
		t.Fatalf("response leaked secret fields: %s", body)
	}

	var payload struct {
		Profiles []recentProfile `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode profiles payload: %v", err)
	}
	if len(payload.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(payload.Profiles))
	}

	latest := payload.Profiles[0]
	if latest.AuthType != "bearer" {
		t.Fatalf("expected latest profile auth=bearer, got %q", latest.AuthType)
	}
	if latest.Endpoint != "https://vm.example.com/metrics/insert/prometheus" {
		t.Fatalf("unexpected latest endpoint: %q", latest.Endpoint)
	}

	older := payload.Profiles[1]
	if older.Username != "monitoring-1042" {
		t.Fatalf("expected stored basic username, got %q", older.Username)
	}
	if strings.Contains(older.Endpoint, "secret@") {
		t.Fatalf("expected sanitized endpoint without userinfo, got %q", older.Endpoint)
	}
	if older.TimeShiftMs != 120000 {
		t.Fatalf("expected timeshift 120000, got %d", older.TimeShiftMs)
	}
	if older.MaxLabelsOverride != 45 {
		t.Fatalf("expected max labels override=45, got %d", older.MaxLabelsOverride)
	}
	if len(older.DropLabels) != 1 || older.DropLabels[0] != "cluster" {
		t.Fatalf("expected sanitized drop labels [cluster], got %v", older.DropLabels)
	}

	srvReloaded := newServer("test", profilesPath)
	reloaded := srvReloaded.recentProfilesSnapshot()
	if len(reloaded) != 2 {
		t.Fatalf("expected 2 persisted profiles, got %d", len(reloaded))
	}
	if reloaded[0].ID == "" || reloaded[1].ID == "" {
		t.Fatalf("expected non-empty profile IDs after reload: %+v", reloaded)
	}
}

func TestRecentProfilesDeduplicateAndMoveToTop(t *testing.T) {
	t.Parallel()

	srv := newServer("test", filepath.Join(t.TempDir(), "profiles.json"))
	first := uploadConfig{
		Endpoint:          "https://vm-a.example.com/metrics/insert/prometheus",
		TenantID:          "100",
		AuthType:          "basic",
		Username:          "first",
		MetricStepSeconds: 60,
		BatchWindowSecs:   60,
		DropOld:           true,
	}
	second := uploadConfig{
		Endpoint:          "https://vm-b.example.com/metrics/insert/prometheus",
		TenantID:          "200",
		AuthType:          "none",
		MetricStepSeconds: 300,
		BatchWindowSecs:   300,
		DropOld:           true,
	}

	srv.saveRecentProfile(first)
	srv.saveRecentProfile(second)
	srv.saveRecentProfile(first)

	profiles := srv.recentProfilesSnapshot()
	if len(profiles) != 2 {
		t.Fatalf("expected 2 unique profiles, got %d", len(profiles))
	}
	if profiles[0].Endpoint != "https://vm-a.example.com/metrics/insert/prometheus" {
		t.Fatalf("expected first profile moved to top, got %q", profiles[0].Endpoint)
	}
	if profiles[1].Endpoint != "https://vm-b.example.com/metrics/insert/prometheus" {
		t.Fatalf("unexpected second profile order: %q", profiles[1].Endpoint)
	}
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
	profiles := srvImpl.recentProfilesSnapshot()
	found := false
	for _, profile := range profiles {
		if profile.Endpoint == downstream.URL && profile.TenantID == "42" && profile.AuthType == "none" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected profile for endpoint=%q tenant=42 auth=none in %+v", downstream.URL, profiles)
	}
}

func TestHandleUploadFailedImportStillSavesRecentProfile(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/import"):
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway from test"))
		case strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"retentionTime":"30d"}}`))
		case strings.HasSuffix(r.URL.Path, "/metrics"):
			_, _ = w.Write([]byte(`flag{name="retentionPeriod", value="30d", is_set="true"} 1`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := newServer("test", filepath.Join(t.TempDir(), "profiles.json"))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, TenantID: "1042"}
	configBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(configBytes))
	fileWriter, _ := writer.CreateFormFile("bundle", "test.jsonl")
	fmt.Fprintf(fileWriter, `{"metric":{"__name__":"test_metric","job":"demo"},"values":[1],"timestamps":[%d]}`, recentTimestampMs())
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	srv.handleUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	job := waitForJobCompletion(t, srv, created.JobID, 2*time.Second)
	if job.State != jobStateFailed {
		t.Fatalf("expected failed job state, got %+v", job)
	}

	profiles := srv.recentProfilesSnapshot()
	found := false
	for _, profile := range profiles {
		if profile.Endpoint == downstream.URL && profile.TenantID == "1042" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected recent profile for failed import endpoint=%q tenant=1042 in %+v", downstream.URL, profiles)
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

	srv := newServer("test", filepath.Join(t.TempDir(), "profiles.json"))
	reqBody := bytes.NewBufferString(fmt.Sprintf(`{"endpoint":"%s","tenant_id":"42"}`, downstream.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/check-endpoint", reqBody)
	recorder := httptest.NewRecorder()

	srv.handleCheckEndpoint(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	profiles := srv.recentProfilesSnapshot()
	if len(profiles) != 1 {
		t.Fatalf("expected 1 recent profile, got %d", len(profiles))
	}
	if profiles[0].Endpoint != downstream.URL || profiles[0].TenantID != "42" {
		t.Fatalf("unexpected recent profile: %+v", profiles[0])
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
	summary, err := srv.analyzeBundle(context.Background(), bundle, 5000, 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if summary.DroppedOld == 0 {
		t.Fatalf("expected old samples dropped, got %+v", summary)
	}
	warnings := buildAnalysisWarnings(summary, 5000, 0)
	if len(warnings) == 0 {
		t.Fatalf("expected retention warning")
	}
}

func TestAnalyzeBundleWarnsOnTargetLabelLimit(t *testing.T) {
	payload := `{"metric":{"__name__":"demo","job":"preflight","instance":"i-1"},"values":[1],"timestamps":[20000]}`
	tmpPath := ensureTestFile(t, "bundle-label-limit.jsonl", func(w io.Writer) error {
		_, err := io.WriteString(w, payload)
		return err
	})

	srv := NewServer("test")
	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: int64(len(payload)), ExtractedBytes: int64(len(payload))}
	summary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0, 2, nil, 0)
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if summary.OverLabelLimit == 0 {
		t.Fatalf("expected over-limit series, got %+v", summary)
	}
	warnings := buildAnalysisWarnings(summary, 0, 2)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "maxLabelsPerTimeseries=2") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected maxLabels warning, got %v", warnings)
	}
}

func TestAnalyzeBundleDropLabelsCanReduceLimitRisk(t *testing.T) {
	payload := `{"metric":{"__name__":"demo","job":"preflight","instance":"i-1","cluster":"c1","pod":"p1"},"values":[1],"timestamps":[20000]}`
	tmpPath := ensureTestFile(t, "bundle-label-limit-drop.jsonl", func(w io.Writer) error {
		_, err := io.WriteString(w, payload)
		return err
	})

	srv := NewServer("test")
	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: int64(len(payload)), ExtractedBytes: int64(len(payload))}
	summary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0, 4, []string{"cluster", "pod"}, 0)
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if summary.TotalLabels != 5 {
		t.Fatalf("expected total labels=5 before drops, got %d", summary.TotalLabels)
	}
	if summary.OverLabelLimit != 0 {
		t.Fatalf("expected no over-limit series after label drop, got %+v", summary)
	}
	if summary.MaxLabelsSeen > 4 {
		t.Fatalf("expected max labels <= 4 after drop, got %d", summary.MaxLabelsSeen)
	}
}

func TestHandleAnalyzeDropLabelsReduceLabelRisk(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status/tsdb":
			w.WriteHeader(http.StatusNotFound)
		case "/metrics":
			_, _ = io.WriteString(w, "flag{name=\"maxLabelsPerTimeseries\", value=\"4\", is_set=\"true\"} 1\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{
		Endpoint:   downstream.URL,
		DropOld:    true,
		DropLabels: []string{"cluster", "pod"},
	}
	cfgBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "demo.jsonl")
	_, _ = io.WriteString(fw, `{"metric":{"__name__":"demo","job":"preflight","instance":"i-1","cluster":"c1","pod":"p1"},"values":[1],"timestamps":[20000]}`)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	srv.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Summary struct {
			OverLabelLimit int         `json:"over_label_limit"`
			TotalLabels    int         `json:"total_labels"`
			LabelStats     []labelStat `json:"label_stats"`
		} `json:"summary"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Summary.OverLabelLimit != 0 {
		t.Fatalf("expected zero over-limit series after drop labels, got %d", resp.Summary.OverLabelLimit)
	}
	if len(resp.Summary.LabelStats) == 0 {
		t.Fatalf("expected label stats to be present")
	}
	if resp.Summary.TotalLabels != 5 {
		t.Fatalf("expected total_labels=5, got %d", resp.Summary.TotalLabels)
	}
	for _, warning := range resp.Warnings {
		if strings.Contains(warning, "maxLabelsPerTimeseries") {
			t.Fatalf("did not expect label-limit warning after drops, got %v", resp.Warnings)
		}
	}
}

func TestBuildLabelStatsSortedAndLimited(t *testing.T) {
	stats := buildLabelStats(map[string]int{
		"z": 3,
		"a": 3,
		"m": 5,
		"b": 1,
	}, 3)
	if len(stats) != 3 {
		t.Fatalf("expected 3 stats, got %d", len(stats))
	}
	if stats[0].Name != "m" || stats[0].Count != 5 {
		t.Fatalf("unexpected top stat: %+v", stats[0])
	}
	// tie on count=3 should be name ascending
	if stats[1].Name != "a" || stats[2].Name != "z" {
		t.Fatalf("unexpected tie ordering: %+v", stats)
	}
}

func TestAnalyzeBundleReportsAllDetectedLabels(t *testing.T) {
	metric := map[string]string{
		"__name__": "demo_total_labels",
		"job":      "preflight",
		"instance": "i-1",
	}
	for i := 0; i < 45; i++ {
		metric[fmt.Sprintf("label_%02d", i)] = "v"
	}
	lineBytes, err := json.Marshal(metricLine{
		Metric:     metric,
		Values:     []json.RawMessage{json.RawMessage("1")},
		Timestamps: []int64{recentTimestampMs()},
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	tmpPath := ensureTestFile(t, "bundle-total-labels.jsonl", func(w io.Writer) error {
		_, writeErr := fmt.Fprintf(w, "%s\n", lineBytes)
		return writeErr
	})
	srv := NewServer("test")
	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: int64(len(lineBytes)), ExtractedBytes: int64(len(lineBytes))}
	summary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	if summary.TotalLabels != 48 {
		t.Fatalf("expected total labels=48, got %d", summary.TotalLabels)
	}
	if len(summary.LabelStats) != 48 {
		t.Fatalf("expected all 48 label stats, got %d", len(summary.LabelStats))
	}
	if len(summary.LabelUniverse) != 48 {
		t.Fatalf("expected label universe size=48, got %d", len(summary.LabelUniverse))
	}
	if len(summary.LabelBitsets) != 1 {
		t.Fatalf("expected 1 label bitset row, got %d", len(summary.LabelBitsets))
	}
	if len(summary.LabelCounts) != 1 || summary.LabelCounts[0] != 48 {
		t.Fatalf("expected one series label count=48, got %v", summary.LabelCounts)
	}
	if len(summary.PointCounts) != 1 || summary.PointCounts[0] != 1 {
		t.Fatalf("expected one series point count=1, got %v", summary.PointCounts)
	}
	if summary.SimSeries != 1 {
		t.Fatalf("expected simulation_series=1, got %d", summary.SimSeries)
	}
}

func TestAnalyzeBundleSampleLimitAndFullCollection(t *testing.T) {
	tmpPath := ensureTestFile(t, "bundle-sample-limit.jsonl", func(w io.Writer) error {
		ts := recentTimestampMs()
		for i := 0; i < 2105; i++ {
			if _, err := fmt.Fprintf(w, `{"metric":{"__name__":"sample_limit_demo","job":"preflight","instance":"i-%d","label_%d":"x"},"values":[1],"timestamps":[%d]}`+"\n", i, i, ts); err != nil {
				return err
			}
		}
		return nil
	})

	srv := NewServer("test")
	bundle := &bundleInfo{
		MetricsPath:    tmpPath,
		OriginalBytes:  1,
		ExtractedBytes: 1,
	}

	sampleSummary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0, 0, nil, defaultAnalyzeSampleLines)
	if err != nil {
		t.Fatalf("sample analyze failed: %v", err)
	}
	if sampleSummary.ScannedLines != defaultAnalyzeSampleLines {
		t.Fatalf("expected scanned_lines=%d, got %d", defaultAnalyzeSampleLines, sampleSummary.ScannedLines)
	}
	if !sampleSummary.SampleCut {
		t.Fatalf("expected sample_cut=true for sample-limited analysis")
	}

	fullSummary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("full analyze failed: %v", err)
	}
	if fullSummary.ScannedLines != 2105 {
		t.Fatalf("expected full scanned_lines=2105, got %d", fullSummary.ScannedLines)
	}
	if fullSummary.SampleCut {
		t.Fatalf("expected sample_cut=false for full analysis")
	}
}

func TestSanitizeDropLabelsProtectsCoreLabels(t *testing.T) {
	got := sanitizeDropLabels([]string{"cluster", "job", "__name__", "instance", "pod", "cluster", ""})
	want := []string{"cluster", "pod"}
	if len(got) != len(want) {
		t.Fatalf("unexpected sanitized labels: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected sanitized labels: got %v want %v", got, want)
		}
	}
}

func TestMaxLabelsPerTimeseriesFromMetrics(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, "flag{name=\"maxLabelsPerTimeseries\", value=\"77\", is_set=\"true\"} 1\n")
	}))
	defer downstream.Close()

	srv := NewServer("test")
	limit := srv.maxLabelsPerTimeseries(context.Background(), uploadConfig{Endpoint: downstream.URL})
	if limit != 77 {
		t.Fatalf("expected maxLabelsPerTimeseries=77, got %d", limit)
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

func TestHandleAnalyzeIncludesLabelLimitWarning(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status/tsdb":
			w.WriteHeader(http.StatusNotFound)
		case "/metrics":
			_, _ = io.WriteString(w, "flag{name=\"retentionPeriod\", value=\"1d\", is_set=\"true\"} 1\n")
			_, _ = io.WriteString(w, "flag{name=\"maxLabelsPerTimeseries\", value=\"2\", is_set=\"true\"} 1\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, DropOld: true}
	cfgBytes, _ := json.Marshal(config)
	writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "demo.jsonl")
	_, _ = io.WriteString(fw, `{"metric":{"__name__":"demo","job":"preflight","instance":"i-1"},"values":[1],"timestamps":[20000]}`)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	srv.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		MaxLabelsLimit int      `json:"max_labels_limit"`
		Protected      []string `json:"protected_labels"`
		Summary        struct {
			OverLabelLimit int `json:"over_label_limit"`
			TotalLabels    int `json:"total_labels"`
		} `json:"summary"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.MaxLabelsLimit != 2 {
		t.Fatalf("expected max label limit 2, got %d", resp.MaxLabelsLimit)
	}
	if resp.Summary.OverLabelLimit == 0 {
		t.Fatalf("expected over label limit > 0, got %+v", resp.Summary)
	}
	if resp.Summary.TotalLabels == 0 {
		t.Fatalf("expected non-zero total labels in summary")
	}
	if len(resp.Protected) == 0 || resp.Protected[0] != "__name__" {
		t.Fatalf("expected protected label list in response, got %v", resp.Protected)
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "maxLabelsPerTimeseries=2") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected label limit warning, got %v", resp.Warnings)
	}
}

func TestHandleAnalyzeWarnsWhenMaxLabelsUnknownAndHighSeen(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status/tsdb":
			w.WriteHeader(http.StatusNotFound)
		case "/metrics":
			_, _ = io.WriteString(w, "flag{name=\"retentionPeriod\", value=\"1d\", is_set=\"true\"} 1\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, DropOld: true}
	cfgBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "labels.jsonl")
	metric := map[string]string{
		"__name__": "demo_labels",
		"job":      "preflight",
		"instance": "i-1",
	}
	for i := 0; i < 50; i++ {
		metric[fmt.Sprintf("label_%d", i)] = "x"
	}
	line, _ := json.Marshal(metricLine{
		Metric:     metric,
		Values:     []json.RawMessage{json.RawMessage("1")},
		Timestamps: []int64{recentTimestampMs()},
	})
	_, _ = io.WriteString(fw, string(line))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	srv.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		MaxLabelsLimit int `json:"max_labels_limit"`
		Summary        struct {
			MaxLabelsSeen int `json:"max_labels_seen"`
		} `json:"summary"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.MaxLabelsLimit != 0 {
		t.Fatalf("expected unknown max label limit (0), got %d", resp.MaxLabelsLimit)
	}
	if resp.Summary.MaxLabelsSeen <= 40 {
		t.Fatalf("expected max labels seen > 40, got %d", resp.Summary.MaxLabelsSeen)
	}
	found := false
	for _, warning := range resp.Warnings {
		if strings.Contains(warning, "could not be read") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning for unknown maxLabelsPerTimeseries, got %v", resp.Warnings)
	}
}

func TestHandleAnalyzeDefaultsToSampleModeWith2000Lines(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status/tsdb":
			w.WriteHeader(http.StatusNotFound)
		case "/metrics":
			_, _ = io.WriteString(w, "flag{name=\"retentionPeriod\", value=\"1d\", is_set=\"true\"} 1\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, DropOld: true}
	cfgBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "sample-default.jsonl")
	for i := 0; i < 2101; i++ {
		_, _ = io.WriteString(fw, fmt.Sprintf(`{"metric":{"__name__":"demo","job":"preflight","instance":"i-%d"},"values":[1],"timestamps":[%d]}`+"\n", i, recentTimestampMs()))
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	srv.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AnalysisMode string `json:"analysis_mode"`
		Summary      struct {
			ScannedLines int  `json:"scanned_lines"`
			SampleCut    bool `json:"sample_cut"`
			SampleLimit  int  `json:"sample_limit"`
		} `json:"summary"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.AnalysisMode != "sample" {
		t.Fatalf("expected analysis_mode=sample, got %q", resp.AnalysisMode)
	}
	if resp.Summary.ScannedLines != defaultAnalyzeSampleLines {
		t.Fatalf("expected scanned_lines=%d, got %d", defaultAnalyzeSampleLines, resp.Summary.ScannedLines)
	}
	if !resp.Summary.SampleCut {
		t.Fatalf("expected sample_cut=true")
	}
	if resp.Summary.SampleLimit != defaultAnalyzeSampleLines {
		t.Fatalf("expected sample_limit=%d, got %d", defaultAnalyzeSampleLines, resp.Summary.SampleLimit)
	}
	found := false
	for _, warning := range resp.Warnings {
		if strings.Contains(warning, "Use Full collection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sampling warning in %v", resp.Warnings)
	}
}

func TestHandleAnalyzeFullCollectionMode(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status/tsdb":
			w.WriteHeader(http.StatusNotFound)
		case "/metrics":
			_, _ = io.WriteString(w, "flag{name=\"retentionPeriod\", value=\"1d\", is_set=\"true\"} 1\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, DropOld: true}
	cfgBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(cfgBytes))
	_ = writer.WriteField("full_collection", "1")
	fw, _ := writer.CreateFormFile("bundle", "demo.jsonl")
	for i := 0; i < 2101; i++ {
		_, _ = io.WriteString(fw, fmt.Sprintf(`{"metric":{"__name__":"demo","job":"preflight","instance":"i-%d"},"values":[1],"timestamps":[%d]}`+"\n", i, recentTimestampMs()))
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	srv.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AnalysisMode string `json:"analysis_mode"`
		Summary      struct {
			ScannedLines int  `json:"scanned_lines"`
			SampleCut    bool `json:"sample_cut"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.AnalysisMode != "full" {
		t.Fatalf("expected analysis_mode=full, got %q", resp.AnalysisMode)
	}
	if resp.Summary.ScannedLines != 2101 {
		t.Fatalf("expected scanned_lines=2101, got %d", resp.Summary.ScannedLines)
	}
	if resp.Summary.SampleCut {
		t.Fatalf("expected sample_cut=false for full collection")
	}
}

func TestHandleAnalyzeUsesManualMaxLabelsOverride(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status/tsdb":
			w.WriteHeader(http.StatusNotFound)
		case "/metrics":
			_, _ = io.WriteString(w, "flag{name=\"retentionPeriod\", value=\"1d\", is_set=\"true\"} 1\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer downstream.Close()

	srv := NewServer("test")
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{
		Endpoint:          downstream.URL,
		DropOld:           true,
		MaxLabelsOverride: 20,
	}
	cfgBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(cfgBytes))
	fw, _ := writer.CreateFormFile("bundle", "labels.jsonl")
	metric := map[string]string{
		"__name__": "demo_labels",
		"job":      "preflight",
		"instance": "i-1",
	}
	for i := 0; i < 50; i++ {
		metric[fmt.Sprintf("label_%d", i)] = "x"
	}
	line, _ := json.Marshal(metricLine{
		Metric:     metric,
		Values:     []json.RawMessage{json.RawMessage("1")},
		Timestamps: []int64{recentTimestampMs()},
	})
	_, _ = io.WriteString(fw, string(line))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/analyze", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	srv.handleAnalyze(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		MaxLabelsLimit int    `json:"max_labels_limit"`
		MaxLabelsSrc   string `json:"max_labels_source"`
		Summary        struct {
			MaxLabelsSeen int `json:"max_labels_seen"`
			OverLabel     int `json:"over_label_limit"`
		} `json:"summary"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.MaxLabelsLimit != 20 {
		t.Fatalf("expected manual max labels limit 20, got %d", resp.MaxLabelsLimit)
	}
	if resp.MaxLabelsSrc != "manual" {
		t.Fatalf("expected max_labels_source=manual, got %q", resp.MaxLabelsSrc)
	}
	if resp.Summary.MaxLabelsSeen <= 20 {
		t.Fatalf("expected max labels seen > 20, got %d", resp.Summary.MaxLabelsSeen)
	}
	if resp.Summary.OverLabel == 0 {
		t.Fatalf("expected over_label_limit > 0 with manual limit override")
	}
	found := false
	for _, warning := range resp.Warnings {
		if strings.Contains(warning, "maxLabelsPerTimeseries=20") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning with manual limit 20, got %v", resp.Warnings)
	}
}

func TestStreamImportDropsSelectedLabelsButKeepsProtected(t *testing.T) {
	var imported []byte
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/import") {
			var err error
			imported, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("failed reading body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer downstream.Close()

	tmpPath := ensureTestFile(t, "demo-drop-labels.jsonl", func(w io.Writer) error {
		_, err := fmt.Fprintf(w, `{"metric":{"__name__":"demo","job":"j1","instance":"i1","cluster":"c1","pod":"p1"},"values":[1],"timestamps":[%d]}`+"\n", recentTimestampMs())
		return err
	})

	bundle := &bundleInfo{MetricsPath: tmpPath, OriginalBytes: 128, ExtractedBytes: 128}
	srv := NewServer("test")
	cfg := uploadConfig{DropLabels: []string{"cluster", "pod", "job"}}
	_, _, err := srv.streamImport(context.Background(), cfg, bundle, downstream.URL+"/api/v1/import", 0, 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("streamImport failed: %v", err)
	}
	if len(imported) == 0 {
		t.Fatalf("expected import body")
	}
	line := strings.TrimSpace(string(imported))
	var payload struct {
		Metric map[string]string `json:"metric"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("failed to decode import line: %v, line=%s", err, line)
	}
	if _, exists := payload.Metric["cluster"]; exists {
		t.Fatalf("expected cluster to be dropped, got %v", payload.Metric)
	}
	if _, exists := payload.Metric["pod"]; exists {
		t.Fatalf("expected pod to be dropped, got %v", payload.Metric)
	}
	if payload.Metric["job"] == "" || payload.Metric["instance"] == "" || payload.Metric["__name__"] == "" {
		t.Fatalf("protected labels must remain, got %v", payload.Metric)
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
	_, summary, err := srv.streamImport(context.Background(), cfg, bundle, downstream.URL+"/api/v1/import", 0, 0, 0, 0, nil)
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
	summary, err := srv.analyzeBundle(context.Background(), bundle, retentionCutoff, 0, 0, nil, 0)
	if err != nil {
		t.Fatalf("analyzeBundle failed: %v", err)
	}
	if summary.Start.IsZero() || summary.End.IsZero() {
		t.Fatalf("expected start/end set, got %+v", summary)
	}
	if summary.DroppedOld == 0 {
		t.Fatalf("expected drops under retention cutoff, got %+v", summary)
	}
	warnings := buildAnalysisWarnings(summary, retentionCutoff, 0)
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
	summary, err := srv.analyzeBundle(context.Background(), bundle, 0, 0, 0, nil, 0)
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
	w := buildAnalysisWarnings(summary, cutoff.UnixMilli(), 0)
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
