package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/VictoriaMetrics/VMGather/internal/application/services"
	"github.com/VictoriaMetrics/VMGather/internal/domain"
	"github.com/VictoriaMetrics/VMGather/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/VMGather/internal/infrastructure/vm"
)

//go:embed static/*
var staticFiles embed.FS

// Server is the HTTP server for VMGather
type Server struct {
	vmService     services.VMService
	exportService services.ExportService
	jobManager    *ExportJobManager
	outputDir     string
	version       string
	debug         bool
}

// NewServer creates a new HTTP server
func NewServer(outputDir, version string, debug bool) *Server {
	if version == "" {
		version = "dev"
	}
	server := &Server{
		vmService:     services.NewVMService(),
		exportService: services.NewExportService(outputDir, version),
		jobManager:    nil,
		outputDir:     outputDir,
		version:       version,
		debug:         debug,
	}
	server.jobManager = NewExportJobManager(server.exportService)
	return server
}

// respondWithError sends JSON error response
// CRITICAL: Always return JSON, never text/plain, even on errors!
func respondWithError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
	mux.HandleFunc("/api/export/start", s.handleExportStart)
	mux.HandleFunc("/api/export/resume", s.handleExportResume)
	mux.HandleFunc("/api/export/status", s.handleExportStatus)
	mux.HandleFunc("/api/fs/list", s.handleListDirectory)
	mux.HandleFunc("/api/fs/check", s.handleCheckDirectory)
	mux.HandleFunc("/api/export/cancel", s.handleExportCancel)
	mux.HandleFunc("/api/config", s.handleConfig)
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
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	defaultDir := recommendedStagingDir()
	response := map[string]interface{}{
		"version":              s.version,
		"default_staging_dir":  defaultDir,
		"os":                   runtime.GOOS,
		"output_dir":           s.outputDir,
		"supports_dir_picker":  true,
		"supports_dir_prepare": true,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
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

	// DEBUG: Log connection details
	log.Printf("Validating connection:")
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
	log.Printf("Executing query: %s", query)

	result, err := client.Query(ctx, query, time.Now())

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		log.Printf("[ERROR] Connection validation failed: %v", err)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"valid":   false,
			"message": fmt.Sprintf("Connection failed: %v", err),
			"error":   err.Error(),
		})
		return
	}

	// If vm_app_version returns no results, try alternative queries
	if result != nil && result.Status == "success" && len(result.Data.Result) == 0 {
		log.Printf("[WARN] vm_app_version returned no results, trying alternative queries...")

		// Try to query any vm_* metric
		result, err = client.Query(ctx, `{__name__=~"vm_.*"}`, time.Now())
		if err == nil && len(result.Data.Result) > 0 {
			log.Printf("[OK] Found %d vm_* metrics", len(result.Data.Result))
		}

		// If still no results, try a simple constant query to verify API works
		if err == nil && len(result.Data.Result) == 0 {
			log.Printf("[WARN] No vm_* metrics found, trying constant query...")
			result, err = client.Query(ctx, `1`, time.Now())
			if err == nil {
				log.Printf("[OK] API responds correctly (Prometheus-compatible)")
			}
		}

		if err != nil {
			log.Printf("[ERROR] All queries failed: %v", err)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"valid":   false,
				"message": fmt.Sprintf("Connection failed: %v", err),
				"error":   err.Error(),
			})
			return
		}
	}

	// Extract version info and verify it's VictoriaMetrics
	version := "unknown"
	components := 0
	isVictoriaMetrics := false
	vmComponents := []string{}

	if result != nil && result.Status == "success" && len(result.Data.Result) > 0 {
		log.Printf("[OK] Connection successful! Components found: %d", len(result.Data.Result))
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
		log.Printf("[WARN] Warning: No VictoriaMetrics-specific metrics found, but API is Prometheus-compatible")
		// Still mark as Victoria Metrics if API responds correctly
		isVictoriaMetrics = true
		if len(vmComponents) == 0 {
			vmComponents = []string{"prometheus-compatible-api"}
		}
	}

	log.Printf("[OK] VictoriaMetrics detected! Version: %s, Components: %v", version, vmComponents)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
	var request struct {
		Connection domain.VMConnection `json:"connection"`
		TimeRange  domain.TimeRange    `json:"time_range"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// Propagate debug flag
	if s.debug {
		request.Connection.Debug = true
	}

	// DEBUG: Log discovery request
	log.Printf("🔎 Component Discovery:")
	log.Printf("  Time Range: %s to %s", request.TimeRange.Start.Format(time.RFC3339), request.TimeRange.End.Format(time.RFC3339))
	log.Printf("  URL: %s", request.Connection.URL)
	log.Printf("  Tenant ID: %s", request.Connection.TenantId)
	log.Printf("  Multitenant: %v", request.Connection.IsMultitenant)

	// Discover components using VM service
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	components, err := s.vmService.DiscoverComponents(ctx, request.Connection, request.TimeRange)
	if err != nil {
		// If discovery fails and the client provided an ApiBasePath (common when users paste full /prometheus URLs),
		// retry without the path so we still find VM components on single-node endpoints.
		if request.Connection.ApiBasePath != "" && !request.Connection.IsMultitenant {
			log.Printf("[WARN] Discovery failed with api_base_path=%q, retrying without base path...", request.Connection.ApiBasePath)
			fallbackConn := request.Connection
			fallbackConn.ApiBasePath = ""
			fallbackConn.FullApiUrl = ""

			components, err = s.vmService.DiscoverComponents(ctx, fallbackConn, request.TimeRange)
			if err != nil {
				log.Printf("[ERROR] Discovery retry without base path failed: %v", err)
				respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("No VictoriaMetrics component metrics found at the provided URL: %v", err))
				return
			}
			// Success on fallback
			request.Connection = fallbackConn
		} else {
			log.Printf("[ERROR] Discovery failed: %v", err)
			respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("No VictoriaMetrics component metrics found at the provided URL: %v", err))
			return
		}
	}

	// Log discovery results
	componentTypes := make(map[string]int)
	for _, comp := range components {
		componentTypes[comp.Component]++
	}
	log.Printf("[OK] Discovery complete: %d components found", len(components))
	log.Printf("  Component types: %v", componentTypes)

	// Return discovered components
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

	// Propagate debug flag
	if s.debug {
		req.Config.Connection.Debug = true
	}

	// Set default limit if not specified
	if req.Limit <= 0 {
		req.Limit = 10
	}

	// DEBUG: Log sample request
	log.Printf("Sample Metrics Request:")
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
			log.Printf("[ERROR] Sample timeout: request took > 30s")
			respondWithError(w, http.StatusRequestTimeout, "Request timeout: sample loading took too long. Try reducing time range or number of components.")
		} else {
			log.Printf("[ERROR] Sample retrieval failed: %v", err)
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
	log.Printf("[OK] Sample retrieval complete: %d samples", len(samples))
	log.Printf("  Unique labels: %v", labelList)

	// Convert samples to response format with 'name' field for frontend compatibility
	sampleData := make([]map[string]interface{}, 0, len(samples))
	for _, sample := range samples {
		// Ensure metric name is never empty
		metricName := sample.MetricName
		if metricName == "" {
			// Fallback to __name__ label if MetricName is empty
			if labels := sample.Labels; labels != nil {
				if name, exists := labels["__name__"]; exists {
					metricName = name
				}
			}
			// Final fallback
			if metricName == "" {
				metricName = "unknown"
			}
		}

		sampleData = append(sampleData, map[string]interface{}{
			"name":        metricName,        // Frontend expects 'name' field
			"metric_name": sample.MetricName, // Keep for backward compatibility
			"labels":      sample.Labels,
			"value":       sample.Value,
			"timestamp":   sample.Timestamp,
		})
	}

	// Return samples
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"samples": sampleData,
		"count":   len(sampleData),
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

	ensureBatchDefaults(&config)

	// Propagate debug flag
	if s.debug {
		config.Connection.Debug = true
	}

	// DEBUG: Log export request
	if s.debug {
		log.Printf("[SEND] Metrics Export:")
		log.Printf("  Time Range: %s to %s", config.TimeRange.Start.Format(time.RFC3339), config.TimeRange.End.Format(time.RFC3339))
		log.Printf("  Components: %v", config.Components)
		log.Printf("  Jobs: %v", config.Jobs)
		log.Printf("  Obfuscation Enabled: %v", config.Obfuscation.Enabled)
		if config.Obfuscation.Enabled {
			log.Printf("  Obfuscate Instance: %v", config.Obfuscation.ObfuscateInstance)
			log.Printf("  Obfuscate Job: %v", config.Obfuscation.ObfuscateJob)
		}
	}

	// Execute export using export service
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := s.exportService.ExecuteExport(ctx, config)
	if err != nil {
		log.Printf("[ERROR] Export failed: %v", err)
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Export failed: %v", err))
		return
	}

	log.Printf("[OK] Export complete:")
	log.Printf("  Export ID: %s", result.ExportID)
	log.Printf("  Metrics Exported: %d", result.MetricsExported)
	log.Printf("  Archive Size: %.2f KB", float64(result.ArchiveSizeBytes)/1024)
	log.Printf("  Archive Path: %s", result.ArchivePath)
	log.Printf("  Obfuscation Applied: %v", result.ObfuscationApplied)

	// Get sample data from the exported archive for preview
	// This shows the top 5 metrics that were exported
	sampleData, sampleErr := s.getSampleDataFromResult(ctx, config)
	var sampleErrorMsg string
	if sampleErr != nil {
		sampleErrorMsg = sampleErr.Error()
	}

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

	if sampleErrorMsg != "" {
		response["sample_error"] = sampleErrorMsg
	}

	// Return export result
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handleExportStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var config domain.ExportConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	ensureBatchDefaults(&config)
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	stagingDir := config.StagingDir
	if stagingDir == "" {
		stagingDir = recommendedStagingDir()
	}
	absDir, err := filepath.Abs(stagingDir)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid staging directory: %v", err))
		return
	}
	stagingDir = absDir
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to prepare staging directory: %v", err))
		return
	}
	// Check write permission by creating temp file
	testFile := filepath.Join(stagingDir, ".vmgather-write-test")
	testHandle, err := os.Create(testFile)
	if err != nil {
		respondWithError(w, http.StatusForbidden, fmt.Sprintf("Cannot write to staging directory %s: %v", stagingDir, err))
		return
	}
	_ = testHandle.Close()
	_ = os.Remove(testFile)

	config.StagingDir = stagingDir
	config.StagingFile = filepath.Join(stagingDir, fmt.Sprintf("%s.partial.jsonl", jobID))

	status, err := s.jobManager.StartJob(r.Context(), jobID, config)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start export: %v", err))
		return
	}
	response := map[string]interface{}{
		"job_id":               status.ID,
		"state":                status.State,
		"total_batches":        status.TotalBatches,
		"batch_window_seconds": status.BatchWindowSeconds,
		"staging_path":         config.StagingFile,
		"obfuscation_enabled":  status.ObfuscationEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handleExportResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	if req.JobID == "" {
		respondWithError(w, http.StatusBadRequest, "Missing job_id")
		return
	}

	status, err := s.jobManager.ResumeJob(r.Context(), req.JobID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to resume export: %v", err))
		return
	}
	response := map[string]interface{}{
		"job_id":               status.ID,
		"state":                status.State,
		"total_batches":        status.TotalBatches,
		"completed_batches":    status.CompletedBatches,
		"batch_window_seconds": status.BatchWindowSeconds,
		"staging_path":         status.StagingPath,
		"obfuscation_enabled":  status.ObfuscationEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handleExportStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	jobID := r.URL.Query().Get("id")
	if jobID == "" {
		respondWithError(w, http.StatusBadRequest, "Missing id parameter")
		return
	}

	status, ok := s.jobManager.GetStatus(jobID)
	if !ok {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Job %s not found", jobID))
		return
	}
	log.Printf("[EXPORT][HTTP] status job_id=%s state=%s completed=%d total=%d staging=%s",
		status.ID, status.State, status.CompletedBatches, status.TotalBatches, status.StagingPath)

	response := map[string]interface{}{
		"job_id":                      status.ID,
		"state":                       status.State,
		"total_batches":               status.TotalBatches,
		"completed_batches":           status.CompletedBatches,
		"progress":                    status.Progress,
		"metrics_processed":           status.MetricsProcessed,
		"batch_window_seconds":        status.BatchWindowSeconds,
		"average_batch_seconds":       status.AverageBatchSeconds,
		"last_batch_duration_seconds": status.LastBatchDurationSeconds,
	}
	if status.StagingPath != "" {
		response["staging_path"] = status.StagingPath
	}

	if status.StartedAt != nil {
		response["started_at"] = status.StartedAt.Format(time.RFC3339)
	}
	if status.CompletedAt != nil {
		response["completed_at"] = status.CompletedAt.Format(time.RFC3339)
	}
	if status.ETA != nil {
		response["eta"] = status.ETA.Format(time.RFC3339)
	}
	if status.Error != "" {
		response["error"] = status.Error
	}
	if status.Result != nil {
		response["result"] = status.Result
	}
	if status.CurrentRange != nil {
		response["current_range"] = map[string]string{
			"start": status.CurrentRange.Start.Format(time.RFC3339),
			"end":   status.CurrentRange.End.Format(time.RFC3339),
		}
	}
	if status.StagingPath != "" {
		response["staging_path"] = status.StagingPath
	}
	response["obfuscation_enabled"] = status.ObfuscationEnabled

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handleExportCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	if req.JobID == "" {
		respondWithError(w, http.StatusBadRequest, "job_id is required")
		return
	}
	if err := s.jobManager.CancelJob(req.JobID); err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"canceled": true,
		"job_id":   req.JobID,
	})
}

func ensureBatchDefaults(config *domain.ExportConfig) {
	settings := &config.Batching
	if !settings.Enabled && settings.Strategy == "" && settings.CustomIntervalSecs == 0 {
		settings.Enabled = true
	}
	if settings.Strategy == "" {
		settings.Strategy = "auto"
	}
	if settings.CustomIntervalSecs < 0 {
		settings.CustomIntervalSecs = 0
	}
	minSeconds := services.MinBatchIntervalSeconds
	maxSeconds := services.MaxBatchIntervalSeconds
	if settings.CustomIntervalSecs > 0 && settings.CustomIntervalSecs < minSeconds {
		settings.CustomIntervalSecs = minSeconds
	}
	if settings.CustomIntervalSecs > maxSeconds {
		settings.CustomIntervalSecs = maxSeconds
	}
	if config.MetricStepSeconds <= 0 {
		config.MetricStepSeconds = services.RecommendedMetricStepSeconds(config.TimeRange)
	}
	if !config.Obfuscation.Enabled {
		config.Obfuscation = domain.ObfuscationConfig{}
	}
}

func recommendedStagingDir() string {
	homeDir, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if homeDir != "" {
			return filepath.Join(homeDir, "Library", "Application Support", "VMGather", "Staging")
		}
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "VMGather", "Staging")
		}
		if homeDir != "" {
			return filepath.Join(homeDir, "AppData", "Local", "VMGather", "Staging")
		}
	default:
		if homeDir != "" {
			return filepath.Join(homeDir, ".vmgather", "staging")
		}
	}
	return filepath.Join(os.TempDir(), "vmgather")
}

func ensureWritableDirectory(path string) error {
	testFile := filepath.Join(path, fmt.Sprintf(".vmgather-check-%d", time.Now().UnixNano()))
	file, err := os.Create(testFile)
	if err != nil {
		return err
	}
	_ = file.Close()
	return os.Remove(testFile)
}

func canCreateDirectory(path string) bool {
	dir := filepath.Clean(path)
	for {
		parent := filepath.Dir(dir)
		if parent == "" || parent == dir {
			break
		}
		info, err := os.Stat(parent)
		if err == nil && info.IsDir() {
			testDir := filepath.Join(parent, fmt.Sprintf(".vmgather-create-%d", time.Now().UnixNano()))
			if err := os.Mkdir(testDir, 0o755); err != nil {
				return false
			}
			_ = os.RemoveAll(testDir)
			return true
		}
		dir = parent
	}
	return false
}

func (s *Server) handleListDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	requested := r.URL.Query().Get("path")
	if requested == "" {
		requested = "/"
	}
	absPath, err := filepath.Abs(requested)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid path: %v", err))
		return
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			parent := filepath.Dir(absPath)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"path":    absPath,
				"parent":  parent,
				"entries": []interface{}{},
				"exists":  false,
			})
			return
		}
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to access directory: %v", err))
		return
	}

	if !info.IsDir() {
		respondWithError(w, http.StatusBadRequest, "Path is not a directory")
		return
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to list directory: %v", err))
		return
	}

	type dirEntry struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Writable bool   `json:"writable"`
	}

	result := []dirEntry{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childPath := filepath.Join(absPath, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mode := info.Mode()
		writable := mode&0o200 != 0
		result = append(result, dirEntry{
			Name:     entry.Name(),
			Path:     childPath,
			Writable: writable,
		})
	}

	parent := filepath.Dir(absPath)
	if absPath == "/" {
		parent = ""
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"path":    absPath,
		"parent":  parent,
		"entries": result,
		"exists":  true,
	})
}

func (s *Server) handleCheckDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Path   string `json:"path"`
		Ensure bool   `json:"ensure,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	if req.Path == "" {
		respondWithError(w, http.StatusBadRequest, "Path is required")
		return
	}
	absPath, err := filepath.Abs(req.Path)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid path: %v", err))
		return
	}
	absPath = filepath.Clean(absPath)

	info, err := os.Stat(absPath)
	if err != nil && !os.IsNotExist(err) {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to access directory: %v", err))
		return
	}

	dirExists := err == nil
	if dirExists && !info.IsDir() {
		respondWithError(w, http.StatusBadRequest, "Path is not a directory")
		return
	}

	if !dirExists {
		if !req.Ensure {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":         false,
				"abs_path":   absPath,
				"exists":     false,
				"can_create": canCreateDirectory(absPath),
				"message":    "Directory does not exist",
			})
			return
		}
		if err := os.MkdirAll(absPath, 0o755); err != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":         false,
				"abs_path":   absPath,
				"exists":     false,
				"can_create": false,
				"message":    fmt.Sprintf("Failed to create directory: %v", err),
			})
			return
		}
	}

	if err := ensureWritableDirectory(absPath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":         false,
			"abs_path":   absPath,
			"exists":     true,
			"can_create": false,
			"message":    fmt.Sprintf("Cannot write to directory: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"abs_path":   absPath,
		"exists":     true,
		"can_create": true,
	})
}

