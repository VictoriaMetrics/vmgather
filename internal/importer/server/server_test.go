package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	_, _ = fileWriter.Write([]byte(`{"metric":{"__name__":"test_metric","job":"demo"},"timestamps":[1763052540000]}`))
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
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer downstream.Close()

	var zipBuffer bytes.Buffer
	zw := zip.NewWriter(&zipBuffer)
	mw, _ := zw.Create("metrics.jsonl")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(mw, `{"metric":{"__name__":"demo","job":"zip","idx":"%d"},"values":[%d],"timestamps":[1763052540000]}`+"\n", i, i)
	}
	meta, _ := zw.Create("metadata.json")
	meta.Write([]byte(`{"time_range":{"start":"2025-01-01T00:00:00Z","end":"2025-01-01T00:05:00Z"},"metrics_count":5}`))
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
