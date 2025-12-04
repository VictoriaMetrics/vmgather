package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

type fakeExportService struct {
	batches []services.BatchProgress
	result  *domain.ExportResult
	err     error
}

func (f *fakeExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
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

type resumeExportService struct {
	mu      sync.Mutex
	configs []domain.ExportConfig
}

func (r *resumeExportService) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	r.mu.Lock()
	r.configs = append(r.configs, config)
	r.mu.Unlock()
	return &domain.ExportResult{ExportID: "resume"}, nil
}

func TestResumeJobUsesSameStagingAndOffset(t *testing.T) {
	service := &resumeExportService{}
	manager := NewExportJobManager(service)

	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		StagingFile: "stage.partial.jsonl",
		Batching:    domain.BatchSettings{Enabled: true},
	}

	if _, err := manager.StartJob(context.Background(), "job-resume", cfg); err != nil {
		t.Fatalf("start job failed: %v", err)
	}
	// Simulate partial progress and cancellation
	manager.mu.Lock()
	job := manager.jobs["job-resume"]
	job.status.State = JobCanceled
	job.status.CompletedBatches = 2
	job.status.MetricsProcessed = 42
	job.resumeFrom = 2
	job.config.ResumeFromBatch = 2
	manager.mu.Unlock()

	resumed, err := manager.ResumeJob(context.Background(), "job-resume")
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resumed.StagingPath != cfg.StagingFile {
		t.Fatalf("staging path lost: %s", resumed.StagingPath)
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
}
