package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleUploadSuccess(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/jsonl" {
			t.Fatalf("unexpected content type %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Vm-TenantID") != "42" {
			t.Fatalf("tenant header missing")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer downstream.Close()

	srv := httptest.NewServer(NewServer("test").Router())
	defer srv.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	config := uploadConfig{Endpoint: downstream.URL, TenantID: "42"}
	configBytes, _ := json.Marshal(config)
	_ = writer.WriteField("config", string(configBytes))
	fileWriter, _ := writer.CreateFormFile("bundle", "test.jsonl")
	_, _ = fileWriter.Write([]byte("{\"metric\":1}"))
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
