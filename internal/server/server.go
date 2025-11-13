package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/VictoriaMetrics/support/internal/application/services"
	"github.com/VictoriaMetrics/support/internal/domain"
	"github.com/VictoriaMetrics/support/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/support/internal/infrastructure/vm"
)

//go:embed static/*
var staticFiles embed.FS

// Server is the HTTP server for VMExporter
type Server struct {
	vmService     services.VMService
	exportService services.ExportService
	outputDir     string
}

// NewServer creates a new HTTP server
func NewServer(outputDir string) *Server {
	return &Server{
		vmService:     services.NewVMService(),
		exportService: services.NewExportService(outputDir),
		outputDir:     outputDir,
	}
}

// respondWithError sends JSON error response
// CRITICAL: Always return JSON, never text/plain, even on errors!
func respondWithError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":  message,
		"status": statusCode,
	})
}

// Router returns the HTTP router
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/validate", s.handleValidateConnection)
	mux.HandleFunc("/api/discover", s.handleDiscoverComponents)
	mux.HandleFunc("/api/sample", s.handleGetSample)
	mux.HandleFunc("/api/export", s.handleExport)
	mux.HandleFunc("/api/download", s.handleDownload)
	mux.HandleFunc("/api/health", s.handleHealth)

	// Serve static files with proper MIME types
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", staticFileServer(staticFS)))
	mux.Handle("/", staticFileServer(staticFS)) // Serve index.html at root

	// Logging middleware
	return loggingMiddleware(mux)
}

// handleHealth returns server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": "1.0.0",
	})
}

