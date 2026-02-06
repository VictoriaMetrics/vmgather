package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

type ExportJobState string

const (
	JobPending   ExportJobState = "pending"
	JobRunning   ExportJobState = "running"
	JobCompleted ExportJobState = "completed"
	JobFailed    ExportJobState = "failed"
	JobCanceled  ExportJobState = "canceled"
)

const (
	defaultMaxConcurrentJobs = 3
	defaultJobRetention      = 30 * time.Minute
)

type ExportJobStatus struct {
	ID                       string               `json:"job_id"`
	State                    ExportJobState       `json:"state"`
	CreatedAt                time.Time            `json:"created_at"`
	StartedAt                *time.Time           `json:"started_at,omitempty"`
	CompletedAt              *time.Time           `json:"completed_at,omitempty"`
	TotalBatches             int                  `json:"total_batches"`
	CompletedBatches         int                  `json:"completed_batches"`
	Progress                 float64              `json:"progress"`
	MetricsProcessed         int                  `json:"metrics_processed"`
	BatchWindowSeconds       int                  `json:"batch_window_seconds"`
	AverageBatchSeconds      float64              `json:"average_batch_seconds"`
	LastBatchDurationSeconds float64              `json:"last_batch_duration_seconds"`
	ETA                      *time.Time           `json:"eta,omitempty"`
	StagingPath              string               `json:"staging_path,omitempty"`
	ObfuscationEnabled       bool                 `json:"obfuscation_enabled"`
	Result                   *domain.ExportResult `json:"result,omitempty"`
	Error                    string               `json:"error,omitempty"`
	CurrentRange             *domain.TimeRange    `json:"current_range,omitempty"`
}

func (s *ExportJobStatus) clone() *ExportJobStatus {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}

type exportJob struct {
	status        *ExportJobStatus
	durationTotal time.Duration
	cancel        context.CancelFunc
	config        domain.ExportConfig
	resumeFrom    int
	baseMetrics   int
}

type ExportJobManager struct {
	exportService     services.ExportService
	mu                sync.RWMutex
	jobs              map[string]*exportJob
	maxConcurrentJobs int
	retention         time.Duration
	activeJobs        int
}

func NewExportJobManager(service services.ExportService) *ExportJobManager {
	return &ExportJobManager{
		exportService:     service,
		jobs:              make(map[string]*exportJob),
		maxConcurrentJobs: defaultMaxConcurrentJobs,
		retention:         defaultJobRetention,
	}
}

func (m *ExportJobManager) StartJob(ctx context.Context, jobID string, config domain.ExportConfig) (*ExportJobStatus, error) {
	if jobID == "" {
		jobID = fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	windows := services.CalculateBatchWindows(config.TimeRange, config.Batching)
	total := len(windows)
	if total == 0 {
		total = 1
	}
	batchWindowSeconds := 0
	if len(windows) > 0 {
		batchWindowSeconds = int(windows[0].End.Sub(windows[0].Start).Seconds())
	}

	status := &ExportJobStatus{
		ID:                 jobID,
		State:              JobPending,
		CreatedAt:          time.Now(),
		TotalBatches:       total,
		BatchWindowSeconds: batchWindowSeconds,
		StagingPath:        config.StagingFile,
		ObfuscationEnabled: config.Obfuscation.Enabled,
	}

	jobCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	job := &exportJob{status: status, cancel: cancel, config: config}

	m.mu.Lock()
	m.cleanupLocked(time.Now())
	if m.activeJobs >= m.maxConcurrentJobs {
		m.mu.Unlock()
		return nil, fmt.Errorf("maximum concurrent exports reached (%d)", m.maxConcurrentJobs)
	}
	m.jobs[jobID] = job
	m.activeJobs++
	statusSnapshot := status.clone()
	m.mu.Unlock()

	go m.runJob(jobCtx, jobID, config)

	return statusSnapshot, nil
}

func (m *ExportJobManager) GetStatus(jobID string) (*ExportJobStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, exists := m.jobs[jobID]
	if !exists {
		return nil, false
	}
	return job.status.clone(), true
}

func (m *ExportJobManager) ResumeJob(ctx context.Context, jobID string) (*ExportJobStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, exists := m.jobs[jobID]
	if !exists {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	if job.status.State != JobCanceled && job.status.State != JobFailed {
		return nil, fmt.Errorf("job %s is not resumable", jobID)
	}

	resumeFrom := job.status.CompletedBatches
	baseMetrics := job.status.MetricsProcessed
	cfg := job.config
	cfg.ResumeFromBatch = resumeFrom
	if job.status.StagingPath != "" {
		cfg.StagingFile = job.status.StagingPath
	}
	if m.activeJobs >= m.maxConcurrentJobs {
		return nil, fmt.Errorf("maximum concurrent exports reached (%d)", m.maxConcurrentJobs)
	}
	jobCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	job.cancel = cancel
	job.config = cfg
	job.resumeFrom = resumeFrom
	job.baseMetrics = baseMetrics
	job.durationTotal = 0
	job.status.StagingPath = cfg.StagingFile

	job.status.State = JobPending
	job.status.Error = ""
	job.status.Result = nil
	job.status.CompletedAt = nil
	job.status.ETA = nil

	m.activeJobs++

	go m.runJob(jobCtx, jobID, cfg)
	return job.status.clone(), nil
}

func (m *ExportJobManager) runJob(ctx context.Context, jobID string, config domain.ExportConfig) {
	baseBatches := 0
	baseMetrics := 0
	m.mu.RLock()
	job, ok := m.jobs[jobID]
	m.mu.RUnlock()
	if ok {
		baseBatches = job.resumeFrom
		baseMetrics = job.baseMetrics
	}
	if baseBatches > 0 {
		config.ResumeFromBatch = baseBatches
	}
	reporter := &jobProgressReporter{manager: m, jobID: jobID, baseBatches: baseBatches, baseMetrics: baseMetrics}
	ctx = services.WithProgressReporter(ctx, reporter)

	m.markRunning(jobID)

	result, err := m.exportService.ExecuteExport(ctx, config)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.markCanceled(jobID, err)
		} else {
			m.markFailed(jobID, err)
		}
		return
	}

	m.markCompleted(jobID, result)
}

