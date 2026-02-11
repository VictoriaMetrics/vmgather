package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const importerHTTPTimeout = 5 * time.Minute

var maxImportChunkBytes = 512 * 1024

//go:embed static/*
var staticFiles embed.FS

type uploadConfig struct {
	Endpoint          string `json:"endpoint"`
	TenantID          string `json:"tenant_id"`
	AuthType          string `json:"auth_type,omitempty"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	CustomHeaderName  string `json:"custom_header_name,omitempty"`
	CustomHeaderValue string `json:"custom_header_value,omitempty"`
	SkipTLSVerify     bool   `json:"skip_tls_verify"`
	MetricStepSeconds int    `json:"metric_step_seconds"`
	BatchWindowSecs   int    `json:"batch_window_seconds"`
	DropOld           bool   `json:"drop_old"`
	TimeShiftMs       int64  `json:"time_shift_ms"`
}

type uploadResult struct {
	BytesSent   int    `json:"bytes_sent"`
	RemotePath  string `json:"remote_path"`
	StatusCode  int    `json:"status_code"`
	Message     string `json:"message"`
	ContentType string `json:"content_type"`
}

type verificationResult struct {
	Verified   bool   `json:"verified"`
	Query      string `json:"query"`
	SeriesSeen int    `json:"series_seen"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Message    string `json:"message"`
}

type bundleInfo struct {
	MetricsPath    string
	Metadata       *bundleMetadata
	ContentType    string
	OriginalBytes  int64
	ExtractedBytes int64
	Cleanup        func()
}

type bundleMetadata struct {
	ExportID  string `json:"export_id"`
	TimeRange struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"time_range"`
	MetricsCount int      `json:"metrics_count"`
	Jobs         []string `json:"jobs"`
}

func (s *Server) newJob(uploadedBytes int64) *importJob {
	now := time.Now()
	return &importJob{
		ID:          fmt.Sprintf("job-%d", now.UnixNano()),
		State:       jobStateQueued,
		Stage:       "queued",
		Message:     "Waiting to start import…",
		Percent:     0,
		SourceBytes: uploadedBytes,
		ChunkSize:   maxImportChunkBytes,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func (s *Server) storeJob(job *importJob) {
	s.jobsMu.Lock()
	s.jobs[job.ID] = job
	s.jobsMu.Unlock()
}

func (s *Server) getJobSnapshot(id string) (importJob, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return importJob{}, false
	}
	return snapshotJob(job), true
}

func snapshotJob(job *importJob) importJob {
	cp := *job
	if job.Summary != nil {
		summary := *job.Summary
		cp.Summary = &summary
	}
	if job.Verification != nil {
		ver := *job.Verification
		cp.Verification = &ver
	}
	return cp
}

func (s *Server) updateJob(job *importJob, fn func(*importJob)) {
	s.jobsMu.Lock()
	fn(job)
	job.UpdatedAt = time.Now()
	s.jobsMu.Unlock()
}

func (s *Server) failJob(job *importJob, err error) {
	s.updateJob(job, func(j *importJob) {
		j.State = jobStateFailed
		j.Stage = "failed"
		j.Message = err.Error()
		j.Error = err.Error()
		j.Percent = 100
	})
}

type importJob struct {
	ID              string              `json:"id"`
	State           string              `json:"state"`
	Stage           string              `json:"stage"`
	Message         string              `json:"message"`
	Percent         float64             `json:"percent"`
	SourceBytes     int64               `json:"source_bytes"`
	InflatedBytes   int64               `json:"inflated_bytes"`
	ChunksCompleted int                 `json:"chunks_completed"`
	ChunksTotal     int                 `json:"chunks_total"`
	ChunkSize       int                 `json:"chunk_size"`
	Summary         *importSummary      `json:"summary,omitempty"`
	Verification    *verificationResult `json:"verification,omitempty"`
	RemotePath      string              `json:"remote_path,omitempty"`
	Error           string              `json:"error,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	ImportURL       string              `json:"import_url,omitempty"`
	QueryURL        string              `json:"query_url,omitempty"`
	BundlePath      string              `json:"bundle_path,omitempty"`
	ResumeOffset    int64               `json:"resume_offset,omitempty"`
	ResumeReady     bool                `json:"resume_ready,omitempty"`
	Config          uploadConfig        `json:"-"`
}

const (
	jobStateQueued    = "queued"
	jobStateRunning   = "running"
	jobStateCompleted = "completed"
	jobStateFailed    = "failed"
)

type importSummary struct {
	MetricName     string              `json:"metric_name"`
	Labels         map[string]string   `json:"labels"`
	Start          time.Time           `json:"start"`
	End            time.Time           `json:"end"`
	TotalPoints    int                 `json:"total_points,omitempty"`
	Points         int                 `json:"points"`
	Bytes          int64               `json:"bytes"`
	SourceBytes    int64               `json:"source_bytes"`
	InflatedBytes  int64               `json:"inflated_bytes"`
	Chunks         int                 `json:"chunks"`
	ChunkBytes     int                 `json:"chunk_bytes"`
	Examples       []map[string]string `json:"examples,omitempty"`
	SkippedLines   int                 `json:"skipped_lines,omitempty"`
	DroppedOld     int                 `json:"dropped_old,omitempty"`
	ProcessedBytes int64               `json:"processed_bytes,omitempty"`
	NormalizedTs   bool                `json:"normalized_ts,omitempty"`

	rangePinned bool
}