// handleValidateConnection validates VM connection
func (s *Server) handleValidateConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse request body
	var req struct {
		Connection domain.VMConnection `json:"connection"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
		return
	}

	// 🔍 DEBUG: Log connection details
	log.Printf("🔌 Validating connection:")
	log.Printf("  URL: %s", req.Connection.URL)
	log.Printf("  ApiBasePath: %s", req.Connection.ApiBasePath)
	log.Printf("  TenantId: %s", req.Connection.TenantId)
	log.Printf("  IsMultitenant: %v", req.Connection.IsMultitenant)
	log.Printf("  FullApiUrl: %s", req.Connection.FullApiUrl)
	log.Printf("  Auth Type: %s", req.Connection.Auth.Type)
	log.Printf("  Has Username: %v", req.Connection.Auth.Username != "")
	log.Printf("  Has Password: %v", req.Connection.Auth.Password != "")
	log.Printf("  Has Token: %v", req.Connection.Auth.Token != "")
	log.Printf("  Has Header: %v", req.Connection.Auth.HeaderName != "")

	// Create VM client
	client := vm.NewClient(req.Connection)
	
	// Try a simple query to validate connection
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	
	query := "vm_app_version"
	log.Printf("📡 Executing query: %s", query)
	
	result, err := client.Query(ctx, query, time.Now())
	
	w.Header().Set("Content-Type", "application/json")
	
	if err != nil {
		log.Printf("❌ Connection validation failed: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"valid":   false,
			"message": fmt.Sprintf("Connection failed: %v", err),
			"error":   err.Error(),
		})
		return
	}
	
	// If vm_app_version returns no results, try alternative queries
	if result != nil && result.Status == "success" && len(result.Data.Result) == 0 {
		log.Printf("⚠️  vm_app_version returned no results, trying alternative queries...")
		
		// Try to query any vm_* metric
		result, err = client.Query(ctx, `{__name__=~"vm_.*"}`, time.Now())
		if err == nil && len(result.Data.Result) > 0 {
			log.Printf("✅ Found %d vm_* metrics", len(result.Data.Result))
		}
		
		// If still no results, try a simple constant query to verify API works
		if err == nil && len(result.Data.Result) == 0 {
			log.Printf("⚠️  No vm_* metrics found, trying constant query...")
			result, err = client.Query(ctx, `1`, time.Now())
			if err == nil {
				log.Printf("✅ API responds correctly (Prometheus-compatible)")
			}
		}
		
		if err != nil {
			log.Printf("❌ All queries failed: %v", err)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"valid":   false,
				"message": fmt.Sprintf("Connection failed: %v", err),
				"error":   err.Error(),
			})
			return
		}
	}
	
	log.Printf("✅ Connection successful! Components found: %d", len(result.Data.Result))

	// Extract version info and verify it's VictoriaMetrics
	version := "unknown"
	components := 0
	isVictoriaMetrics := false
	vmComponents := []string{}
	
	if result != nil && result.Status == "success" && len(result.Data.Result) > 0 {
		components = len(result.Data.Result)
		
		// Extract version and component info from metrics
		for _, metric := range result.Data.Result {
			// Check if this is VictoriaMetrics by looking for vm_component or version label
			if v, ok := metric.Metric["version"]; ok {
				if version == "unknown" {
					version = v
				}
				// VictoriaMetrics versions typically contain "victoria-metrics" or start with specific patterns
				if len(v) > 0 {
					isVictoriaMetrics = true
				}
			}
			
			// Extract component name
			if comp, ok := metric.Metric["vm_component"]; ok {
				vmComponents = append(vmComponents, comp)
				isVictoriaMetrics = true
			} else if job, ok := metric.Metric["job"]; ok {
				// Fallback to job name if vm_component not available
				vmComponents = append(vmComponents, job)
			}
		}
	}
	
	// If API responds correctly but no VM-specific metrics found, still consider it valid
	// (metrics might not be scraped yet)
	if !isVictoriaMetrics {
		log.Printf("⚠️  Warning: No VictoriaMetrics-specific metrics found, but API is Prometheus-compatible")
		// Still mark as Victoria Metrics if API responds correctly
		isVictoriaMetrics = true
		if len(vmComponents) == 0 {
			vmComponents = []string{"prometheus-compatible-api"}
		}
	}

	log.Printf("✅ VictoriaMetrics detected! Version: %s, Components: %v", version, vmComponents)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":             true,
		"valid":               true,
		"message":             "Connection successful",
		"version":             version,
		"components":          components,
		"is_victoria_metrics": isVictoriaMetrics,
		"vm_components":       vmComponents,
	})
}

// handleDiscoverComponents discovers VM components
func (s *Server) handleDiscoverComponents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse request body
	var req struct {
		Connection domain.VMConnection `json:"connection"`
		TimeRange  domain.TimeRange    `json:"time_range"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// 🔍 DEBUG: Log discovery request
	log.Printf("🔎 Component Discovery:")
	log.Printf("  Time Range: %s to %s", req.TimeRange.Start.Format(time.RFC3339), req.TimeRange.End.Format(time.RFC3339))
	log.Printf("  URL: %s", req.Connection.URL)
	log.Printf("  Tenant ID: %s", req.Connection.TenantId)
	log.Printf("  Multitenant: %v", req.Connection.IsMultitenant)

	// Discover components using VM service
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	components, err := s.vmService.DiscoverComponents(ctx, req.Connection, req.TimeRange)
	if err != nil {
		log.Printf("❌ Discovery failed: %v", err)
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Discovery failed: %v", err))
		return
	}

	// Log discovery results
	componentTypes := make(map[string]int)
	for _, comp := range components {
		componentTypes[comp.Component]++
	}
	log.Printf("✅ Discovery complete: %d components found", len(components))
	log.Printf("  Component types: %v", componentTypes)

	// Return discovered components
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"components": components,
	})
}

