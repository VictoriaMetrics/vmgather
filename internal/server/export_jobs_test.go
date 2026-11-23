package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

type fakeExportService struct {
	batches []services.BatchProgress
	retries []services.AdaptiveRetryProgress
	result  *domain.ExportResult
	err     error
}

func (f *fakeExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	for _, retry := range f.retries {
		services.ReportAdaptiveRetry(ctx, retry)
	}
	for _, batch := range f.batches {
		services.ReportBatchProgress(ctx, batch)
		time.Sleep(5 * time.Millisecond)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type blockingExportService struct {
	blockCh chan struct{}
}

func (b *blockingExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.blockCh:
		return &domain.ExportResult{ExportID: "done"}, nil
	}
}

func TestExportJobManagerTracksProgress(t *testing.T) {
	now := time.Now()
	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: now.Add(-5 * time.Minute),
			End:   now,
		},
		Batching:    domain.BatchSettings{Enabled: true},
		StagingFile: "/tmp/job-progress.partial",
	}

	manager := NewExportJobManager(&fakeExportService{
		batches: []services.BatchProgress{
			{BatchIndex: 1, TotalBatches: 2, Metrics: 100, Duration: 2 * time.Second, TimeRange: cfg.TimeRange},
			{BatchIndex: 2, TotalBatches: 2, Metrics: 150, Duration: 3 * time.Second, TimeRange: cfg.TimeRange},
		},
		result: &domain.ExportResult{ExportID: "job-progress", MetricsExported: 250},
	})

	status, err := manager.StartJob(context.Background(), "job-progress-test", cfg)
	if err != nil {
		t.Fatalf("failed to start job: %v", err)
	}
	if status.State != JobPending {
		t.Fatalf("expected pending job, got %s", status.State)
	}
	if status.StagingPath != cfg.StagingFile {
		t.Fatalf("expected staging path %s, got %s", cfg.StagingFile, status.StagingPath)
	}

	// wait for goroutine to finish
	timeout := time.After(2 * time.Second)
	var final *ExportJobStatus
	for final == nil {
		select {
		case <-timeout:
			t.Fatal("timeout waiting for job completion")
		default:
			if s, ok := manager.GetStatus(status.ID); ok && s.State == JobCompleted {
				final = s
			} else {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}

	if final.CompletedBatches != 2 {
		t.Fatalf("expected two batches completed, got %d", final.CompletedBatches)
	}
	if final.MetricsProcessed != 250 {
		t.Fatalf("expected 250 metrics, got %d", final.MetricsProcessed)
	}
	if final.Result == nil || final.Result.ExportID != "job-progress" {
		t.Fatalf("missing export result in final status: %+v", final.Result)
	}
	if final.Progress < 0.99 {
		t.Fatalf("progress not updated, got %.2f", final.Progress)
	}
}

func TestExportJobStatusTracksAdaptiveRetryFields(t *testing.T) {
	now := time.Now()
	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: now.Add(-10 * time.Minute),
			End:   now,
		},
		Batching:    domain.BatchSettings{Enabled: true},
		StagingFile: "/tmp/job-adaptive.partial",
	}

	manager := NewExportJobManager(&fakeExportService{
		retries: []services.AdaptiveRetryProgress{
			{
				Retries:   1,
				TimeRange: cfg.TimeRange,
				ErrorKind: "too_many_series",
				Strategy:  "split_by_job",
			},
		},
		batches: []services.BatchProgress{
			{BatchIndex: 1, TotalBatches: 1, Metrics: 10, Duration: time.Second, TimeRange: cfg.TimeRange},
		},
		result: &domain.ExportResult{ExportID: "job-adaptive", MetricsExported: 10},
	})

	status, err := manager.StartJob(context.Background(), "job-adaptive", cfg)
	if err != nil {
		t.Fatalf("failed to start job: %v", err)
	}
	if status.State != JobPending {
		t.Fatalf("expected pending job, got %s", status.State)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for adaptive retry status")
		default:
			s, ok := manager.GetStatus(status.ID)
			if ok && s.State == JobCompleted {
				if s.AdaptiveRetries != 1 {
					t.Fatalf("expected adaptive retries=1, got %d", s.AdaptiveRetries)
				}
				if s.LastErrorKind != "too_many_series" {
					t.Fatalf("expected too_many_series, got %s", s.LastErrorKind)
				}
				if s.CurrentStrategy != "split_by_job" {
					t.Fatalf("expected split_by_job strategy, got %s", s.CurrentStrategy)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestExportJobManagerLimitsConcurrency(t *testing.T) {
	blocker := &blockingExportService{blockCh: make(chan struct{})}
	manager := NewExportJobManager(blocker)
	manager.maxConcurrentJobs = 1

	cfg := domain.ExportConfig{
		TimeRange:   domain.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()},
		StagingFile: "/tmp/job-concurrency.partial",
	}
	status, err := manager.StartJob(context.Background(), "job-concurrency-1", cfg)
	if err != nil {
		t.Fatalf("unexpected error starting first job: %v", err)
	}
	if status.State != JobPending {
		t.Fatalf("expected pending state, got %s", status.State)
	}

	if _, err := manager.StartJob(context.Background(), "job-concurrency-2", cfg); err == nil {
		t.Fatal("expected error when exceeding concurrency limit")
	}
	close(blocker.blockCh)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for first job to finish")
		default:
			if s, ok := manager.GetStatus(status.ID); ok && s.State == JobCompleted {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestExportJobManagerCancelJob(t *testing.T) {
	blocker := &blockingExportService{blockCh: make(chan struct{})}
	manager := NewExportJobManager(blocker)
	cfg := domain.ExportConfig{
		TimeRange:   domain.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()},
		StagingFile: "/tmp/job-cancel.partial",
	}
	status, err := manager.StartJob(context.Background(), "job-cancel", cfg)
	if err != nil {
		t.Fatalf("failed to start job: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := manager.CancelJob(status.ID); err != nil {
		t.Fatalf("cancel should succeed, got %v", err)
	}
	close(blocker.blockCh)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("timeout waiting for canceled status")
		default:
			if s, ok := manager.GetStatus(status.ID); ok && s.State == JobCanceled {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

type deadlineProbeExportService struct {
	hasDeadlineCh chan bool
}

func (s *deadlineProbeExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	_, ok := ctx.Deadline()
	s.hasDeadlineCh <- ok
	return &domain.ExportResult{ExportID: "deadline-probe"}, nil
}

func TestExportJobManagerDoesNotSetJobContextDeadlineByDefault(t *testing.T) {
	svc := &deadlineProbeExportService{
		hasDeadlineCh: make(chan bool, 1),
	}
	manager := NewExportJobManager(svc)

	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-time.Minute),
			End:   time.Now(),
		},
		StagingFile: "/tmp/job-no-deadline.partial",
	}

	if _, err := manager.StartJob(context.Background(), "job-no-deadline", cfg); err != nil {
		t.Fatalf("unexpected error starting job: %v", err)
	}

	select {
	case ok := <-svc.hasDeadlineCh:
		if ok {
			t.Fatal("expected no context deadline by default, but deadline was set")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for export service to be called")
	}
}

type resumeExportService struct {
	mu      sync.Mutex
	configs []domain.ExportConfig
	blockCh chan struct{}
}

func (r *resumeExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	r.mu.Lock()
	r.configs = append(r.configs, config)
	r.mu.Unlock()

	if r.blockCh == nil {
		return &domain.ExportResult{ExportID: "resume"}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.blockCh:
		return &domain.ExportResult{ExportID: "resume"}, nil
	}
}

func TestResumeJobUsesSameStagingAndOffset(t *testing.T) {
	service := &resumeExportService{blockCh: make(chan struct{})}
	manager := NewExportJobManager(service)

	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		StagingFile: "stage.partial.jsonl",
		Batching:    domain.BatchSettings{Enabled: true},
	}

	status, err := manager.StartJob(context.Background(), "job-resume", cfg)
	if err != nil {
		t.Fatalf("start job failed: %v", err)
	}

	// Cancel and wait until the manager marks the job as canceled, so we don't race with the job goroutine.
	if err := manager.CancelJob(status.ID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	deadlineCancel := time.Now().Add(2 * time.Second)
	for {
		if s, ok := manager.GetStatus(status.ID); ok && s.State == JobCanceled {
			break
		}
		if time.Now().After(deadlineCancel) {
			t.Fatal("timeout waiting for canceled state")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Simulate partial progress before resume.
	manager.mu.Lock()
	job := manager.jobs["job-resume"]
	job.status.CompletedBatches = 2
	job.status.MetricsProcessed = 42
	job.status.AdaptiveRetries = 1
	job.status.LastErrorKind = "too_many_series"
	job.status.CurrentStrategy = "split_by_job"
	manager.mu.Unlock()

	resumed, err := manager.ResumeJob(context.Background(), "job-resume")
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resumed.StagingPath != cfg.StagingFile {
		t.Fatalf("staging path lost: %s", resumed.StagingPath)
	}
	if resumed.AdaptiveRetries != 0 || resumed.LastErrorKind != "" || resumed.CurrentStrategy != "" {
		t.Fatalf("adaptive retry status was not reset on resume: %+v", resumed)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		service.mu.Lock()
		if len(service.configs) >= 2 {
			service.mu.Unlock()
			break
		}
		service.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("resume did not call ExecuteExport")
		}
		time.Sleep(10 * time.Millisecond)
	}

	service.mu.Lock()
	lastCfg := service.configs[len(service.configs)-1]
	if lastCfg.ResumeFromBatch != 2 {
		t.Fatalf("expected resume_from_batch=2, got %d", lastCfg.ResumeFromBatch)
	}
	if lastCfg.StagingFile != cfg.StagingFile {
		t.Fatalf("expected staging file %s, got %s", cfg.StagingFile, lastCfg.StagingFile)
	}
	service.mu.Unlock()

	close(service.blockCh)
}

type resumableProgressExportService struct {
	mu           sync.Mutex
	calls        int
	totalBatches int
	cancelAfter  int
}

func (s *resumableProgressExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	s.mu.Lock()
	s.calls++
	callNum := s.calls
	s.mu.Unlock()

	startIdx := config.ResumeFromBatch
	for batchIndex := startIdx; batchIndex < s.totalBatches; batchIndex++ {
		services.ReportBatchProgress(ctx, services.BatchProgress{
			BatchIndex:   batchIndex + 1,
			TotalBatches: s.totalBatches,
			TimeRange:    config.TimeRange,
			Metrics:      1,
			Duration:     10 * time.Millisecond,
		})
		time.Sleep(5 * time.Millisecond)
		if callNum == 1 && (batchIndex+1) == s.cancelAfter {
			return nil, context.Canceled
		}
	}
	return &domain.ExportResult{ExportID: "resume-progress", MetricsExported: s.totalBatches}, nil
}

func TestResumeJobDoesNotDoubleCountBatches(t *testing.T) {
	service := &resumableProgressExportService{
		totalBatches: 4,
		cancelAfter:  2,
	}
	manager := NewExportJobManager(service)

	now := time.Now()
	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: now.Add(-20 * time.Minute),
			End:   now,
		},
		Batching:    domain.BatchSettings{Enabled: true},
		StagingFile: "/tmp/job-resume-progress.partial",
	}

	status, err := manager.StartJob(context.Background(), "job-resume-progress", cfg)
	if err != nil {
		t.Fatalf("failed to start job: %v", err)
	}

	// Wait for the job to become canceled at the configured checkpoint.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for canceled status")
		default:
			if s, ok := manager.GetStatus(status.ID); ok && s.State == JobCanceled {
				if s.CompletedBatches != 2 {
					t.Fatalf("expected 2 completed batches before resume, got %d", s.CompletedBatches)
				}
				goto resume
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

resume:
	if _, err := manager.ResumeJob(context.Background(), status.ID); err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	// Wait for completion after resume.
	deadline = time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for resumed job completion")
		default:
			if s, ok := manager.GetStatus(status.ID); ok && s.State == JobCompleted {
				if s.CompletedBatches != 4 {
					t.Fatalf("expected 4 completed batches after resume, got %d", s.CompletedBatches)
				}
				if s.MetricsProcessed != 4 {
					t.Fatalf("expected 4 metrics processed, got %d", s.MetricsProcessed)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestExportJobManagerCleanupRemovesCanceledJobsAfterRetention(t *testing.T) {
	manager := NewExportJobManager(&fakeExportService{})
	manager.retention = 1 * time.Second

	completedAt := time.Now().Add(-2 * time.Second)
	job := &exportJob{
		status: &ExportJobStatus{
			ID:          "job-canceled",
			State:       JobCanceled,
			CompletedAt: &completedAt,
		},
	}

	manager.mu.Lock()
	manager.jobs[job.status.ID] = job
	manager.cleanupLocked(time.Now())
	manager.mu.Unlock()

	if _, ok := manager.GetStatus(job.status.ID); ok {
		t.Fatalf("expected canceled job to be removed after retention")
	}
}

// A failed job keeps its staging file for resume during the retention; after that nothing can resume
// it, so the file goes too. Recent failed jobs keep theirs.
func TestExportJobManagerCleanupRemovesStagingOfExpiredJobs(t *testing.T) {
	manager := NewExportJobManager(&fakeExportService{})
	manager.retention = time.Second
	dir := t.TempDir()
	old, recent := time.Now().Add(-2*time.Second), time.Now()
	jobs := map[string]*time.Time{"job-old": &old, "job-recent": &recent}
	manager.mu.Lock()
	for id, at := range jobs {
		path := filepath.Join(dir, id+".partial.jsonl")
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		manager.jobs[id] = &exportJob{status: &ExportJobStatus{ID: id, State: JobFailed, CompletedAt: at, StagingPath: path}}
	}
	manager.cleanupLocked(time.Now())
	manager.mu.Unlock()

	waitRemoved(t, filepath.Join(dir, "job-old.partial.jsonl"), 2*time.Second)
	if _, err := os.Stat(filepath.Join(dir, "job-recent.partial.jsonl")); err != nil {
		t.Fatalf("staging file of a recent failed job is gone: %v", err)
	}
}

// A server that gets no more requests still removes the staging file when the retention of a failed job
// ends: cleanup is scheduled when the job finishes.
func TestExportJobManagerRemovesStagingOfIdleServer(t *testing.T) {
	manager := NewExportJobManager(&fakeExportService{err: errors.New("boom")})
	manager.retention = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "job-idle.partial.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartJob(context.Background(), "job-idle", domain.ExportConfig{StagingFile: path}); err != nil {
		t.Fatal(err)
	}
	// retention + the 1s margin of the scheduled cleanup
	waitRemoved(t, path, 3*time.Second)
}

func waitRemoved(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is still there after %s", path, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
