package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/httpguard"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

const importerHTTPTimeout = 5 * time.Minute

var maxImportChunkBytes = 512 * 1024

const (
	defaultAnalyzeSampleLines = 2000
	maxSimulationSeries       = 5000
	maxRecentProfiles         = 10
	recentProfilesFile        = "recent_profiles.json"
	recentProfilesDir         = "vmimporter"
	defaultAuthTypeNone       = "none"
)

var protectedDropLabels = []string{"__name__", "job", "instance"}

//go:embed static/*
var staticFiles embed.FS

type uploadConfig struct {
	Endpoint          string   `json:"endpoint"`
	TenantID          string   `json:"tenant_id"`
	AuthType          string   `json:"auth_type,omitempty"`
	Username          string   `json:"username"`
	Password          string   `json:"password"`
	CustomHeaderName  string   `json:"custom_header_name,omitempty"`
	CustomHeaderValue string   `json:"custom_header_value,omitempty"`
	SkipTLSVerify     bool     `json:"skip_tls_verify"`
	DropOld           bool     `json:"drop_old"`
	TimeShiftMs       int64    `json:"time_shift_ms"`
	MaxLabelsOverride int      `json:"max_labels_override,omitempty"`
	DropLabels        []string `json:"drop_labels,omitempty"`
}

type recentProfile struct {
	ID                string    `json:"id"`
	Endpoint          string    `json:"endpoint"`
	TenantID          string    `json:"tenant_id,omitempty"`
	AuthType          string    `json:"auth_type"`
	Username          string    `json:"username,omitempty"`
	CustomHeaderName  string    `json:"custom_header_name,omitempty"`
	SkipTLSVerify     bool      `json:"skip_tls_verify,omitempty"`
	DropOld           bool      `json:"drop_old"`
	TimeShiftMs       int64     `json:"time_shift_ms,omitempty"`
	MaxLabelsOverride int       `json:"max_labels_override,omitempty"`
	DropLabels        []string  `json:"drop_labels,omitempty"`
	LastUsedAt        time.Time `json:"last_used_at"`
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
	// MetricsPath is the uploaded file: plain JSONL, or a zip when ZipEntry is set.
	MetricsPath string
	// ZipEntry is the JSONL entry inside the zip. The zip is read in place and never extracted to disk:
	// a 9 MB bundle used to take 1 GB of temp space before the import even started.
	ZipEntry string
	// zipIndex is the position of ZipEntry in the zip directory, so a zip with duplicate names is read
	// from the same entry that was validated.
	zipIndex       int
	Metadata       *bundleMetadata
	ContentType    string
	OriginalBytes  int64
	ExtractedBytes int64
}

// open returns the metrics JSONL stream positioned at offset (bytes of the uncompressed stream).
func (b *bundleInfo) open(offset int64) (io.ReadCloser, error) {
	if b.ZipEntry == "" {
		f, err := os.Open(b.MetricsPath)
		if err != nil {
			return nil, err
		}
		if offset > 0 {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("failed to seek for resume: %w", err)
			}
		}
		return f, nil
	}
	zr, err := zip.OpenReader(b.MetricsPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open zip bundle: %w", err)
	}
	if b.zipIndex < len(zr.File) && zr.File[b.zipIndex].Name == b.ZipEntry {
		f := zr.File[b.zipIndex]
		rc, err := f.Open()
		if err != nil {
			_ = zr.Close()
			return nil, fmt.Errorf("failed to open %s: %w", b.ZipEntry, err)
		}
		if offset > 0 {
			// A deflate stream cannot seek: skip the already imported bytes.
			if _, err := io.CopyN(io.Discard, rc, offset); err != nil {
				_ = rc.Close()
				_ = zr.Close()
				return nil, fmt.Errorf("failed to skip to resume offset: %w", err)
			}
		}
		return &zipEntryReader{ReadCloser: rc, zip: zr}, nil
	}
	_ = zr.Close()
	return nil, fmt.Errorf("zip entry %q not found", b.ZipEntry)
}

// zipEntryReader closes the zip file together with the entry stream.
type zipEntryReader struct {
	io.ReadCloser
	zip *zip.ReadCloser
}

func (r *zipEntryReader) Close() error {
	err := r.ReadCloser.Close()
	if zerr := r.zip.Close(); err == nil {
		err = zerr
	}
	return err
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
	s.scheduleExpiry(job)
}

// jobRetention is how long a finished job stays visible. A failed job keeps its upload for resume
// during this time; after it the upload is deleted, so failed imports do not fill the temp dir.
var jobRetention = time.Hour

// scheduleExpiry removes a finished job (and the upload kept for its resume) after jobRetention.
func (s *Server) scheduleExpiry(job *importJob) {
	time.AfterFunc(jobRetention, func() { s.expireJob(job.ID) })
}

// Close removes the uploads that jobs still hold. Jobs live only in memory, so after a restart such
// uploads can never be resumed and would stay in the temp dir forever.
func (s *Server) Close() {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for _, job := range s.jobs {
		if job.BundlePath != "" {
			_ = os.Remove(job.BundlePath)
		}
	}
}

