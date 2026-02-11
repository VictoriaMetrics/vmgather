package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleDownload_PathTraversal(t *testing.T) {
	// Setup temporary directories
	tempDir, err := os.MkdirTemp("", "vmgather-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	outputDir := filepath.Join(tempDir, "exports")
	if err := os.Mkdir(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a valid file inside outputDir
	validFile := filepath.Join(outputDir, "valid.zip")
	if err := os.WriteFile(validFile, []byte("valid content"), 0644); err != nil {
		t.Fatalf("Failed to create valid file: %v", err)
	}

	// Create a secret file outside outputDir
	secretFile := filepath.Join(tempDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("secret content"), 0644); err != nil {
		t.Fatalf("Failed to create secret file: %v", err)
	}

	// Create a symlink inside outputDir pointing to the secret file (symlink escape attempt).
	// Skip if the platform does not support symlinks or they are not permitted.
	symlinkPath := filepath.Join(outputDir, "symlink.zip")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		symlinkPath = ""
	}

	// Initialize server
	srv := NewServer(outputDir, "test", false)
	handler := srv.Router()

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "Valid download",
			path:           validFile,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Path traversal attempt (../)",
			path:           filepath.Join(outputDir, "../secret.txt"),
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Absolute path to secret",
			path:           secretFile,
			expectedStatus: http.StatusForbidden,
		},
	}
	if symlinkPath != "" {
		tests = append(tests, struct {
			name           string
			path           string
			expectedStatus int
		}{
			name:           "Symlink inside outputDir pointing outside",
			path:           symlinkPath,
			expectedStatus: http.StatusForbidden,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/api/download?path="+tt.path, nil)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					status, tt.expectedStatus)
			}
		})
	}
}