// getSampleDataFromResult retrieves sample data for preview
func (s *Server) getSampleDataFromResult(ctx context.Context, config domain.ExportConfig) ([]map[string]interface{}, error) {
	// Get sample metrics (limit to 5 for preview)
	samples, err := s.vmService.GetSample(ctx, config, 5)
	if err != nil {
		log.Printf("Failed to get sample data: %v", err)
		return nil, err
	}

	if config.Obfuscation.Enabled {
		samples = s.obfuscateSamples(samples, config.Obfuscation)
	}

	// Convert to response format
	sampleData := make([]map[string]interface{}, 0, len(samples))
	for _, sample := range samples {
		// Ensure metric name is never empty or undefined
		metricName := sample.MetricName
		if metricName == "" {
			// Fallback to __name__ label if MetricName is empty
			if labels := sample.Labels; labels != nil {
				if name, exists := labels["__name__"]; exists {
					metricName = name
				}
			}
			// Final fallback
			if metricName == "" {
				metricName = "unknown"
			}
		}

		sampleData = append(sampleData, map[string]interface{}{
			"name":   metricName,
			"labels": sample.Labels,
			"value":  sample.Value,
		})
	}

	return sampleData, nil
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

	// DEBUG: Log download request
	if s.debug {
		log.Printf("Archive Download:")
		log.Printf("  File Path: %s", filePath)
		log.Printf("  Client IP: %s", r.RemoteAddr)
	}

	// Security: ensure file is within output directory
	absOutputDir, err := filepath.Abs(s.outputDir)
	if err != nil {
		log.Printf("[ERROR] Failed to resolve output directory: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	// Resolve requested path to absolute path
	// Note: We treat relative paths as relative to CWD, which matches http.Dir(".").Open behavior
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		log.Printf("[ERROR] Failed to resolve file path: %v", err)
		respondWithError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	// Ensure the path is clean
	absFilePath = filepath.Clean(absFilePath)

	// Check if the file is inside the output directory
	// We append PathSeparator to ensure we don't match partial directory names (e.g. /tmp/exp vs /tmp/export)
	// We also allow the output directory itself (though downloading a dir usually fails or is not what we want)
	prefix := absOutputDir + string(os.PathSeparator)
	if !strings.HasPrefix(absFilePath, prefix) && absFilePath != absOutputDir {
		log.Printf("[WARN] Blocked path traversal attempt: %s (resolved: %s, allowed: %s)", filePath, absFilePath, absOutputDir)
		respondWithError(w, http.StatusForbidden, "Access denied: file must be in export directory")
		return
	}

	// Check if file exists
	info, err := os.Stat(absFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[ERROR] File not found: %s", absFilePath)
			respondWithError(w, http.StatusNotFound, fmt.Sprintf("File not found: %s", filePath))
			return
		}
		log.Printf("[ERROR] File access error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "File access error")
		return
	}

	if info.IsDir() {
		respondWithError(w, http.StatusBadRequest, "Cannot download a directory")
		return
	}

	// Set headers for download
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(absFilePath)+"\"")

	log.Printf("[OK] Serving file for download: %s", absFilePath)

	// Serve file
	http.ServeFile(w, r, absFilePath)
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