func (s *Server) expireJob(id string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job, ok := s.jobs[id]
	if !ok || (job.State != jobStateCompleted && job.State != jobStateFailed) {
		return // gone, or resumed and running again
	}
	if time.Since(job.UpdatedAt) < jobRetention {
		return // finished again later (resume); that run scheduled its own expiry
	}
	if job.ResumeReady && job.BundlePath != "" {
		_ = os.Remove(job.BundlePath)
	}
	delete(s.jobs, id)
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
	AnalyzedLines  int                 `json:"analyzed_lines,omitempty"`
	ScannedLines   int                 `json:"scanned_lines,omitempty"`
	SampleLimit    int                 `json:"sample_limit,omitempty"`
	SampleCut      bool                `json:"sample_cut,omitempty"`
	MaxLabelsSeen  int                 `json:"max_labels_seen,omitempty"`
	OverLabelLimit int                 `json:"over_label_limit,omitempty"`
	OverLimitPts   int                 `json:"over_limit_points,omitempty"`
	MaxLabelsLimit int                 `json:"max_labels_limit,omitempty"`
	TotalLabels    int                 `json:"total_labels,omitempty"`
	LabelStats     []labelStat         `json:"label_stats,omitempty"`
	LabelUniverse  []string            `json:"label_universe,omitempty"`
	LabelBitsets   []string            `json:"series_label_bitsets,omitempty"`
	LabelCounts    []int               `json:"series_label_counts,omitempty"`
	PointCounts    []int               `json:"series_point_counts,omitempty"`
	SimSeries      int                 `json:"simulation_series,omitempty"`
	SimSeriesCut   bool                `json:"simulation_series_capped,omitempty"`

	rangePinned bool
}

type labelStat struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
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
	insecureClientOnce  sync.Once
	insecureClient      *http.Client
	profilesPath        string
	profiles            []recentProfile
	profilesMu          sync.RWMutex
	// allowedHosts are extra Host names accepted from local clients and as the Origin of a plain HTTP
	// page behind a reverse proxy (see httpguard.LocalHostOnly and httpguard.SameOrigin).
	allowedHosts []string
}

// SetAllowedHosts sets the host names of a reverse proxy or sidecar: accepted as Host from local clients
// and as the Origin of plain HTTP pages (see httpguard.LocalHostOnly and httpguard.SameOrigin).
func (s *Server) SetAllowedHosts(hosts []string) {
	s.allowedHosts = hosts
}

func NewServer(version string) *Server {
	return newServer(version, defaultProfilesPath())
}

func newServer(version, profilesPath string) *Server {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	server := &Server{
		version: version,
		httpClient: &http.Client{
			Timeout:   importerHTTPTimeout,
			Transport: transport,
		},
		jobs:         make(map[string]*importJob),
		profilesPath: profilesPath,
		profiles:     make([]recentProfile, 0, maxRecentProfiles),
	}
	server.loadRecentProfiles()
	return server
}

func defaultProfilesPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		return filepath.Join(os.TempDir(), recentProfilesFile)
	}
	return filepath.Join(cacheDir, recentProfilesDir, recentProfilesFile)
}

func sanitizeEndpointForStorage(raw string) string {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return ""
	}
	candidate := endpoint
	addedScheme := false
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
		addedScheme = true
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return endpoint
	}
	parsed.User = nil
	sanitized := parsed.String()
	if addedScheme {
		return strings.TrimPrefix(sanitized, "http://")
	}
	return sanitized
}

func normalizeAuthType(authType string) string {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "basic", "bearer", "header":
		return strings.ToLower(strings.TrimSpace(authType))
	default:
		return defaultAuthTypeNone
	}
}

func isProtectedDropLabel(name string) bool {
	for _, candidate := range protectedDropLabels {
		if name == candidate {
			return true
		}
	}
	return false
}

func sanitizeDropLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(labels))
	for _, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" || isProtectedDropLabel(label) {
			continue
		}
		set[label] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	result := make([]string, 0, len(set))
	for label := range set {
		result = append(result, label)
	}
	sort.Strings(result)
	return result
}

func sanitizeMaxLabelsOverride(value int) int {
	if value <= 0 {
		return 0
	}
	return value
}

