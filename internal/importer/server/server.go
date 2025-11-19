package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

type uploadConfig struct {
	Endpoint      string `json:"endpoint"`
	TenantID      string `json:"tenant_id"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	SkipTLSVerify bool   `json:"skip_tls_verify"`
}

type uploadResult struct {
	BytesSent   int    `json:"bytes_sent"`
	RemotePath  string `json:"remote_path"`
	StatusCode  int    `json:"status_code"`
	Message     string `json:"message"`
	ContentType string `json:"content_type"`
}

// Server handles VMImport UI and API endpoints.
type Server struct {
	version    string
	httpClient *http.Client
}

func NewServer(version string) *Server {
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: false}}
	return &Server{
		version: version,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
		},
	}
}

func (s *Server) withInsecure(insecure bool) *http.Client {
	if !insecure {
		return s.httpClient
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 - intentional for air-gapped envs
	return &http.Client{Timeout: 60 * time.Second, Transport: transport}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": s.version})
	})
	mux.HandleFunc("/api/upload", s.handleUpload)

	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f, err := staticFS.Open("index.html")
		if err != nil {
			http.Error(w, "UI missing", http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("failed to stream UI: %v", err)
		}
	})
	return mux
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse form: %v", err))
		return
	}

	cfgRaw := r.FormValue("config")
	if cfgRaw == "" {
		respondWithError(w, http.StatusBadRequest, "missing config payload")
		return
	}
	var cfg uploadConfig
	if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid config: %v", err))
		return
	}
	file, header, err := r.FormFile("bundle")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "bundle file is required")
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to read bundle: %v", err))
		return
	}
	if len(data) == 0 {
		respondWithError(w, http.StatusBadRequest, "bundle is empty")
		return
	}

	result, err := s.sendToEndpoint(r.Context(), cfg, header.Filename, data)
	if err != nil {
		respondWithError(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) sendToEndpoint(ctx context.Context, cfg uploadConfig, fileName string, data []byte) (*uploadResult, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/api/v1/import"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	contentType := detectContentType(fileName)
	req.Header.Set("Content-Type", contentType)
	if cfg.TenantID != "" {
		req.Header.Set("X-Vm-AccountID", cfg.TenantID)
		req.Header.Set("X-Vm-TenantID", cfg.TenantID)
	}
	if cfg.Username != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}

	client := s.withInsecure(cfg.SkipTLSVerify)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote import failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote responded %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
	}

	return &uploadResult{
		BytesSent:   len(data),
		RemotePath:  endpoint,
		StatusCode:  resp.StatusCode,
		Message:     strings.TrimSpace(string(bodyBytes)),
		ContentType: contentType,
	}, nil
}

func detectContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip":
		return "application/zip"
	default:
		return "application/jsonl"
	}
}

func respondWithError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
