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
}

const (
	jobStateQueued    = "queued"
	jobStateRunning   = "running"
	jobStateCompleted = "completed"
	jobStateFailed    = "failed"
)

type importSummary struct {
	MetricName    string              `json:"metric_name"`
	Labels        map[string]string   `json:"labels"`
	Start         time.Time           `json:"start"`
	End           time.Time           `json:"end"`
	Points        int                 `json:"points"`
	Bytes         int64               `json:"bytes"`
	SourceBytes   int64               `json:"source_bytes"`
	InflatedBytes int64               `json:"inflated_bytes"`
	Chunks        int                 `json:"chunks"`
	ChunkBytes    int                 `json:"chunk_bytes"`
	Examples      []map[string]string `json:"examples,omitempty"`

	rangePinned bool
}

type metricLine struct {
	Metric     map[string]string `json:"metric"`
	Values     []json.Number     `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

// Server handles VMImport UI and API endpoints.
type Server struct {
	version    string
	httpClient *http.Client
	jobs       map[string]*importJob
	jobsMu     sync.RWMutex
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

func (s *Server) withInsecure(insecure bool) *http.Client {
	if !insecure {
		return s.httpClient
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 - intentional for air-gapped envs
	return &http.Client{Timeout: importerHTTPTimeout, Transport: transport}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": s.version})
	})
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/check-endpoint", s.handleCheckEndpoint)
	mux.HandleFunc("/api/import/status", s.handleJobStatus)

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

	go s.runImportJob(context.Background(), job, cfg, tempPath, header.Filename, importURL, queryURL)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		JobID string    `json:"job_id"`
		Job   importJob `json:"job"`
	}{
		JobID: job.ID,
		Job:   snapshotJob(job),
	})
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

func prepareZipBundle(path string, uploadedBytes int64) (*bundleInfo, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open zip bundle: %w", err)
	}
	defer func() { _ = reader.Close() }()

	var metricsFile *zip.File
	var metadata *bundleMetadata

	for _, f := range reader.File {
		switch strings.ToLower(f.Name) {
		case "metrics.jsonl":
			metricsFile = f
		case "metadata.json":
			meta, err := parseMetadataFile(f)
			if err != nil {
				return nil, err
			}
			metadata = meta
		}
	}

	if metricsFile == nil {
		return nil, errors.New("bundle is missing metrics.jsonl")
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

func (s *Server) runImportJob(ctx context.Context, job *importJob, cfg uploadConfig, tempPath, originalName, importURL, queryURL string) {
	defer os.Remove(tempPath)

	s.updateJob(job, func(j *importJob) {
		j.State = jobStateRunning
		j.Stage = "extracting"
		j.Message = "Extracting bundle…"
		j.Percent = 2
	})

	bundle, err := prepareBundle(tempPath, originalName, job.SourceBytes)
	if err != nil {
		s.failJob(job, err)
		return
	}
	if bundle != nil && bundle.Cleanup != nil {
		defer bundle.Cleanup()
	}

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

	_, summary, err := s.streamImport(ctx, cfg, bundle, importURL, progress)
	if err != nil {
		s.failJob(job, err)
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

func (s *Server) streamImport(ctx context.Context, cfg uploadConfig, bundle *bundleInfo, importURL string, progress func(int)) (*uploadResult, importSummary, error) {
	summary := importSummary{
		Labels:      make(map[string]string),
		SourceBytes: bundle.OriginalBytes,
	}
	if bundle.ExtractedBytes > 0 {
		summary.InflatedBytes = bundle.ExtractedBytes
	}
	summary.ChunkBytes = maxImportChunkBytes
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
		if bundle.Metadata.MetricsCount > 0 {
			summary.Points = bundle.Metadata.MetricsCount
		}
	}

	file, err := os.Open(bundle.MetricsPath)
	if err != nil {
		return nil, summary, fmt.Errorf("failed to open metrics for import: %w", err)
	}
	defer func() { _ = file.Close() }()

	var lastStatus int
	var lastMessage string
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	var chunk bytes.Buffer

	flushChunk := func() error {
		if chunk.Len() == 0 {
			return nil
		}
		body := make([]byte, chunk.Len())
		copy(body, chunk.Bytes())
		status, message, err := s.postImportChunk(ctx, cfg, importURL, body)
		if err != nil {
			return err
		}
		summary.Bytes += int64(len(body))
		summary.Chunks++
		lastStatus = status
		lastMessage = message
		chunk.Reset()
		if progress != nil {
			progress(summary.Chunks)
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		var parsed metricLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			return nil, summary, fmt.Errorf("failed to parse metrics line: %w", err)
		}
		if err := summary.consumeMetric(parsed); err != nil {
			return nil, summary, err
		}
		normalized, err := normalizeMetricLine(parsed)
		if err != nil {
			return nil, summary, err
		}
		chunk.Write(normalized)
		chunk.WriteByte('\n')
		if chunk.Len() >= maxImportChunkBytes {
			if err := flushChunk(); err != nil {
				return nil, summary, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	if err := flushChunk(); err != nil {
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

func normalizeMetricLine(line metricLine) ([]byte, error) {
	values := make([]float64, len(line.Values))
	for i, val := range line.Values {
		floatVal, err := val.Float64()
		if err != nil {
			return nil, fmt.Errorf("invalid value %s: %w", val.String(), err)
		}
		values[i] = floatVal
	}
	payload := struct {
		Metric     map[string]string `json:"metric"`
		Values     []float64         `json:"values"`
		Timestamps []int64           `json:"timestamps"`
	}{
		Metric:     line.Metric,
		Values:     values,
		Timestamps: line.Timestamps,
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

	client := s.withInsecure(cfg.SkipTLSVerify)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, seriesURL+"?"+params.Encode(), nil)
	if err != nil {
		return &verificationResult{Verified: false, Message: err.Error(), Query: match}
	}
	applyTenantHeaders(req, cfg)
	applyAuthHeaders(req, cfg)

	client := s.withInsecure(cfg.SkipTLSVerify)
	resp, err := client.Do(req)
	if err != nil {
		return &verificationResult{Verified: false, Message: err.Error(), Query: match}
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return &verificationResult{
			Verified: false,
			Query:    match,
			Message:  fmt.Sprintf("query failed: %s %s", resp.Status, strings.TrimSpace(string(body))),
		}
	}

	var payload struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return &verificationResult{Verified: false, Query: match, Message: fmt.Sprintf("invalid verification payload: %v", err)}
	}

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
	client := s.withInsecure(cfg.SkipTLSVerify)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusInternalServerError {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("remote responded %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
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