func dropLabelsSet(labels []string) map[string]struct{} {
	if len(labels) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(labels))
	for _, label := range sanitizeDropLabels(labels) {
		set[label] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func filterMetricLabels(metric map[string]string, dropSet map[string]struct{}) map[string]string {
	if len(metric) == 0 || len(dropSet) == 0 {
		return metric
	}
	filtered := make(map[string]string, len(metric))
	for key, value := range metric {
		if isProtectedDropLabel(key) {
			filtered[key] = value
			continue
		}
		if _, drop := dropSet[key]; drop {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func buildRecentProfile(cfg uploadConfig) (recentProfile, bool) {
	endpoint := sanitizeEndpointForStorage(cfg.Endpoint)
	if endpoint == "" {
		return recentProfile{}, false
	}
	profile := recentProfile{
		Endpoint:          endpoint,
		TenantID:          strings.TrimSpace(cfg.TenantID),
		AuthType:          normalizeAuthType(cfg.AuthType),
		SkipTLSVerify:     cfg.SkipTLSVerify,
		DropOld:           cfg.DropOld,
		TimeShiftMs:       cfg.TimeShiftMs,
		MaxLabelsOverride: sanitizeMaxLabelsOverride(cfg.MaxLabelsOverride),
		DropLabels:        sanitizeDropLabels(cfg.DropLabels),
		LastUsedAt:        time.Now().UTC(),
	}

	if profile.AuthType == "basic" {
		profile.Username = strings.TrimSpace(cfg.Username)
	}
	if profile.AuthType == "header" {
		profile.CustomHeaderName = strings.TrimSpace(cfg.CustomHeaderName)
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(profile.Endpoint)))
	_, _ = h.Write([]byte("|" + profile.TenantID))
	_, _ = h.Write([]byte("|" + profile.AuthType))
	_, _ = h.Write([]byte("|" + profile.Username))
	_, _ = h.Write([]byte("|" + profile.CustomHeaderName))
	_, _ = h.Write([]byte("|" + strconv.FormatBool(profile.SkipTLSVerify)))
	_, _ = h.Write([]byte("|" + strconv.FormatBool(profile.DropOld)))
	_, _ = h.Write([]byte("|" + strconv.FormatInt(profile.TimeShiftMs, 10)))
	_, _ = h.Write([]byte("|" + strconv.Itoa(profile.MaxLabelsOverride)))
	_, _ = h.Write([]byte("|" + strings.Join(profile.DropLabels, ",")))
	profile.ID = strconv.FormatUint(h.Sum64(), 16)
	return profile, true
}

func (s *Server) recentProfilesSnapshot() []recentProfile {
	s.profilesMu.RLock()
	defer s.profilesMu.RUnlock()
	snapshot := make([]recentProfile, len(s.profiles))
	copy(snapshot, s.profiles)
	return snapshot
}

func (s *Server) saveRecentProfile(cfg uploadConfig) {
	profile, ok := buildRecentProfile(cfg)
	if !ok {
		return
	}

	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()

	updated := make([]recentProfile, 0, maxRecentProfiles)
	updated = append(updated, profile)
	for _, existing := range s.profiles {
		if existing.ID == profile.ID {
			continue
		}
		updated = append(updated, existing)
		if len(updated) >= maxRecentProfiles {
			break
		}
	}
	s.profiles = updated

	if err := s.persistRecentProfilesLocked(); err != nil {
		log.Printf("[WARN] failed to persist recent profiles: %v", err)
	}
}

func (s *Server) loadRecentProfiles() {
	if strings.TrimSpace(s.profilesPath) == "" {
		return
	}
	raw, err := os.ReadFile(s.profilesPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("[WARN] failed to read recent profiles: %v", err)
		}
		return
	}
	var profiles []recentProfile
	if err := json.Unmarshal(raw, &profiles); err != nil {
		log.Printf("[WARN] failed to decode recent profiles: %v", err)
		return
	}
	for index := range profiles {
		profiles[index].Endpoint = sanitizeEndpointForStorage(profiles[index].Endpoint)
		profiles[index].AuthType = normalizeAuthType(profiles[index].AuthType)
		if profiles[index].AuthType != "basic" {
			profiles[index].Username = ""
		}
		if profiles[index].AuthType != "header" {
			profiles[index].CustomHeaderName = ""
		}
		profiles[index].DropLabels = sanitizeDropLabels(profiles[index].DropLabels)
		profiles[index].MaxLabelsOverride = sanitizeMaxLabelsOverride(profiles[index].MaxLabelsOverride)
		if profiles[index].ID == "" {
			cfg := uploadConfig{
				Endpoint:          profiles[index].Endpoint,
				TenantID:          profiles[index].TenantID,
				AuthType:          profiles[index].AuthType,
				Username:          profiles[index].Username,
				CustomHeaderName:  profiles[index].CustomHeaderName,
				SkipTLSVerify:     profiles[index].SkipTLSVerify,
				DropOld:           profiles[index].DropOld,
				TimeShiftMs:       profiles[index].TimeShiftMs,
				MaxLabelsOverride: profiles[index].MaxLabelsOverride,
				DropLabels:        profiles[index].DropLabels,
			}
			if rebuilt, ok := buildRecentProfile(cfg); ok {
				profiles[index].ID = rebuilt.ID
			}
		}
	}
	if len(profiles) > maxRecentProfiles {
		profiles = profiles[:maxRecentProfiles]
	}

	s.profilesMu.Lock()
	s.profiles = profiles
	s.profilesMu.Unlock()
}

func (s *Server) persistRecentProfilesLocked() error {
	if strings.TrimSpace(s.profilesPath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.profilesPath), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(s.profiles, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.profilesPath + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.profilesPath)
}

func (s *Server) withInsecure(insecure bool, endpoint string) *http.Client {
	if !insecure {
		return s.httpClient
	}
	s.insecureTLSWarnOnce.Do(func() {
		log.Printf("[WARN] vmimporter is using skip_tls_verify for endpoint %s. Use only in trusted lab/dev environments.", redactURLForLog(endpoint))
	})
	s.insecureClientOnce.Do(func() {
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}} // #nosec G402 - intentional for air-gapped envs
		s.insecureClient = &http.Client{Timeout: importerHTTPTimeout, Transport: transport}
	})
	return s.insecureClient
}

func redactURLForLog(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "unknown-endpoint"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid-url"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": s.version})
	})
	mux.HandleFunc("/api/profiles/recent", s.handleRecentProfiles)
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
	return httpguard.LocalHostOnly(httpguard.SameOrigin(mux, s.allowedHosts...), s.allowedHosts...)
}