type metricLine struct {
	Metric     map[string]string `json:"metric"`
	Values     []json.RawMessage `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

// Server handles VMImport UI and API endpoints.
type Server struct {
	version             string
	httpClient          *http.Client
	jobs                map[string]*importJob
	jobsMu              sync.RWMutex
	insecureTLSWarnOnce sync.Once
}

func NewServer(version string) *Server {
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: false}}
	return &Server{
		version: version,
		httpClient: &http.Client{
			Timeout:   importerHTTPTimeout,
			Transport: transport,
		},
		jobs: make(map[string]*importJob),
	}
}

func (s *Server) withInsecure(insecure bool, endpoint string) *http.Client {
	if !insecure {
		return s.httpClient
	}
	s.insecureTLSWarnOnce.Do(func() {
		log.Printf("[WARN] vmimporter is using skip_tls_verify for endpoint %s. Use only in trusted lab/dev environments.", redactURLForLog(endpoint))
	})
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 - intentional for air-gapped envs
	return &http.Client{Timeout: importerHTTPTimeout, Transport: transport}
}

func redactURLForLog(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "unknown-endpoint"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid-url"
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	return u.String()
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": s.version})
	})
	mux.HandleFunc("/api/analyze", s.handleAnalyze)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/check-endpoint", s.handleCheckEndpoint)
	mux.HandleFunc("/api/import/status", s.handleJobStatus)
	mux.HandleFunc("/api/import/resume", s.handleResume)

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

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jobID := r.URL.Query().Get("id")
	if jobID == "" {
		respondWithError(w, http.StatusBadRequest, "missing job id")
		return
	}

	job, ok := s.getJobSnapshot(jobID)
	if !ok {
		respondWithError(w, http.StatusNotFound, "job not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jobID := r.URL.Query().Get("id")
	if jobID == "" {
		respondWithError(w, http.StatusBadRequest, "missing job id")
		return
	}

	s.jobsMu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.jobsMu.Unlock()
		respondWithError(w, http.StatusNotFound, "job not found")
		return
	}
	if !job.ResumeReady {
		s.jobsMu.Unlock()
		respondWithError(w, http.StatusBadRequest, "job is not resumable")
		return
	}
	cfg := job.Config
	tempPath := job.BundlePath
	importURL := job.ImportURL
	queryURL := job.QueryURL
	startOffset := job.ResumeOffset
	job.State = jobStateQueued
	job.Stage = "queued"
	job.Message = "Queued for resume…"
	job.Percent = 0
	job.Error = ""
	job.ResumeReady = false
	s.jobsMu.Unlock()

	go s.runImportJob(context.Background(), job, cfg, tempPath, filepath.Base(tempPath), importURL, queryURL, startOffset)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": jobID, "status": "resuming"})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
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

	tempPath, uploadedBytes, err := persistUploadedFile(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to persist bundle: %v", err))
		return
	}

	importURL, queryURL, err := resolveEndpoints(cfg)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	job := s.newJob(uploadedBytes)
	s.storeJob(job)

	// Snapshot the queued job before starting async execution to avoid races under -race.
	jobSnapshot := snapshotJob(job)
	go s.runImportJob(context.Background(), job, cfg, tempPath, header.Filename, importURL, queryURL, 0)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		JobID string    `json:"job_id"`
		Job   importJob `json:"job"`
	}{
		JobID: job.ID,
		Job:   jobSnapshot,
	})
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
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

	tempPath, uploadedBytes, err := persistUploadedFile(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to persist bundle: %v", err))
		return
	}
	defer func() { _ = os.Remove(tempPath) }()

	bundle, err := prepareBundle(tempPath, header.Filename, uploadedBytes)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("failed to prepare bundle: %v", err))
		return
	}
	if bundle.Cleanup != nil {
		defer bundle.Cleanup()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	retentionCutoff := s.retentionCutoff(ctx, cfg)
	if !cfg.DropOld {
		retentionCutoff = 0
	}
	if !cfg.DropOld {
		retentionCutoff = 0
	}

	summary, err := s.analyzeBundle(ctx, bundle, retentionCutoff, cfg.TimeShiftMs)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to analyze bundle: %v", err))
		return
	}

	payload := map[string]interface{}{
		"summary":          summary,
		"retention_cutoff": retentionCutoff,
		"warnings":         buildAnalysisWarnings(summary, retentionCutoff),
		"suggested_shift_ms": func() int64 {
			if retentionCutoff > 0 && !summary.Start.IsZero() && summary.Start.UnixMilli() < retentionCutoff {
				return retentionCutoff - summary.Start.UnixMilli() + int64(time.Hour/time.Millisecond)
			}
			return 0
		}(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func persistUploadedFile(src multipart.File) (string, int64, error) {
	tmp, err := os.CreateTemp("", "vmimport-upload-*")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tmp.Close() }()

	n, err := io.Copy(tmp, src)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", 0, err
	}
	return tmp.Name(), n, nil
}

func prepareBundle(path, originalName string, uploadedBytes int64) (*bundleInfo, error) {
	ext := strings.ToLower(filepath.Ext(originalName))
	switch ext {
	case ".zip":
		return prepareZipBundle(path, uploadedBytes)
	case ".jsonl", ".json":
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("failed to stat bundle: %w", err)
		}
		return &bundleInfo{
			MetricsPath:    path,
			ContentType:    "application/jsonl",
			OriginalBytes:  uploadedBytes,
			ExtractedBytes: info.Size(),
		}, nil
	default:
		// fall back to extension from path
		if strings.HasSuffix(strings.ToLower(path), ".zip") {
			return prepareZipBundle(path, uploadedBytes)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("failed to stat bundle: %w", err)
		}
		return &bundleInfo{
			MetricsPath:    path,
			ContentType:    "application/jsonl",
			OriginalBytes:  uploadedBytes,
			ExtractedBytes: info.Size(),
		}, nil
	}
}

func (s *Server) analyzeBundle(ctx context.Context, bundle *bundleInfo, retentionCutoffMs int64, shiftMs int64) (importSummary, error) {
	summary := importSummary{
		Labels:         make(map[string]string),
		SourceBytes:    bundle.OriginalBytes,
		InflatedBytes:  bundle.ExtractedBytes,
		ChunkBytes:     maxImportChunkBytes,
		ProcessedBytes: 0,
	}
	if summary.InflatedBytes == 0 && bundle.ExtractedBytes > 0 {
		summary.InflatedBytes = bundle.ExtractedBytes
	}

	file, err := os.Open(bundle.MetricsPath)
	if err != nil {
		return summary, fmt.Errorf("failed to open bundle: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	const maxLines = 1000
	linesScanned := 0

	for scanner.Scan() {
		linesScanned++
		if linesScanned > maxLines {
			break
		}
		line := scanner.Bytes()
		summary.ProcessedBytes += int64(len(line)) + 1

		var parsed metricLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			summary.SkippedLines++
			continue
		}
		values, err := normalizeValues(parsed.Values)
		if err != nil {
			summary.SkippedLines++
			continue
		}
		parsedTotal := len(parsed.Timestamps)
		summary.TotalPoints += parsedTotal
		filteredTs, filteredVals, dropped := filterTimestampsAndValues(parsed.Timestamps, values, retentionCutoffMs)
		if dropped > 0 {
			summary.DroppedOld += dropped
		}
		if len(filteredTs) == 0 || len(filteredTs) != len(filteredVals) {
			summary.SkippedLines++
			continue
		}
		if tsNormalized, scaled := normalizeTimestamps(filteredTs); scaled {
			filteredTs = tsNormalized
			summary.NormalizedTs = true
		}
		if shiftMs != 0 {
			for i := range filteredTs {
				filteredTs[i] += shiftMs
			}
		}
		if _, err := buildNormalizedLine(parsed.Metric, filteredVals, filteredTs); err != nil {
			summary.SkippedLines++
			continue
		}
		parsed.Timestamps = filteredTs
		parsed.Values = nil
		if err := summary.consumeMetric(metricLine{Metric: parsed.Metric, Timestamps: filteredTs}); err != nil {
			summary.SkippedLines++
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return summary, err
	}
	if summary.InflatedBytes == 0 {
		summary.InflatedBytes = summary.ProcessedBytes
	}
	return summary, nil
}

func buildAnalysisWarnings(summary importSummary, cutoffMs int64) []string {
	var warnings []string
	if cutoffMs > 0 && !summary.Start.IsZero() {
		cutoff := time.UnixMilli(cutoffMs)
		if summary.Start.Before(cutoff) {
			total := summary.Points + summary.DroppedOld
			percent := 0.0
			if total > 0 {
				percent = float64(summary.DroppedOld) / float64(total) * 100
			}
			shift := cutoff.Sub(summary.Start)
			warnings = append(warnings, fmt.Sprintf("Data starts at %s which is before retention cutoff %s. Older samples would drop (%.1f%% of points). Consider shifting timestamps forward by %s.", summary.Start.UTC().Format(time.RFC3339), cutoff.UTC().Format(time.RFC3339), percent, shift.Round(time.Second)))
		}
		if !summary.End.IsZero() {
			window := time.Since(cutoff)
			span := summary.End.Sub(summary.Start)
			if span > window {
				warnings = append(warnings, fmt.Sprintf("Bundle covers %s which exceeds current retention window (~%s). Tail data will be trimmed.", span.Round(time.Second), window.Round(time.Second)))
			}
		}
	}
	if summary.SkippedLines > 0 {
		warnings = append(warnings, fmt.Sprintf("Skipped %d invalid or empty lines.", summary.SkippedLines))
	}
	if summary.DroppedOld > 0 {
		warnings = append(warnings, fmt.Sprintf("Dropped %d samples outside retention window.", summary.DroppedOld))
	}
	if summary.NormalizedTs {
		warnings = append(warnings, "Timestamps were auto-scaled to milliseconds (detected non-ms input).")
	}
	return warnings
}

func prepareZipBundle(path string, uploadedBytes int64) (*bundleInfo, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open zip bundle: %w", err)
	}
	defer func() { _ = reader.Close() }()

	var metricsFile *zip.File
	var jsonlCandidates []*zip.File
	var metadata *bundleMetadata

	for _, f := range reader.File {
		nameLower := strings.ToLower(f.Name)
		switch nameLower {
		case "metrics.jsonl":
			metricsFile = f
		case "metadata.json":
			meta, err := parseMetadataFile(f)
			if err != nil {
				return nil, err
			}
			metadata = meta
		default:
			if strings.HasSuffix(nameLower, ".jsonl") {
				jsonlCandidates = append(jsonlCandidates, f)
			}
		}
	}

	if metricsFile == nil {
		var validationErr error
		for _, candidate := range jsonlCandidates {
			ok, err := isLikelyMetricsFile(candidate)
			if err != nil {
				validationErr = err
				continue
			}
			if ok {
				metricsFile = candidate
				break
			}
		}
		if metricsFile == nil {
			if validationErr != nil {
				return nil, validationErr
			}
			return nil, errors.New("bundle is missing metrics data (.jsonl)")
		}
	}

	tempMetrics, err := os.CreateTemp("", "vmimport-metrics-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("failed to prepare staging metrics file: %w", err)
	}

	source, err := metricsFile.Open()
	if err != nil {
		_ = tempMetrics.Close()
		_ = os.Remove(tempMetrics.Name())
		return nil, fmt.Errorf("failed to open metrics entry: %w", err)
	}

	if _, err := io.Copy(tempMetrics, source); err != nil {
		_ = source.Close()
		_ = tempMetrics.Close()
		_ = os.Remove(tempMetrics.Name())
		return nil, fmt.Errorf("failed to extract metrics.jsonl: %w", err)
	}
	_ = source.Close()
	_ = tempMetrics.Close()

	info, err := os.Stat(tempMetrics.Name())
	if err != nil {
		_ = os.Remove(tempMetrics.Name())
		return nil, fmt.Errorf("failed to stat extracted metrics: %w", err)
	}

	return &bundleInfo{
		MetricsPath:    tempMetrics.Name(),
		Metadata:       metadata,
		ContentType:    "application/jsonl",
		OriginalBytes:  uploadedBytes,
		ExtractedBytes: info.Size(),
		Cleanup: func() {
			_ = os.Remove(tempMetrics.Name())
		},
	}, nil
}

func estimateChunkCount(size int64) int {
	if size <= 0 {
		return 0
	}
	chunk := int64(maxImportChunkBytes)
	return int((size + chunk - 1) / chunk)
}

func (s *Server) runImportJob(ctx context.Context, job *importJob, cfg uploadConfig, tempPath, originalName, importURL, queryURL string, startOffset int64) {
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	s.updateJob(job, func(j *importJob) {
		j.State = jobStateRunning
		j.Stage = "extracting"
		j.Message = "Extracting bundle…"
		j.Percent = 2
		j.ImportURL = importURL
		j.QueryURL = queryURL
		j.BundlePath = tempPath
		j.ResumeOffset = startOffset
		j.ResumeReady = false
		j.Config = cfg
	})

	bundle, err := prepareBundle(tempPath, originalName, job.SourceBytes)
	if err != nil {
		s.failJob(job, err)
		return
	}
	cleanupBundle := true
	if bundle != nil && bundle.Cleanup != nil {
		defer func() {
			if cleanupBundle {
				bundle.Cleanup()
			}
		}()
	}
	s.updateJob(job, func(j *importJob) {
		j.BundlePath = bundle.MetricsPath
	})

	s.updateJob(job, func(j *importJob) {
		j.SourceBytes = bundle.OriginalBytes
		j.InflatedBytes = bundle.ExtractedBytes
		j.ChunksTotal = estimateChunkCount(bundle.ExtractedBytes)
		j.Stage = "importing"
		j.Message = "Streaming JSONL chunks…"
		j.Percent = 5
	})

	progress := func(done int) {
		s.updateJob(job, func(j *importJob) {
			j.ChunksCompleted = done
			if done > j.ChunksTotal {
				j.ChunksTotal = done
			}
			total := j.ChunksTotal
			if total > 0 {
				j.Percent = 5 + (float64(done)/float64(total))*80
				j.Message = fmt.Sprintf("Streaming chunk %d/%d…", done, total)
			} else {
				j.Message = fmt.Sprintf("Streaming chunk %d…", done)
			}
		})
	}

	retentionCutoff := s.retentionCutoff(ctx, cfg)

	_, summary, err := s.streamImport(ctx, cfg, bundle, importURL, startOffset, retentionCutoff, cfg.TimeShiftMs, progress)
	if err != nil {
		s.updateJob(job, func(j *importJob) {
			j.State = jobStateFailed
			j.Stage = "failed"
			j.Message = err.Error()
			j.Error = err.Error()
			j.Percent = 100
			j.ResumeOffset = summary.ProcessedBytes
			j.ResumeReady = true
			j.Summary = &summary
		})
		cleanupTemp = false
		cleanupBundle = false
		return
	}
	summary.SourceBytes = bundle.OriginalBytes
	summary.InflatedBytes = bundle.ExtractedBytes
	summary.ChunkBytes = maxImportChunkBytes

	s.updateJob(job, func(j *importJob) {
		j.Stage = "verifying"
		j.Message = "Verifying imported metrics…"
		j.Percent = math.Max(j.Percent, 92)
		j.RemotePath = importURL
	})

	verification := s.verifyImport(ctx, cfg, summary, queryURL)

	s.updateJob(job, func(j *importJob) {
		j.State = jobStateCompleted
		j.Stage = "completed"
		j.Message = "Import completed"
		j.Percent = 100
		j.Summary = &summary
		j.Verification = verification
		j.ResumeReady = false
		j.ResumeOffset = 0
		j.BundlePath = ""
	})
}

func parseMetadataFile(f *zip.File) (*bundleMetadata, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open metadata.json: %w", err)
	}
	defer func() { _ = rc.Close() }()

	var meta bundleMetadata
	if err := json.NewDecoder(rc).Decode(&meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata.json: %w", err)
	}
	return &meta, nil
}

func isLikelyMetricsFile(f *zip.File) (bool, error) {
	rc, err := f.Open()
	if err != nil {
		return false, fmt.Errorf("failed to open %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	scanner := bufio.NewScanner(rc)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	linesChecked := 0
	for scanner.Scan() && linesChecked < 20 {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		linesChecked++
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if _, ok := obj["metric"]; ok {
			return true, nil
		}
		if _, ok := obj["labels"]; ok {
			return true, nil
		}
		if _, ok := obj["__name__"]; ok {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("failed to scan %s: %w", f.Name, err)
	}
	return false, nil
}

func (s *Server) streamImport(ctx context.Context, cfg uploadConfig, bundle *bundleInfo, importURL string, startOffset, retentionCutoffMs, shiftMs int64, progress func(int)) (*uploadResult, importSummary, error) {
	summary := importSummary{
		Labels:         make(map[string]string),
		SourceBytes:    bundle.OriginalBytes,
		InflatedBytes:  bundle.ExtractedBytes,
		ChunkBytes:     maxImportChunkBytes,
		ProcessedBytes: startOffset,
	}
	if summary.InflatedBytes == 0 && bundle.ExtractedBytes > 0 {
		summary.InflatedBytes = bundle.ExtractedBytes
	}
	if bundle.Metadata != nil {
		if start, err := time.Parse(time.RFC3339, bundle.Metadata.TimeRange.Start); err == nil {
			summary.Start = start
		}
		if end, err := time.Parse(time.RFC3339, bundle.Metadata.TimeRange.End); err == nil {
			summary.End = end
		}
		if !summary.Start.IsZero() && !summary.End.IsZero() {
			summary.rangePinned = true
		}
	}

	file, err := os.Open(bundle.MetricsPath)
	if err != nil {
		return nil, summary, fmt.Errorf("failed to open metrics for import: %w", err)
	}
	defer func() { _ = file.Close() }()

	if startOffset > 0 {
		if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
			return nil, summary, fmt.Errorf("failed to seek for resume: %w", err)
		}
	}

	var (
		lastStatus      int
		lastMessage     string
		currentOffset   = startOffset
		committedOffset = startOffset

		chunk          bytes.Buffer
		chunkPoints    int
		chunkLabels    []map[string]string
		chunkMetric    string
		chunkMinTs     int64
		chunkMaxTs     int64
		chunkEndOffset int64
	)

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)

	commitChunk := func() error {
		if chunk.Len() == 0 {
			return nil
		}
		body := make([]byte, chunk.Len())
		copy(body, chunk.Bytes())
		status, message, err := s.postImportChunk(ctx, cfg, importURL, body)
		if err != nil {
			summary.ProcessedBytes = committedOffset
			return err
		}
		summary.Bytes += int64(len(body))
		summary.Points += chunkPoints
		summary.Chunks++
		lastStatus = status
		lastMessage = message
		committedOffset = chunkEndOffset
		summary.ProcessedBytes = committedOffset

		if chunkMinTs > 0 {
			t := time.UnixMilli(chunkMinTs)
			if summary.Start.IsZero() || t.Before(summary.Start) {
				summary.Start = t
			}
		}
		if chunkMaxTs > 0 {
			t := time.UnixMilli(chunkMaxTs)
			if summary.End.IsZero() || t.After(summary.End) {
				summary.End = t
			}
		}
		if summary.MetricName == "" && chunkMetric != "" {
			summary.MetricName = chunkMetric
		}
		if len(summary.Labels) == 0 && len(chunkLabels) > 0 {
			for k, v := range chunkLabels[0] {
				summary.Labels[k] = v
			}
		}
		for _, lbl := range chunkLabels {
			if len(summary.Examples) >= 3 {
				break
			}
			summary.Examples = append(summary.Examples, lbl)
		}

		chunk.Reset()
		chunkPoints = 0
		chunkLabels = chunkLabels[:0]
		chunkMetric = ""
		chunkMinTs = 0
		chunkMaxTs = 0
		chunkEndOffset = committedOffset
		if progress != nil {
			progress(summary.Chunks)
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		currentOffset += int64(len(line)) + 1 // account for newline

		var parsed metricLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			summary.SkippedLines++
			continue
		}

		parsed.Timestamps, _ = normalizeTimestamps(parsed.Timestamps)
		values, err := normalizeValues(parsed.Values)
		if err != nil {
			summary.SkippedLines++
			continue
		}
		filteredTs, filteredVals, dropped := filterTimestampsAndValues(parsed.Timestamps, values, retentionCutoffMs)
		if dropped > 0 {
			summary.DroppedOld += dropped
		}
		if len(filteredTs) == 0 || len(filteredTs) != len(filteredVals) {
			summary.SkippedLines++
			continue
		}

		if tsNormalized, scaled := normalizeTimestamps(filteredTs); scaled {
			filteredTs = tsNormalized
			summary.NormalizedTs = true
		}
		if shiftMs != 0 {
			for i := range filteredTs {
				filteredTs[i] += shiftMs
			}
		}

		normalized, err := buildNormalizedLine(parsed.Metric, filteredVals, filteredTs)
		if err != nil {
			summary.SkippedLines++
			continue
		}

		chunk.Write(normalized)
		chunk.WriteByte('\n')
		chunkPoints += len(filteredTs)
		if chunkMetric == "" && parsed.Metric != nil {
			chunkMetric = parsed.Metric["__name__"]
		}
		lbl := selectLabelSubset(parsed.Metric)
		if len(chunkLabels) < 5 { // keep a few to propagate to examples
			chunkLabels = append(chunkLabels, lbl)
		}
		if len(filteredTs) > 0 {
			if chunkMinTs == 0 || filteredTs[0] < chunkMinTs {
				chunkMinTs = filteredTs[0]
			}
			if filteredTs[len(filteredTs)-1] > chunkMaxTs {
				chunkMaxTs = filteredTs[len(filteredTs)-1]
			}
		}
		chunkEndOffset = currentOffset

		if chunk.Len() >= maxImportChunkBytes {
			if err := commitChunk(); err != nil {
				return nil, summary, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	if err := commitChunk(); err != nil {
		return nil, summary, err
	}

	if summary.InflatedBytes == 0 {
		summary.InflatedBytes = summary.Bytes
	}

	return &uploadResult{
		BytesSent:   int(summary.Bytes),
		RemotePath:  importURL,
		StatusCode:  lastStatus,
		Message:     lastMessage,
		ContentType: "application/jsonl",
	}, summary, nil
}

func normalizeValues(raw []json.RawMessage) ([]float64, error) {
	values := make([]float64, 0, len(raw))
	for _, v := range raw {
		// Try to decode as number first
		var num json.Number
		if err := json.Unmarshal(v, &num); err == nil {
			f, err := num.Float64()
			if err != nil {
				return nil, err
			}
			values = append(values, f)
			continue
		}

		// Strings that contain numbers ("1", "0.5", "NaN")
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, err
			}
			values = append(values, f)
			continue
		}

		// Booleans as 0/1
		var b bool
		if err := json.Unmarshal(v, &b); err == nil {
			if b {
				values = append(values, 1)
			} else {
				values = append(values, 0)
			}
			continue
		}

		return nil, fmt.Errorf("unsupported value %s", string(v))
	}
	return values, nil
}

func normalizeTimestamps(ts []int64) ([]int64, bool) {
	if len(ts) == 0 {
		return ts, false
	}
	median := ts[len(ts)/2]
	unit := detectTimestampUnit(median)
	switch unit {
	case "seconds":
		out := make([]int64, len(ts))
		for i, v := range ts {
			out[i] = v * 1000
		}
		return out, true
	case "microseconds":
		out := make([]int64, len(ts))
		for i, v := range ts {
			out[i] = v / 1000
		}
		return out, true
	case "nanoseconds":
		out := make([]int64, len(ts))
		for i, v := range ts {
			out[i] = v / 1_000_000
		}
		return out, true
	default:
		return ts, false
	}
}

func detectTimestampUnit(v int64) string {
	abs := v
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs < 1e9:
		return "milliseconds"
	case abs < 1e11:
		return "seconds"
	case abs < 1e14:
		return "milliseconds"
	case abs < 1e17:
		return "microseconds"
	default:
		return "nanoseconds"
	}
}

func filterTimestampsAndValues(timestamps []int64, values []float64, cutoffMs int64) ([]int64, []float64, int) {
	if cutoffMs <= 0 {
		return timestamps, values, 0
	}
	if len(timestamps) != len(values) {
		return nil, nil, len(timestamps)
	}
	keptTs := make([]int64, 0, len(timestamps))
	keptVals := make([]float64, 0, len(values))
	dropped := 0
	for i, ts := range timestamps {
		if ts < cutoffMs {
			dropped++
			continue
		}
		keptTs = append(keptTs, ts)
		keptVals = append(keptVals, values[i])
	}
	return keptTs, keptVals, dropped
}

func buildNormalizedLine(labels map[string]string, values []float64, timestamps []int64) ([]byte, error) {
	payload := struct {
		Metric     map[string]string `json:"metric"`
		Values     []float64         `json:"values"`
		Timestamps []int64           `json:"timestamps"`
	}{
		Metric:     labels,
		Values:     values,
		Timestamps: timestamps,
	}
	return json.Marshal(payload)
}

func (s *Server) postImportChunk(ctx context.Context, cfg uploadConfig, importURL string, body []byte) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, importURL, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("failed to build import request: %w", err)
	}
	req.Header.Set("Content-Type", "application/jsonl")
	applyTenantHeaders(req, cfg)
	applyAuthHeaders(req, cfg)

	client := s.withInsecure(cfg.SkipTLSVerify, importURL)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("remote import failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, "", fmt.Errorf("remote responded %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
	}
	return resp.StatusCode, strings.TrimSpace(string(bodyBytes)), nil
}

func (s *importSummary) consumeMetric(parsed metricLine) error {
	if parsed.Metric == nil {
		return errors.New("metrics line missing labels")
	}
	if s.MetricName == "" {
		if name, ok := parsed.Metric["__name__"]; ok {
			s.MetricName = name
		}
		s.Labels = selectLabelSubset(parsed.Metric)
	}

	if len(parsed.Timestamps) > 0 && !s.rangePinned {
		for _, ts := range parsed.Timestamps {
			t := time.UnixMilli(ts)
			if s.Start.IsZero() || t.Before(s.Start) {
				s.Start = t
			}
			if s.End.IsZero() || t.After(s.End) {
				s.End = t
			}
		}
	}
	s.Points += len(parsed.Timestamps)
	s.recordExample(parsed.Metric)
	return nil
}

func (s *importSummary) recordExample(labels map[string]string) {
	if len(s.Examples) >= 5 || labels == nil {
		return
	}
	example := make(map[string]string)
	keys := []string{"__name__", "job", "instance", "service", "namespace", "pod", "cluster"}
	for _, key := range keys {
		if val, ok := labels[key]; ok {
			example[key] = val
		}
	}
	if len(example) == 0 {
		count := 0
		for k, v := range labels {
			if strings.HasPrefix(k, "__") {
				continue
			}
			example[k] = v
			count++
			if count >= 4 {
				break
			}
		}
	}
	if len(example) > 0 {
		s.Examples = append(s.Examples, example)
	}
}

func selectLabelSubset(labels map[string]string) map[string]string {
	order := []string{"job", "instance", "service", "cluster", "namespace", "pod"}
	result := make(map[string]string)
	for _, key := range order {
		if val, ok := labels[key]; ok {
			result[key] = val
		}
	}
	if _, ok := result["instance"]; !ok {
		if val, ok := labels["instance"]; ok {
			result["instance"] = val
		}
	}
	if len(result) == 0 {
		count := 0
		for k, v := range labels {
			if strings.HasPrefix(k, "__") {
				continue
			}
			result[k] = v
			count++
			if count >= 3 {
				break
			}
		}
	}
	return result
}

func (s *Server) verifyImport(ctx context.Context, cfg uploadConfig, summary importSummary, queryURL string) *verificationResult {
	if summary.MetricName == "" || summary.Start.IsZero() || summary.End.IsZero() {
		return &verificationResult{
			Verified: false,
			Message:  "import completed but summary lacks metric/time range for verification",
		}
	}

	match := buildLabelMatcher(summary)
	start := summary.Start.Add(-1 * time.Minute).Unix()
	end := summary.End.Add(1 * time.Minute).Unix()

	seriesURL := queryURL
	if strings.Contains(seriesURL, "/api/v1/query") {
		seriesURL = strings.Replace(seriesURL, "/api/v1/query", "/api/v1/series", 1)
	} else {
		seriesURL = strings.TrimSuffix(seriesURL, "/") + "/api/v1/series"
	}

	params := url.Values{}
	params.Set("match[]", match)
	params.Set("start", fmt.Sprintf("%d", start))
	params.Set("end", fmt.Sprintf("%d", end))

	var lastErr string
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, seriesURL+"?"+params.Encode(), nil)
		if err != nil {
			return &verificationResult{Verified: false, Message: err.Error(), Query: match}
		}
		applyTenantHeaders(req, cfg)
		applyAuthHeaders(req, cfg)

		client := s.withInsecure(cfg.SkipTLSVerify, seriesURL)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				lastErr = fmt.Sprintf("query failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
			} else {
				var payload struct {
					Status string              `json:"status"`
					Data   []map[string]string `json:"data"`
				}
				if len(body) == 0 {
					lastErr = fmt.Sprintf("verification response is empty (HTTP %s)", resp.Status)
				} else if err := json.Unmarshal(body, &payload); err != nil {
					preview := string(body)
					if len(preview) > 200 {
						preview = preview[:200] + "…"
					}
					lastErr = fmt.Sprintf("invalid verification payload: %v; body=%q", err, preview)
				} else {
					verified := payload.Status == "success" && len(payload.Data) > 0
					message := fmt.Sprintf("%d matching series observed between %s and %s",
						len(payload.Data),
						summary.Start.Format(time.RFC3339),
						summary.End.Format(time.RFC3339),
					)
					return &verificationResult{
						Verified:   verified,
						Query:      match,
						SeriesSeen: len(payload.Data),
						Start:      summary.Start.Format(time.RFC3339),
						End:        summary.End.Format(time.RFC3339),
						Message:    message,
					}
				}
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
	return &verificationResult{Verified: false, Query: match, Message: lastErr}
}

func buildLabelMatcher(summary importSummary) string {
	var parts []string
	if summary.MetricName != "" {
		parts = append(parts, fmt.Sprintf(`__name__="%s"`, summary.MetricName))
	}
	keys := make([]string, 0, len(summary.Labels))
	for k := range summary.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		val := summary.Labels[key]
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, val))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func respondWithError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleCheckEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var cfg uploadConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if err := s.pingEndpoint(r.Context(), cfg); err != nil {
		respondWithError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) pingEndpoint(ctx context.Context, cfg uploadConfig) error {
	importURL, _, err := resolveEndpoints(cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, importURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	applyTenantHeaders(req, cfg)
	applyAuthHeaders(req, cfg)
	client := s.withInsecure(cfg.SkipTLSVerify, importURL)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("remote responded %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *Server) retentionCutoff(ctx context.Context, cfg uploadConfig) int64 {
	importURL, _, err := resolveEndpoints(cfg)
	if err != nil {
		log.Printf("[WARN] retentionCutoff: failed to resolve endpoints: %v", err)
		return 0
	}
	parsed, err := url.Parse(importURL)
	if err != nil {
		log.Printf("[WARN] retentionCutoff: failed to parse URL: %v", err)
		return 0
	}

	// Try /api/v1/status/tsdb first (might work for newer VMs or vmselect)
	parsed.Path = "/api/v1/status/tsdb"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err == nil {
		applyTenantHeaders(req, cfg)
		applyAuthHeaders(req, cfg)

		client := s.withInsecure(cfg.SkipTLSVerify, parsed.String())
		resp, err := client.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode < 300 {
				var payload struct {
					Data struct {
						RetentionTime string `json:"retentionTime"`
					} `json:"data"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil && payload.Data.RetentionTime != "" {
					dur, err := parseRetentionDuration(payload.Data.RetentionTime)
					if err == nil {
						cutoff := time.Now().Add(-dur).UnixMilli()
						log.Printf("[INFO] retentionCutoff from /api/v1/status/tsdb: %v (cutoff: %d)", dur, cutoff)
						return cutoff
					} else {
						log.Printf("[WARN] retentionCutoff: failed to parse %q: %v", payload.Data.RetentionTime, err)
					}
				}
			}
		}
	}

	// Fallback: parse /metrics for flag{name="retentionPeriod"}
	parsed.Path = "/metrics"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		log.Printf("[WARN] retentionCutoff: failed to create /metrics request: %v", err)
		return 0
	}
	applyTenantHeaders(req, cfg)
	applyAuthHeaders(req, cfg)

	client := s.withInsecure(cfg.SkipTLSVerify, parsed.String())
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[WARN] retentionCutoff: failed to fetch /metrics: %v", err)
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		log.Printf("[WARN] retentionCutoff: /metrics returned %d", resp.StatusCode)
		return 0
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// Look for: flag{name="retentionPeriod", value="400d", is_set="true"} 1
		if !strings.Contains(line, `name="retentionPeriod"`) {
			continue
		}
		// Extract value="..."
		start := strings.Index(line, `value="`)
		if start == -1 {
			continue
		}
		start += len(`value="`)
		end := strings.Index(line[start:], `"`)
		if end == -1 {
			continue
		}
		retentionStr := line[start : start+end]
		dur, err := parseRetentionDuration(retentionStr)
		if err != nil {
			log.Printf("[WARN] retentionCutoff: failed to parse duration %q: %v", retentionStr, err)
			continue
		}
		cutoff := time.Now().Add(-dur).UnixMilli()
		log.Printf("[INFO] retentionCutoff from /metrics: %v (cutoff: %d)", dur, cutoff)
		return cutoff
	}

	log.Printf("[WARN] retentionCutoff: no retention period found in /metrics")
	return 0
}