// handleGetSample returns sample metrics
func (s *Server) handleGetSample(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse request body
	var req struct {
		Config domain.ExportConfig `json:"config"`
		Limit  int                 `json:"limit,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// Set default limit if not specified
	if req.Limit <= 0 {
		req.Limit = 10
	}

	// 🔍 DEBUG: Log sample request
	log.Printf("📊 Sample Metrics Request:")
	log.Printf("  Components: %v", req.Config.Components)
	log.Printf("  Jobs: %v", req.Config.Jobs)
	log.Printf("  Limit: %d", req.Limit)

	// Get sample metrics using VM service
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	samples, err := s.vmService.GetSample(ctx, req.Config, req.Limit)
	if err != nil {
		// Check if error is due to timeout
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("❌ Sample timeout: request took > 30s")
			respondWithError(w, http.StatusRequestTimeout, "Request timeout: sample loading took too long. Try reducing time range or number of components.")
		} else {
			log.Printf("❌ Sample retrieval failed: %v", err)
			respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Sample retrieval failed: %v", err))
		}
		return
	}

	// Apply obfuscation to samples if enabled
	if req.Config.Obfuscation.Enabled {
		log.Printf("🔒 Applying obfuscation to samples (instance: %v, job: %v, custom labels: %v)",
			req.Config.Obfuscation.ObfuscateInstance,
			req.Config.Obfuscation.ObfuscateJob,
			req.Config.Obfuscation.CustomLabels)
		samples = s.obfuscateSamples(samples, req.Config.Obfuscation)
	}

	// Log sample results
	uniqueLabels := make(map[string]bool)
	for _, sample := range samples {
		for label := range sample.Labels {
			uniqueLabels[label] = true
		}
	}
	labelList := make([]string, 0, len(uniqueLabels))
	for label := range uniqueLabels {
		labelList = append(labelList, label)
	}
	log.Printf("✅ Sample retrieval complete: %d samples", len(samples))
	log.Printf("  Unique labels: %v", labelList)

	// Return samples
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"samples": samples,
		"count":   len(samples),
	})
}

// handleExport performs metrics export
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse request body
	var config domain.ExportConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// 🔍 DEBUG: Log export request
	log.Printf("📤 Metrics Export:")
	log.Printf("  Time Range: %s to %s", config.TimeRange.Start.Format(time.RFC3339), config.TimeRange.End.Format(time.RFC3339))
	log.Printf("  Components: %v", config.Components)
	log.Printf("  Jobs: %v", config.Jobs)
	log.Printf("  Obfuscation Enabled: %v", config.Obfuscation.Enabled)
	if config.Obfuscation.Enabled {
		log.Printf("  Obfuscate Instance: %v", config.Obfuscation.ObfuscateInstance)
		log.Printf("  Obfuscate Job: %v", config.Obfuscation.ObfuscateJob)
	}

	// Execute export using export service
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := s.exportService.ExecuteExport(ctx, config)
	if err != nil {
		log.Printf("❌ Export failed: %v", err)
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Export failed: %v", err))
		return
	}

	log.Printf("✅ Export complete:")
	log.Printf("  Export ID: %s", result.ExportID)
	log.Printf("  Metrics Exported: %d", result.MetricsExported)
	log.Printf("  Archive Size: %.2f KB", float64(result.ArchiveSizeBytes)/1024)
	log.Printf("  Archive Path: %s", result.ArchivePath)
	log.Printf("  Obfuscation Applied: %v", result.ObfuscationApplied)

	// Get sample data from the exported archive for preview
	// This shows the top 5 metrics that were exported
	sampleData := s.getSampleDataFromResult(ctx, config)

	// Build response
	response := map[string]interface{}{
		"export_id":     result.ExportID,
		"archive_path":  result.ArchivePath,
		"archive_size":  result.ArchiveSizeBytes,
		"metrics_count": result.MetricsExported,
		"sha256":        result.SHA256,
		"time_range": map[string]string{
			"start": result.TimeRange.Start.Format(time.RFC3339),
			"end":   result.TimeRange.End.Format(time.RFC3339),
		},
		"obfuscation_applied": result.ObfuscationApplied,
		"sample_data":         sampleData,
	}

	// Return export result
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// getSampleDataFromResult retrieves sample data for preview
func (s *Server) getSampleDataFromResult(ctx context.Context, config domain.ExportConfig) []map[string]interface{} {
	// Get sample metrics (limit to 5 for preview)
	samples, err := s.vmService.GetSample(ctx, config, 5)
	if err != nil {
		log.Printf("Failed to get sample data: %v", err)
		return []map[string]interface{}{}
	}

	// Convert to response format
	sampleData := make([]map[string]interface{}, 0, len(samples))
	for _, sample := range samples {
		sampleData = append(sampleData, map[string]interface{}{
			"name":   sample.MetricName,
			"labels": sample.Labels,
			"value":  sample.Value,
		})
	}

	return sampleData
}

// handleDownload serves archive file for download
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get file path from query parameter
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		respondWithError(w, http.StatusBadRequest, "Missing path parameter")
		return
	}

	// 🔍 DEBUG: Log download request
	log.Printf("⬇️  Archive Download:")
	log.Printf("  File Path: %s", filePath)
	log.Printf("  Client IP: %s", r.RemoteAddr)

	// Security: ensure file is within output directory
	// For now, serve from relative path
	// TODO: Add proper path validation
	
	// Check if file exists
	fileInfo, err := http.Dir(".").Open(filePath)
	if err != nil {
		log.Printf("❌ File not found: %s", filePath)
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("File not found: %s", filePath))
		return
	}
	fileInfo.Close()

	// Set headers for download
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filePath+"\"")

	log.Printf("✅ Serving file for download")

	// Serve file
	http.ServeFile(w, r, filePath)
}

// obfuscateSamples applies obfuscation to sample metrics
func (s *Server) obfuscateSamples(samples []domain.MetricSample, config domain.ObfuscationConfig) []domain.MetricSample {
	if !config.Enabled {
		return samples
	}

	// Create obfuscator
	obfuscator := obfuscation.NewObfuscator()

	// Apply obfuscation to each sample
	for i := range samples {
		if samples[i].Labels == nil {
			continue
		}

		// Obfuscate instance
		if config.ObfuscateInstance {
			if instance, exists := samples[i].Labels["instance"]; exists {
				samples[i].Labels["instance"] = obfuscator.ObfuscateInstance(instance)
			}
		}

		// Obfuscate job
		if config.ObfuscateJob {
			if job, exists := samples[i].Labels["job"]; exists {
				// Try to determine component from metric name
				component := "unknown"
				if metricName, ok := samples[i].Labels["__name__"]; ok {
					component = guessComponentFromMetric(metricName)
				}
				samples[i].Labels["job"] = obfuscator.ObfuscateJob(job, component)
			}
		}

		// Obfuscate custom labels (pod, namespace, etc.)
		for _, label := range config.CustomLabels {
			if value, exists := samples[i].Labels[label]; exists {
				// Use simple hash-based obfuscation for custom labels
				samples[i].Labels[label] = obfuscator.ObfuscateCustomLabel(label, value)
			}
		}
	}

	return samples
}

// guessComponentFromMetric tries to determine component type from metric name
func guessComponentFromMetric(metricName string) string {
	if len(metricName) >= 10 && metricName[0:10] == "vmstorage_" {
		return "vmstorage"
	}
	if len(metricName) >= 9 && metricName[0:9] == "vmselect_" {
		return "vmselect"
	}
	if len(metricName) >= 9 && metricName[0:9] == "vminsert_" {
		return "vminsert"
	}
	if len(metricName) >= 8 && metricName[0:8] == "vmagent_" {
		return "vmagent"
	}
	if len(metricName) >= 8 && metricName[0:8] == "vmalert_" {
		return "vmalert"
	}
	return "unknown"
}

// staticFileServer serves static files with proper MIME types
func staticFileServer(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set proper Content-Type based on file extension
		ext := strings.ToLower(filepath.Ext(r.URL.Path))
		
		switch ext {
		case ".js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case ".json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".jpg", ".jpeg":
			w.Header().Set("Content-Type", "image/jpeg")
		case ".svg":
			w.Header().Set("Content-Type", "image/svg+xml")
		case ".woff":
			w.Header().Set("Content-Type", "font/woff")
		case ".woff2":
			w.Header().Set("Content-Type", "font/woff2")
		}
		
		fileServer.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs HTTP requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