func (s *Server) handleRecentProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"profiles": s.recentProfilesSnapshot(),
	})
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
	form, status, err := readUploadForm(r)
	if err != nil {
		respondWithError(w, status, err.Error())
		return
	}
	keepUpload := false
	defer func() {
		if !keepUpload {
			form.discard()
		}
	}()

	cfgRaw := form.Fields["config"]
	if cfgRaw == "" {
		respondWithError(w, http.StatusBadRequest, "missing config payload")
		return
	}
	var cfg uploadConfig
	if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid config: %v", err))
		return
	}
	cfg.DropLabels = sanitizeDropLabels(cfg.DropLabels)
	cfg.MaxLabelsOverride = sanitizeMaxLabelsOverride(cfg.MaxLabelsOverride)
	if form.Path == "" {
		respondWithError(w, http.StatusBadRequest, "bundle file is required")
		return
	}
	s.saveRecentProfile(cfg)

	importURL, queryURL, err := resolveEndpoints(cfg)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	job := s.newJob(form.Size)
	job.BundlePath = form.Path // owned by the job from now on, so Close removes it even before the job starts
	s.storeJob(job)

	// Snapshot the queued job before starting async execution to avoid races under -race.
	jobSnapshot := snapshotJob(job)
	keepUpload = true // runImportJob owns the upload from here
	go s.runImportJob(context.Background(), job, cfg, form.Path, form.FileName, importURL, queryURL, 0)

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
	form, status, err := readUploadForm(r)
	if err != nil {
		respondWithError(w, status, err.Error())
		return
	}
	defer form.discard()
	cfgRaw := form.Fields["config"]
	if cfgRaw == "" {
		respondWithError(w, http.StatusBadRequest, "missing config payload")
		return
	}
	var cfg uploadConfig
	if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid config: %v", err))
		return
	}
	cfg.DropLabels = sanitizeDropLabels(cfg.DropLabels)
	cfg.MaxLabelsOverride = sanitizeMaxLabelsOverride(cfg.MaxLabelsOverride)
	s.saveRecentProfile(cfg)

	fullCollection := parseBoolFormValue(form.Fields["full_collection"])
	sampleLimit := defaultAnalyzeSampleLines
	analysisMode := "sample"
	if fullCollection {
		sampleLimit = 0
		analysisMode = "full"
	}
	if form.Path == "" {
		respondWithError(w, http.StatusBadRequest, "bundle file is required")
		return
	}

	bundle, err := prepareBundle(form.Path, form.FileName, form.Size)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("failed to prepare bundle: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	retentionCutoff := s.effectiveRetentionCutoff(ctx, cfg)
	detectedMaxLabelsLimit, maxLabelsLimit, maxLabelsSource := s.resolveMaxLabelsLimit(ctx, cfg)

	summary, err := s.analyzeBundle(ctx, bundle, retentionCutoff, cfg.TimeShiftMs, maxLabelsLimit, cfg.DropLabels, sampleLimit)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to analyze bundle: %v", err))
		return
	}

	payload := map[string]interface{}{
		"summary":                   summary,
		"analysis_mode":             analysisMode,
		"retention_cutoff":          retentionCutoff,
		"max_labels_limit":          maxLabelsLimit,
		"max_labels_source":         maxLabelsSource,
		"detected_max_labels_limit": detectedMaxLabelsLimit,
		"protected_labels":          protectedDropLabels,
		"warnings":                  buildAnalysisWarnings(summary, retentionCutoff, maxLabelsLimit),
		"suggested_shift_ms": func() int64 {
			if retentionCutoff != 0 && !summary.Start.IsZero() && summary.Start.UnixMilli() < retentionCutoff {
				return retentionCutoff - summary.Start.UnixMilli() + int64(time.Hour/time.Millisecond)
			}
			return 0
		}(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// uploadForm is a parsed upload: small text fields and the bundle stored in a temp file the caller owns.
type uploadForm struct {
	Fields   map[string]string
	Path     string
	FileName string
	Size     int64
}

const (
	maxUploadFieldBytes = 1 << 20
	// maxUploadFormBytes and maxUploadFields cap all text fields together, so a form with many
	// uniquely named fields cannot fill the memory.
	maxUploadFormBytes = 4 << 20
	maxUploadFields    = 64
)

// readUploadForm streams a multipart upload: the bundle part goes straight to a temp file, other fields
// are read into memory (1 MB each, 4 MB and 64 fields in total at most). ParseMultipartForm(512 MB)
// kept the bundle in RAM and then copied it again: 1 GB peak RSS for a 419 MB bundle.
func readUploadForm(r *http.Request) (*uploadForm, int, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("failed to parse form: %w", err)
	}
	form := &uploadForm{Fields: make(map[string]string)}
	formBytes := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return form, 0, nil
		}
		if err != nil {
			form.discard()
			return nil, http.StatusBadRequest, fmt.Errorf("failed to parse form: %w", err)
		}
		if part.FormName() == "bundle" && part.FileName() != "" && form.Path == "" {
			path, n, err := persistUploadedFile(part)
			_ = part.Close()
			if err != nil {
				form.discard()
				return nil, http.StatusInternalServerError, fmt.Errorf("failed to persist bundle: %w", err)
			}
			form.Path, form.FileName, form.Size = path, part.FileName(), n
			continue
		}
		if len(form.Fields) >= maxUploadFields {
			_ = part.Close()
			form.discard()
			return nil, http.StatusBadRequest, fmt.Errorf("too many form fields (%d at most)", maxUploadFields)
		}
		limit := min(maxUploadFieldBytes, maxUploadFormBytes-formBytes)
		value, err := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		_ = part.Close()
		if err != nil || len(value) > limit {
			form.discard()
			return nil, http.StatusBadRequest, fmt.Errorf("invalid form field %q: too large or the form is too large", part.FormName())
		}
		formBytes += len(part.FormName()) + len(value)
		form.Fields[part.FormName()] = string(value)
	}
}

// discard removes the uploaded bundle.
func (f *uploadForm) discard() {
	if f.Path != "" {
		_ = os.Remove(f.Path)
		f.Path = ""
	}
}