func parseRetentionDuration(raw string) (time.Duration, error) {
	// Supports suffixes y,w,d,h,m,s
	var total time.Duration
	var num strings.Builder
	flush := func(unit rune) error {
		if num.Len() == 0 {
			return fmt.Errorf("missing number before %c", unit)
		}
		val, err := strconv.Atoi(num.String())
		num.Reset()
		if err != nil || val < 0 {
			return fmt.Errorf("invalid number in %q", raw)
		}
		switch unit {
		case 'y':
			total += time.Duration(val) * 365 * 24 * time.Hour
		case 'w':
			total += time.Duration(val) * 7 * 24 * time.Hour
		case 'd':
			total += time.Duration(val) * 24 * time.Hour
		case 'h':
			total += time.Duration(val) * time.Hour
		case 'm':
			total += time.Duration(val) * time.Minute
		case 's':
			total += time.Duration(val) * time.Second
		default:
			return fmt.Errorf("unsupported unit %c", unit)
		}
		return nil
	}

	for _, ch := range raw {
		if ch >= '0' && ch <= '9' {
			num.WriteRune(ch)
			continue
		}
		if err := flush(ch); err != nil {
			return 0, err
		}
	}
	if num.Len() > 0 {
		return 0, fmt.Errorf("dangling number without unit in %q", raw)
	}
	if total <= 0 {
		return 0, fmt.Errorf("empty duration in %q", raw)
	}
	return total, nil
}