func (m *ExportJobManager) markRunning(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, exists := m.jobs[jobID]; exists {
		now := time.Now()
		job.status.State = JobRunning
		job.status.StartedAt = &now
	}
}

func (m *ExportJobManager) markFailed(jobID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, exists := m.jobs[jobID]; exists {
		if job.cancel != nil {
			job.cancel()
			job.cancel = nil
		}
		now := time.Now()
		job.status.State = JobFailed
		job.status.CompletedAt = &now
		job.status.Error = err.Error()
		job.status.CurrentRange = nil
		m.jobFinishedLocked()
	}
}

func (m *ExportJobManager) markCompleted(jobID string, result *domain.ExportResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, exists := m.jobs[jobID]; exists {
		if job.cancel != nil {
			job.cancel()
			job.cancel = nil
		}
		now := time.Now()
		job.status.State = JobCompleted
		job.status.CompletedAt = &now
		job.status.Progress = 1.0
		job.status.Result = result
		job.status.CurrentRange = nil
		job.status.ETA = nil
		m.jobFinishedLocked()
	}
}

func (m *ExportJobManager) updateBatch(jobID string, progress services.BatchProgress, baseBatches int, baseMetrics int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, exists := m.jobs[jobID]
	if !exists {
		return
	}

	if progress.TotalBatches > 0 {
		job.status.TotalBatches = progress.TotalBatches
	}

	if progress.BatchIndex > job.status.CompletedBatches {
		job.status.CompletedBatches = progress.BatchIndex
	}
	if job.status.TotalBatches > 0 {
		p := float64(job.status.CompletedBatches) / float64(job.status.TotalBatches)
		if p > 1.0 {
			p = 1.0
		}
		job.status.Progress = p
	}

	if baseMetrics > 0 && job.status.MetricsProcessed < baseMetrics {
		job.status.MetricsProcessed = baseMetrics
	}
	job.status.MetricsProcessed += progress.Metrics
	job.status.LastBatchDurationSeconds = progress.Duration.Seconds()
	job.durationTotal += progress.Duration

	if job.status.CompletedBatches > 0 {
		observedBatches := job.status.CompletedBatches - baseBatches
		if observedBatches <= 0 {
			observedBatches = job.status.CompletedBatches
		}
		avg := job.durationTotal.Seconds() / float64(observedBatches)
		job.status.AverageBatchSeconds = avg

		remaining := job.status.TotalBatches - job.status.CompletedBatches
		if remaining > 0 && avg > 0 {
			eta := time.Now().Add(time.Duration(avg*float64(remaining)) * time.Second)
			job.status.ETA = &eta
		} else {
			job.status.ETA = nil
		}
	}

	job.status.CurrentRange = &domain.TimeRange{
		Start: progress.TimeRange.Start,
		End:   progress.TimeRange.End,
	}
}

func (m *ExportJobManager) jobFinishedLocked() {
	if m.activeJobs > 0 {
		m.activeJobs--
	}
	m.cleanupLocked(time.Now())
}

func (m *ExportJobManager) markCanceled(jobID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, exists := m.jobs[jobID]; exists {
		if job.cancel != nil {
			job.cancel()
			job.cancel = nil
		}
		now := time.Now()
		job.status.State = JobCanceled
		job.status.CompletedAt = &now
		if err != nil {
			job.status.Error = err.Error()
		} else {
			job.status.Error = "canceled"
		}
		job.status.ETA = nil
		job.status.CurrentRange = nil
		m.jobFinishedLocked()
	}
}

func (m *ExportJobManager) CancelJob(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, exists := m.jobs[jobID]
	if !exists {
		return fmt.Errorf("job %s not found", jobID)
	}
	if job.status.State == JobCompleted || job.status.State == JobFailed || job.status.State == JobCanceled {
		return fmt.Errorf("job %s already finished", jobID)
	}
	if job.cancel != nil {
		job.cancel()
	}
	return nil
}

func (m *ExportJobManager) cleanupLocked(now time.Time) {
	for id, job := range m.jobs {
		if job.status.State == JobCompleted || job.status.State == JobFailed || job.status.State == JobCanceled {
			if job.status.CompletedAt != nil && now.Sub(*job.status.CompletedAt) > m.retention {
				delete(m.jobs, id)
			}
		}
	}
}

type jobProgressReporter struct {
	manager     *ExportJobManager
	jobID       string
	baseBatches int
	baseMetrics int
}

func (r *jobProgressReporter) OnBatchComplete(progress services.BatchProgress) {
	r.manager.updateBatch(r.jobID, progress, r.baseBatches, r.baseMetrics)
}