func persistUploadedFile(src io.Reader) (string, int64, error) {
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
	isZip, err := isZipFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read bundle: %w", err)
	}
	if isZip || strings.EqualFold(filepath.Ext(originalName), ".zip") {
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

// isZipFile checks the zip signature. Uploads are stored under temp names without an extension.
func isZipFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false, nil // shorter than a zip header: not a zip
	}
	return string(magic[:]) == "PK\x03\x04", nil
}

func (s *Server) analyzeBundle(ctx context.Context, bundle *bundleInfo, retentionCutoffMs int64, shiftMs int64, maxLabelsLimit int, dropLabels []string, sampleLimit int) (importSummary, error) {
	summary := importSummary{
		Labels:         make(map[string]string),
		SourceBytes:    bundle.OriginalBytes,
		InflatedBytes:  bundle.ExtractedBytes,
		ChunkBytes:     maxImportChunkBytes,
		ProcessedBytes: 0,
		MaxLabelsLimit: maxLabelsLimit,
		SampleLimit:    sampleLimit,
	}
	dropSet := dropLabelsSet(dropLabels)
	labelCounts := make(map[string]int)
	seriesLabels := make([][]string, 0, 256)
	seriesLabelCounts := make([]int, 0, 256)
	seriesPointCounts := make([]int, 0, 256)
	if summary.InflatedBytes == 0 && bundle.ExtractedBytes > 0 {
		summary.InflatedBytes = bundle.ExtractedBytes
	}

	file, err := bundle.open(0)
	if err != nil {
		return summary, fmt.Errorf("failed to open bundle: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner, lineBytes := newMetricsLineScanner(file)
	linesScanned := 0

	for scanner.Scan() {
		if sampleLimit > 0 && linesScanned >= sampleLimit {
			summary.SampleCut = true
			break
		}
		linesScanned++
		line := scanner.Bytes()
		summary.ProcessedBytes += int64(*lineBytes)

		var parsed metricLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			summary.SkippedLines++
			continue
		}
		rawLabels := make([]string, 0, len(parsed.Metric))
		for label := range parsed.Metric {
			labelCounts[label]++
			rawLabels = append(rawLabels, label)
		}
		parsed.Metric = filterMetricLabels(parsed.Metric, dropSet)
		summary.AnalyzedLines++
		labelCount := len(parsed.Metric)
		if labelCount > summary.MaxLabelsSeen {
			summary.MaxLabelsSeen = labelCount
		}
		if maxLabelsLimit > 0 && labelCount > maxLabelsLimit {
			summary.OverLabelLimit++
			summary.OverLimitPts += len(parsed.Timestamps)
		}
		values, err := normalizeValues(parsed.Values)
		if err != nil {
			summary.SkippedLines++
			continue
		}
		parsedTotal := len(parsed.Timestamps)
		summary.TotalPoints += parsedTotal
		filteredTs, filteredVals, dropped, scaled := prepareSeriesSamples(parsed.Timestamps, values, retentionCutoffMs, shiftMs)
		summary.DroppedOld += dropped
		summary.NormalizedTs = summary.NormalizedTs || scaled
		if len(filteredTs) == 0 || len(filteredTs) != len(filteredVals) {
			summary.SkippedLines++
			continue
		}
		if _, err := buildNormalizedLine(parsed.Metric, filteredVals, filteredTs); err != nil {
			summary.SkippedLines++
			continue
		}
		if len(seriesLabels) < maxSimulationSeries {
			seriesLabels = append(seriesLabels, rawLabels)
			seriesLabelCounts = append(seriesLabelCounts, len(rawLabels))
			seriesPointCounts = append(seriesPointCounts, len(filteredTs))
		} else {
			summary.SimSeriesCut = true
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
	summary.ScannedLines = linesScanned
	if summary.InflatedBytes == 0 {
		summary.InflatedBytes = summary.ProcessedBytes
	}
	summary.TotalLabels = len(labelCounts)
	summary.LabelStats = buildLabelStats(labelCounts, 0)
	summary.LabelUniverse = labelNames(summary.LabelStats)
	summary.LabelBitsets = encodeSeriesLabelBitsets(seriesLabels, summary.LabelUniverse)
	summary.LabelCounts = seriesLabelCounts
	summary.PointCounts = seriesPointCounts
	summary.SimSeries = len(seriesLabelCounts)
	return summary, nil
}

func labelNames(stats []labelStat) []string {
	if len(stats) == 0 {
		return nil
	}
	names := make([]string, 0, len(stats))
	for _, item := range stats {
		if item.Name == "" {
			continue
		}
		names = append(names, item.Name)
	}
	return names
}

func encodeSeriesLabelBitsets(seriesLabels [][]string, universe []string) []string {
	if len(seriesLabels) == 0 || len(universe) == 0 {
		return nil
	}
	index := make(map[string]int, len(universe))
	for idx, name := range universe {
		index[name] = idx
	}
	width := (len(universe) + 7) / 8
	encoded := make([]string, 0, len(seriesLabels))
	for _, labels := range seriesLabels {
		bitset := make([]byte, width)
		for _, name := range labels {
			idx, ok := index[name]
			if !ok {
				continue
			}
			byteIdx := idx / 8
			bitIdx := idx % 8
			bitset[byteIdx] |= 1 << bitIdx
		}
		encoded = append(encoded, base64.RawStdEncoding.EncodeToString(bitset))
	}
	return encoded
}

func buildAnalysisWarnings(summary importSummary, cutoffMs int64, maxLabelsLimit int) []string {
	var warnings []string
	if cutoffMs != 0 && !summary.Start.IsZero() {
		cutoff := time.UnixMilli(cutoffMs)
		if summary.Start.Before(cutoff) {
			total := summary.Points + summary.DroppedOld
			percent := 0.0
			if total > 0 {
				percent = float64(summary.DroppedOld) / float64(total) * 100
			}
			shift := cutoff.Sub(summary.Start)
			warnings = append(warnings, fmt.Sprintf("%.1f%% of the samples are older than the target keeps and will not be imported. Move the data forward by %s to keep them.", percent, shift.Round(time.Minute)))
		}
		if !summary.End.IsZero() {
			window := time.Since(cutoff)
			span := summary.End.Sub(summary.Start)
			if span > window {
				warnings = append(warnings, fmt.Sprintf("The bundle covers %s, more than the target retention of %s: the oldest part will not be imported.", span.Round(time.Minute), window.Round(time.Minute)))
			}
		}
	}
	if summary.SkippedLines > 0 {
		warnings = append(warnings, fmt.Sprintf("Skipped %d invalid or empty lines.", summary.SkippedLines))
	}
	if summary.DroppedOld > 0 {
		warnings = append(warnings, fmt.Sprintf("%d samples are older than the target retention and will not be imported.", summary.DroppedOld))
	}
	if summary.NormalizedTs {
		warnings = append(warnings, "Timestamps were converted to milliseconds.")
	}
	if maxLabelsLimit > 0 && summary.OverLabelLimit > 0 {
		warnings = append(warnings, fmt.Sprintf("%d series have more labels than the target allows (%d); VictoriaMetrics drops them.", summary.OverLabelLimit, maxLabelsLimit))
	}
	if maxLabelsLimit == 0 && summary.MaxLabelsSeen > 40 {
		warnings = append(warnings, fmt.Sprintf("Series have up to %d labels, but the target label limit is unknown. Enter it above and check all lines.", summary.MaxLabelsSeen))
	}
	// A sampled check is the default mode, not a problem; the UI already says how many lines it read.
	return warnings
}

func buildLabelStats(labelCounts map[string]int, limit int) []labelStat {
	if len(labelCounts) == 0 {
		return nil
	}
	stats := make([]labelStat, 0, len(labelCounts))
	for name, count := range labelCounts {
		stats = append(stats, labelStat{Name: name, Count: count})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Count == stats[j].Count {
			return stats[i].Name < stats[j].Name
		}
		return stats[i].Count > stats[j].Count
	})
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}
	return stats
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
			if metricsFile == nil {
				metricsFile = f
			}
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

	zipIndex := 0
	for i, f := range reader.File {
		if f == metricsFile {
			zipIndex = i
			break
		}
	}
	return &bundleInfo{
		MetricsPath:    path,
		ZipEntry:       metricsFile.Name,
		zipIndex:       zipIndex,
		Metadata:       metadata,
		ContentType:    "application/jsonl",
		OriginalBytes:  uploadedBytes,
		ExtractedBytes: int64(metricsFile.UncompressedSize64),
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
		j.Stage = "preparing"
		j.Message = "Reading bundle…"
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

	retentionCutoff := s.effectiveRetentionCutoff(ctx, cfg)
	_, maxLabelsLimit, _ := s.resolveMaxLabelsLimit(ctx, cfg)

	_, summary, err := s.streamImport(ctx, cfg, bundle, importURL, startOffset, retentionCutoff, cfg.TimeShiftMs, maxLabelsLimit, progress)
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
		cleanupTemp = false // kept for resume; removed when the job expires
		s.scheduleExpiry(job)
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
	s.scheduleExpiry(job)
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

func (s *Server) streamImport(ctx context.Context, cfg uploadConfig, bundle *bundleInfo, importURL string, startOffset, retentionCutoffMs, shiftMs int64, maxLabelsLimit int, progress func(int)) (*uploadResult, importSummary, error) {
	summary := importSummary{
		Labels:         make(map[string]string),
		SourceBytes:    bundle.OriginalBytes,
		InflatedBytes:  bundle.ExtractedBytes,
		ChunkBytes:     maxImportChunkBytes,
		ProcessedBytes: startOffset,
		MaxLabelsLimit: maxLabelsLimit,
	}
	dropSet := dropLabelsSet(cfg.DropLabels)
	if summary.InflatedBytes == 0 && bundle.ExtractedBytes > 0 {
		summary.InflatedBytes = bundle.ExtractedBytes
	}
	if bundle.Metadata != nil {
		// The bundle's time range is pinned for verification; it must move with the time shift.
		shift := time.Duration(shiftMs) * time.Millisecond
		if start, err := time.Parse(time.RFC3339, bundle.Metadata.TimeRange.Start); err == nil {
			summary.Start = start.Add(shift)
		}
		if end, err := time.Parse(time.RFC3339, bundle.Metadata.TimeRange.End); err == nil {
			summary.End = end.Add(shift)
		}
		if !summary.Start.IsZero() && !summary.End.IsZero() {
			summary.rangePinned = true
		}
	}

	file, err := bundle.open(startOffset)
	if err != nil {
		return nil, summary, fmt.Errorf("failed to open metrics for import: %w", err)
	}
	defer func() { _ = file.Close() }()

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

	scanner, lineBytes := newMetricsLineScanner(file)

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
		currentOffset += int64(*lineBytes) // line plus its "\n" or "\r\n"

		var parsed metricLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			summary.SkippedLines++
			continue
		}
		parsed.Metric = filterMetricLabels(parsed.Metric, dropSet)
		summary.AnalyzedLines++
		labelCount := len(parsed.Metric)
		if labelCount > summary.MaxLabelsSeen {
			summary.MaxLabelsSeen = labelCount
		}
		if maxLabelsLimit > 0 && labelCount > maxLabelsLimit {
			summary.OverLabelLimit++
			summary.OverLimitPts += len(parsed.Timestamps)
		}

		values, err := normalizeValues(parsed.Values)
		if err != nil {
			summary.SkippedLines++
			continue
		}
		filteredTs, filteredVals, dropped, scaled := prepareSeriesSamples(parsed.Timestamps, values, retentionCutoffMs, shiftMs)
		summary.DroppedOld += dropped
		summary.NormalizedTs = summary.NormalizedTs || scaled
		if len(filteredTs) == 0 || len(filteredTs) != len(filteredVals) {
			summary.SkippedLines++
			continue
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

// normalizeValues validates sample values and returns them as JSON for /api/v1/import, without a
// float round trip for plain numbers. Numbers and null pass as-is (/api/v1/export writes NaN as null).
// Strings are parsed as floats, so "NaN", "Inf", "+Inf" and numeric strings become values that
// /api/v1/import accepts (see vm.AppendJSONFloat); "null" stays null. Booleans become 0/1.
func normalizeValues(raw []json.RawMessage) ([]json.RawMessage, error) {
	values := make([]json.RawMessage, 0, len(raw))
	for _, v := range raw {
		if len(v) == 0 {
			return nil, errors.New("empty value")
		}
		switch v[0] {
		case '"':
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return nil, err
			}
			if strings.EqualFold(s, "null") {
				values = append(values, jsonNull)
				continue
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("unsupported value %s", string(v))
			}
			values = append(values, vm.AppendJSONFloat(nil, f))
		case 't':
			values = append(values, jsonOne)
		case 'f':
			values = append(values, jsonZero)
		case '{', '[':
			return nil, fmt.Errorf("unsupported value %s", string(v))
		default:
			// A number or null: the line was already validated by json.Unmarshal.
			values = append(values, v)
		}
	}
	return values, nil
}

var (
	jsonNull = json.RawMessage("null")
	jsonOne  = json.RawMessage("1")
	jsonZero = json.RawMessage("0")
)

// prepareSeriesSamples normalizes timestamp units to milliseconds, applies the time shift and only then
// drops samples older than the retention cutoff, so "Shift to now" can move old samples into retention.
// Analyze and import use it, so both report the same numbers.
func prepareSeriesSamples(timestamps []int64, values []json.RawMessage, cutoffMs, shiftMs int64) ([]int64, []json.RawMessage, int, bool) {
	ts, scaled := normalizeTimestamps(timestamps)
	if shiftMs != 0 {
		for i := range ts {
			ts[i] += shiftMs
		}
	}
	keptTs, keptVals, dropped := filterTimestampsAndValues(ts, values, cutoffMs)
	return keptTs, keptVals, dropped, scaled
}

// newMetricsLineScanner returns a JSONL line scanner (lines up to 16 MB) and a pointer to the size of the
// last line read including its terminator ("\n" or "\r\n"), so resume offsets are exact byte positions.
func newMetricsLineScanner(r io.Reader) (*bufio.Scanner, *int) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	lineBytes := new(int)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		if token != nil {
			*lineBytes = advance
		}
		return advance, token, err
	})
	return scanner, lineBytes
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

// cutoffMs 0 means no filter. A cutoff before 1970 (-retentionPeriod=100y) is negative and still applies.
func filterTimestampsAndValues(timestamps []int64, values []json.RawMessage, cutoffMs int64) ([]int64, []json.RawMessage, int) {
	if cutoffMs == 0 {
		return timestamps, values, 0
	}
	if len(timestamps) != len(values) {
		return nil, nil, len(timestamps)
	}
	keptTs := make([]int64, 0, len(timestamps))
	keptVals := make([]json.RawMessage, 0, len(values))
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

// buildNormalizedLine encodes one series in the /api/v1/import JSON line format. Values are already
// valid JSON (see normalizeValues) and are copied as-is.
func buildNormalizedLine(labels map[string]string, values []json.RawMessage, timestamps []int64) ([]byte, error) {
	encodedLabels, err := json.Marshal(labels)
	if err != nil {
		return nil, err
	}
	size := len(encodedLabels) + 40 + 21*len(timestamps)
	for _, v := range values {
		size += len(v) + 1
	}
	line := make([]byte, 0, size)
	line = append(line, `{"metric":`...)
	line = append(line, encodedLabels...)
	line = append(line, `,"values":[`...)
	for i, v := range values {
		if i > 0 {
			line = append(line, ',')
		}
		line = append(line, v...)
	}
	line = append(line, `],"timestamps":[`...)
	for i, ts := range timestamps {
		if i > 0 {
			line = append(line, ',')
		}
		line = strconv.AppendInt(line, ts, 10)
	}
	return append(line, "]}"...), nil
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

// verifySeriesLimit asks /api/v1/series for a bounded answer (VictoriaMetrics supports "limit"); one
// matching series is enough to verify. verifyMaxBodyBytes covers backends that ignore the limit: a single
// histogram metric can return megabytes of series.
const (
	verifySeriesLimit  = 100
	verifyMaxBodyBytes = 8 << 20
)

// verifyAttempts and verifyRetryDelay give VictoriaMetrics time to make just imported series
// searchable: on v1.155.0 a new series showed up in /api/v1/series 3-11 seconds after the import.
var (
	verifyAttempts   = 30
	verifyRetryDelay = time.Second
)

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
	params.Set("limit", strconv.Itoa(verifySeriesLimit))

	var lastErr string
	var lastResult *verificationResult
	for attempt := 0; attempt < verifyAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return &verificationResult{Verified: false, Query: match, Message: ctx.Err().Error()}
			case <-time.After(verifyRetryDelay):
			}
		}
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
			body, _ := io.ReadAll(io.LimitReader(resp.Body, verifyMaxBodyBytes))
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				lastErr = fmt.Sprintf("query failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
				if resp.StatusCode < 500 {
					// 4xx (no /api/v1/series on the target, bad auth, bad matcher): retrying cannot help.
					return &verificationResult{Verified: false, Query: match, Message: lastErr}
				}
			} else {
				var payload struct {
					Status string              `json:"status"`
					Data   []map[string]string `json:"data"`
				}
				if len(body) == 0 {
					return &verificationResult{Verified: false, Query: match, Message: fmt.Sprintf("verification response is empty (HTTP %s)", resp.Status)}
				} else if err := json.Unmarshal(body, &payload); err != nil {
					preview := string(body)
					if len(preview) > 200 {
						preview = preview[:200] + "…"
					}
					return &verificationResult{Verified: false, Query: match, Message: fmt.Sprintf("invalid verification payload: %v; body=%q", err, preview)}
				} else {
					verified := payload.Status == "success" && len(payload.Data) > 0
					seen := fmt.Sprintf("%d", len(payload.Data))
					if len(payload.Data) >= verifySeriesLimit {
						seen = "at least " + seen
					}
					message := fmt.Sprintf("%s matching series observed between %s and %s",
						seen,
						summary.Start.Format(time.RFC3339),
						summary.End.Format(time.RFC3339),
					)
					lastResult = &verificationResult{
						Verified:   verified,
						Query:      match,
						SeriesSeen: len(payload.Data),
						Start:      summary.Start.Format(time.RFC3339),
						End:        summary.End.Format(time.RFC3339),
						Message:    message,
					}
					// Freshly imported data becomes searchable a moment later: retry only an empty answer
					// (plus network errors and 5xx above).
					if verified {
						return lastResult
					}
				}
			}
		}
	}
	if lastResult != nil {
		return lastResult
	}
	return &verificationResult{Verified: false, Query: match, Message: lastErr}
}