func resolveEndpoints(cfg uploadConfig) (string, string, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return "", "", fmt.Errorf("endpoint is required")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("invalid endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""

	importPath, queryPath := computePaths(strings.TrimRight(parsed.Path, "/"), cfg.TenantID)
	importURL := *parsed
	queryURL := *parsed
	importURL.Path = importPath
	queryURL.Path = queryPath

	return importURL.String(), queryURL.String(), nil
}

func computePaths(rawPath, tenant string) (string, string) {
	path := rawPath
	if path == "" {
		path = ""
	}

	switch {
	case strings.Contains(path, "/insert/"):
		importPath := path
		if !strings.HasSuffix(importPath, "/api/v1/import") {
			importPath += "/api/v1/import"
		}
		queryPath := strings.Replace(importPath, "/insert/", "/select/", 1)
		queryPath = strings.Replace(queryPath, "/api/v1/import", "/api/v1/query", 1)
		return importPath, queryPath
	case strings.Contains(path, "/select/"):
		queryPath := path
		if !strings.HasSuffix(queryPath, "/api/v1/query") {
			queryPath += "/api/v1/query"
		}
		importPath := strings.Replace(queryPath, "/select/", "/insert/", 1)
		importPath = strings.Replace(importPath, "/api/v1/query", "/api/v1/import", 1)
		return importPath, queryPath
	case tenant != "":
		importPath := fmt.Sprintf("/insert/%s/prometheus/api/v1/import", tenant)
		queryPath := fmt.Sprintf("/select/%s/prometheus/api/v1/query", tenant)
		return importPath, queryPath
	default:
		base := path
		if base == "" {
			return "/api/v1/import", "/api/v1/query"
		}
		return base + "/api/v1/import", base + "/api/v1/query"
	}
}

func applyTenantHeaders(req *http.Request, cfg uploadConfig) {
	if cfg.TenantID != "" {
		req.Header.Set("X-Vm-AccountID", cfg.TenantID)
		req.Header.Set("X-Vm-TenantID", cfg.TenantID)
	}
}

func applyAuthHeaders(req *http.Request, cfg uploadConfig) {
	switch strings.ToLower(cfg.AuthType) {
	case "bearer":
		if cfg.Password != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.Password)
		}
	case "header":
		if cfg.CustomHeaderName != "" {
			req.Header.Set(cfg.CustomHeaderName, cfg.CustomHeaderValue)
		}
	default:
		if cfg.Username != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
	}
}