// quoteLabelValue quotes a label value for a series selector, escaping backslashes, quotes and newlines.
func quoteLabelValue(v string) string {
	return `"` + labelValueEscaper.Replace(v) + `"`
}

var labelValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

func buildLabelMatcher(summary importSummary) string {
	var parts []string
	if summary.MetricName != "" {
		parts = append(parts, "__name__="+quoteLabelValue(summary.MetricName))
	}
	keys := make([]string, 0, len(summary.Labels))
	for k := range summary.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+quoteLabelValue(summary.Labels[key]))
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
	s.saveRecentProfile(cfg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func parseBoolFormValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

// effectiveRetentionCutoff is the cutoff that analyze and import apply: 0 when the user turned off
// dropping samples older than the target retention (drop_old=false).
func (s *Server) effectiveRetentionCutoff(ctx context.Context, cfg uploadConfig) int64 {
	if !cfg.DropOld {
		return 0
	}
	return s.retentionCutoff(ctx, cfg)
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
		retentionStr, ok := readFlagValue(line, "retentionPeriod")
		if !ok {
			continue
		}
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

func (s *Server) maxLabelsPerTimeseries(ctx context.Context, cfg uploadConfig) int {
	importURL, _, err := resolveEndpoints(cfg)
	if err != nil {
		log.Printf("[WARN] maxLabelsPerTimeseries: failed to resolve endpoints: %v", err)
		return 0
	}
	parsed, err := url.Parse(importURL)
	if err != nil {
		log.Printf("[WARN] maxLabelsPerTimeseries: failed to parse URL: %v", err)
		return 0
	}

	parsed.Path = "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		log.Printf("[WARN] maxLabelsPerTimeseries: failed to create request: %v", err)
		return 0
	}
	applyTenantHeaders(req, cfg)
	applyAuthHeaders(req, cfg)

	client := s.withInsecure(cfg.SkipTLSVerify, parsed.String())
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[WARN] maxLabelsPerTimeseries: failed to fetch /metrics: %v", err)
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		log.Printf("[WARN] maxLabelsPerTimeseries: /metrics returned %d", resp.StatusCode)
		return 0
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		raw, ok := readFlagValue(line, "maxLabelsPerTimeseries")
		if !ok {
			continue
		}
		limit, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || limit <= 0 {
			continue
		}
		return limit
	}
	return 0
}

func (s *Server) resolveMaxLabelsLimit(ctx context.Context, cfg uploadConfig) (detected int, effective int, source string) {
	detected = s.maxLabelsPerTimeseries(ctx, cfg)
	effective = detected
	source = "metrics"
	if detected <= 0 {
		source = "unknown"
	}
	if cfg.MaxLabelsOverride > 0 {
		effective = cfg.MaxLabelsOverride
		source = "manual"
	}
	return detected, effective, source
}

func readFlagValue(metricsLine, flagName string) (string, bool) {
	if !strings.Contains(metricsLine, `flag{`) || !strings.Contains(metricsLine, `name="`+flagName+`"`) {
		return "", false
	}
	start := strings.Index(metricsLine, `value="`)
	if start == -1 {
		return "", false
	}
	start += len(`value="`)
	end := strings.Index(metricsLine[start:], `"`)
	if end == -1 {
		return "", false
	}
	return metricsLine[start : start+end], true
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
