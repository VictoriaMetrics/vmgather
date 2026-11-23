package services

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/archive"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// defaultBatchTimeout bounds one query_range attempt. /api/v1/export attempts have no total
// deadline; vm.Client cancels them only when the stream goes idle (vm.ExportIdleTimeout).
const defaultBatchTimeout = 2 * time.Minute

// maxExportTimeoutSplitDepth caps time splits after a timeout on the /api/v1/export path
// (1h window down to 7.5 minutes), so a slow upstream fails in minutes, not after many retries.
const maxExportTimeoutSplitDepth = 3

// transientRetryDelays are the waits before retrying an attempt that failed with a transient
// upstream error (502/503/504, connection reset). They cover a typical vmselect pod restart.
var transientRetryDelays = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}

// writeBufferSize sizes the bufio.Writer used for batch/attempt output files.
// The stdlib default (4KB) meant a Write syscall roughly every 15-40 metric
// lines; profiling a live export showed write(2) syscalls as the single
// largest CPU cost, so a much bigger buffer cuts syscall count accordingly.
const writeBufferSize = 256 * 1024

var (
	metricSelectorPattern = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)\s*\{(.*)\}$`)
	metricNamePattern     = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	jobMatcherPattern     = regexp.MustCompile(`(^|,)\s*job\s*(=|!=|=~|!~)`)
)

type exportAttempt struct {
	Selector         string
	TimeRange        domain.TimeRange
	UseQueryRange    bool
	Depth            int
	Jobs             []string
	Strategy         string
	StepSeconds      int
	TransientRetries int
}

type adaptiveExportStats struct {
	Mode               domain.ExportAdaptivityMode
	MaxStepSecondsUsed int
	Sampled            bool
	Decisions          []archive.AdaptiveDecision
}

// ExportService interface for full export operations
type ExportService interface {
	// ExecuteExport performs full export with optional obfuscation
	ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error)
}

// exportServiceImpl implements ExportService
type exportServiceImpl struct {
	clientFactory   func(domain.VMConnection) *vm.Client
	archiveWriter   *archive.Writer
	vmGatherVersion string
}

// NewExportService creates a new export service
func NewExportService(outputDir, version string) ExportService {
	if version == "" {
		version = "dev"
	}
	return &exportServiceImpl{
		clientFactory:   vm.NewClient,
		archiveWriter:   archive.NewWriter(outputDir),
		vmGatherVersion: version,
	}
}

// ExportToWriter streams exported metrics into the provided writer.
// Intended for CLI oneshot mode; writes JSONL metrics without creating an archive.
func ExportToWriter(ctx context.Context, config domain.ExportConfig, writer io.Writer) (int, error) {
	service := &exportServiceImpl{
		clientFactory:   vm.NewClient,
		archiveWriter:   archive.NewWriter(os.TempDir()),
		vmGatherVersion: "dev",
	}
	return service.exportToWriter(ctx, config, writer)
}

// ExecuteExport performs full metrics export with optional obfuscation
func (s *exportServiceImpl) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	applyExportServiceDefaults(&config)
	if err := ValidateExportConfig(config); err != nil {
		return nil, err
	}

	// Generate export ID
	exportID := s.generateExportID()

	// Step 1: Prepare staging file for incremental writes
	stagingDir := config.StagingDir
	if stagingDir == "" {
		stagingDir = filepath.Join(s.archiveWriter.OutputDir(), "staging")
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to prepare staging directory: %w", err)
	}
	if config.StagingFile == "" {
		config.StagingFile = filepath.Join(stagingDir, fmt.Sprintf("%s.partial.jsonl", exportID))
	}
	resuming := config.ResumeFromBatch > 0
	state := resumeStateFrom(ctx, resuming)
	if resuming && !state.recorded {
		// Without the metric count and the staging size of the failed run the archive would count
		// wrong and keep a partly appended batch.
		return nil, fmt.Errorf("cannot resume from batch %d: no progress was recorded for this export", config.ResumeFromBatch)
	}
	if resuming && state.StagingBytes > 0 {
		// Truncate would extend a missing or cut file with zero bytes, and the archive would hold them.
		if fi, err := os.Stat(config.StagingFile); err != nil || fi.Size() < state.StagingBytes {
			return nil, fmt.Errorf("cannot resume: the staging file %s is missing or shorter than the %d bytes of completed batches; start a new export", config.StagingFile, state.StagingBytes)
		}
	}
	if err := initializeStagingFile(config.StagingFile, resuming); err != nil {
		return nil, fmt.Errorf("failed to initialize staging file: %w", err)
	}
	if resuming {
		// Drop a partially appended batch (for example after a full disk) before appending again.
		if err := os.Truncate(config.StagingFile, state.StagingBytes); err != nil {
			return nil, fmt.Errorf("failed to restore staging file for resume: %w", err)
		}
	}

	staging, err := openStagingFile(config.StagingFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = staging.close() }()

	// Step 2: Export metrics from VictoriaMetrics in batches
	client := s.clientFactory(config.Connection)
	batchWindows := CalculateBatchWindows(config.TimeRange, config.Batching)
	metricsCount := state.MetricsCount
	proc := newLineProcessor(config)
	defer proc.close()
	adaptiveStats := &adaptiveExportStats{
		Mode:               config.Safety.Mode,
		MaxStepSecondsUsed: config.MetricStepSeconds,
	}

	// startIdx == len(batchWindows) means every batch is already in staging (the archive step failed),
	// so the loop is skipped and only the archive is created.
	startIdx := config.ResumeFromBatch
	if startIdx < 0 || startIdx > len(batchWindows) {
		return nil, fmt.Errorf("cannot resume from batch %d: the export has %d batches", startIdx, len(batchWindows))
	}

	for batchIndex := startIdx; batchIndex < len(batchWindows); batchIndex++ {
		window := borderSafeWindow(batchWindows[batchIndex], config.TimeRange.End)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		fmt.Printf("Processing batch %d/%d (%s - %s)\n",
			batchIndex+1, len(batchWindows), window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339))
		batchStart := time.Now()

		if err := s.primeMDX(ctx, client, config, proc.mdx, window); err != nil {
			return nil, err
		}
		attempt := s.newExportAttempt(config, window)
		retryCount := 0
		batchCount, err := s.exportWindowAdaptive(ctx, client, config, attempt, staging, proc, &retryCount, adaptiveStats)
		if err != nil {
			fmt.Printf("[ERROR] Metrics processing failed for batch %d: %v\n", batchIndex+1, err)
			return nil, fmt.Errorf("metrics processing failed: %w", err)
		}

		metricsCount += batchCount
		size, err := staging.mark()
		if err != nil {
			return nil, err
		}
		state.recordBatch(size, metricsCount)
		batchDuration := time.Since(batchStart)
		fmt.Printf("[OK] Batch %d processed in %v (%d metrics)\n", batchIndex+1, batchDuration, batchCount)

		ReportBatchProgress(ctx, BatchProgress{
			BatchIndex:   batchIndex + 1,
			TotalBatches: len(batchWindows),
			TimeRange:    window,
			Metrics:      batchCount,
			Duration:     batchDuration,
		})
	}

	if proc.mdx != nil && metricsCount == 0 && proc.mdx.dropped > 0 {
		log.Printf("[WARN] the MDX filter dropped all %d series: no vm_app_version series matched their job and instance", proc.mdx.dropped)
	}

	if err := staging.close(); err != nil {
		return nil, err
	}

	// Step 3: Create archive
	fmt.Printf("Creating archive...\n")
	metadata := s.buildArchiveMetadata(exportID, config, metricsCount, adaptiveStats)
	processedReader, err := os.Open(config.StagingFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open staging file for archive: %w", err)
	}

	archiveStartTime := time.Now()
	archivePath, sha256sum, err := s.archiveWriter.CreateArchive(exportID, processedReader, metadata)
	// Close before the staging file is removed below: Windows cannot delete an open file.
	_ = processedReader.Close()
	if err != nil {
		fmt.Printf("[ERROR] Archive creation failed: %v\n", err)
		return nil, fmt.Errorf("archive creation failed: %w", err)
	}
	fmt.Printf("[OK] Archive created in %v\n", time.Since(archiveStartTime))

	// Step 4: Get archive size
	archiveSize, err := s.archiveWriter.GetArchiveSize(archivePath)
	if err != nil {
		fmt.Printf("[ERROR] Failed to get archive size: %v\n", err)
		return nil, fmt.Errorf("failed to get archive size: %w", err)
	}
	fmt.Printf("Archive size: %.2f MB\n", float64(archiveSize)/(1024*1024))
	fmt.Printf("SHA256: %s\n", sha256sum)

	// The archive holds everything now; a resumed export also leaves no raw JSONL behind.
	if err := os.Remove(config.StagingFile); err != nil {
		log.Printf("[WARN] Failed to remove staging file %s: %v", config.StagingFile, err)
	}

	// Build result
	result := &domain.ExportResult{
		ExportID:           exportID,
		ArchivePath:        archivePath,
		ArchiveName:        filepath.Base(archivePath),
		ArchiveSizeBytes:   archiveSize,
		MetricsExported:    metricsCount,
		TimeRange:          config.TimeRange,
		ObfuscationApplied: config.Obfuscation.Enabled,
		SHA256:             sha256sum,
	}

	return result, nil
}

func initializeStagingFile(path string, preserve bool) error {
	flags := os.O_CREATE | os.O_WRONLY
	if preserve {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	handle, err := os.OpenFile(path, flags, 0o640)
	if err != nil {
		return err
	}
	return handle.Close()
}

func (s *exportServiceImpl) exportToWriter(ctx context.Context, config domain.ExportConfig, writer io.Writer) (int, error) {
	applyExportServiceDefaults(&config)
	if err := ValidateExportConfig(config); err != nil {
		return 0, err
	}

	client := s.clientFactory(config.Connection)
	selector, useQueryRange := s.buildExportQuery(config)
	batchWindows := CalculateBatchWindows(config.TimeRange, config.Batching)
	metricsCount := 0
	proc := newLineProcessor(config)
	defer proc.close()

	buffered := bufio.NewWriterSize(writer, writeBufferSize)
	for _, window := range batchWindows {
		window = borderSafeWindow(window, config.TimeRange.End)
		if err := s.primeMDX(ctx, client, config, proc.mdx, window); err != nil {
			return 0, err
		}
		batchCtx, cancelBatch := attemptContext(ctx, useQueryRange)
		exportReader, err := s.fetchBatch(batchCtx, client, selector, window, config.MetricStepSeconds, useQueryRange)
		if err != nil {
			cancelBatch()
			return 0, err
		}

		count, err := proc.process(exportReader, buffered)
		cancelBatch()
		if closeErr := exportReader.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			return 0, err
		}
		metricsCount += count
	}

	if err := buffered.Flush(); err != nil {
		return 0, fmt.Errorf("flush error: %w", err)
	}
	return metricsCount, nil
}

func applyExportServiceDefaults(config *domain.ExportConfig) {
	originalBatching := config.Batching
	ApplyExportDefaults(config)
	if !originalBatching.Enabled && originalBatching.Strategy == "" && originalBatching.CustomIntervalSecs == 0 {
		config.Batching = originalBatching
	}
}

func (s *exportServiceImpl) newExportAttempt(config domain.ExportConfig, tr domain.TimeRange) exportAttempt {
	selector, useQueryRange := s.buildExportQuery(config)
	strategy := "export"
	if useQueryRange {
		strategy = "query_range"
	}
	return exportAttempt{
		Selector:      selector,
		TimeRange:     tr,
		UseQueryRange: useQueryRange,
		Jobs:          uniqueStrings(config.Jobs),
		Strategy:      strategy,
		StepSeconds:   config.MetricStepSeconds,
	}
}

func (s *exportServiceImpl) exportWindowAdaptive(
	ctx context.Context,
	client *vm.Client,
	config domain.ExportConfig,
	attempt exportAttempt,
	staging *stagingFile,
	proc *lineProcessor,
	retryCount *int,
	stats *adaptiveExportStats,
) (int, error) {
	log.Printf("[EXPORT][ATTEMPT] strategy=%s depth=%d use_query_range=%v step=%ds jobs=%d window=%s..%s selector=%s",
		attempt.Strategy,
		attempt.Depth,
		attempt.UseQueryRange,
		attempt.StepSeconds,
		len(attempt.Jobs),
		attempt.TimeRange.Start.Format(time.RFC3339),
		attempt.TimeRange.End.Format(time.RFC3339),
		formatSelectorForLog(attempt.Selector),
	)
	start, err := staging.mark()
	if err != nil {
		return 0, err
	}
	count, err := s.fetchAndProcessAttempt(ctx, client, config, attempt, proc, staging, stats)
	if err == nil {
		log.Printf("[EXPORT][ATTEMPT][OK] strategy=%s depth=%d metrics=%d window=%s..%s",
			attempt.Strategy,
			attempt.Depth,
			count,
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
		)
		return count, nil
	}
	if rbErr := staging.rollback(start); rbErr != nil {
		return 0, rbErr
	}

	kind := vm.ErrorKindOf(err)
	log.Printf("[EXPORT][ATTEMPT][FAIL] strategy=%s depth=%d kind=%s step=%ds jobs=%d window=%s..%s selector=%s err=%v",
		attempt.Strategy,
		attempt.Depth,
		kind,
		attempt.StepSeconds,
		len(attempt.Jobs),
		attempt.TimeRange.Start.Format(time.RFC3339),
		attempt.TimeRange.End.Format(time.RFC3339),
		formatSelectorForLog(attempt.Selector),
		err,
	)

	switch {
	case kind == vm.ErrorKindMissingRoute && !attempt.UseQueryRange:
		*retryCount++
		log.Printf("[EXPORT][ADAPTIVE] decision=query_range_fallback retries=%d kind=%s window=%s..%s selector=%s",
			*retryCount,
			kind,
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
			formatSelectorForLog(attempt.Selector),
		)
		progress := AdaptiveRetryProgress{
			Retries:   *retryCount,
			TimeRange: attempt.TimeRange,
			ErrorKind: string(kind),
			Strategy:  "query_range",
		}
		ReportAdaptiveRetry(ctx, progress)
		recordAdaptiveDecision(stats, progress)
		next := attempt
		next.UseQueryRange = true
		next.Depth++
		next.Strategy = "query_range"
		return s.exportWindowAdaptive(ctx, client, config, next, staging, proc, retryCount, stats)
	case kind == vm.ErrorKindTooManySeries && config.Safety.AutoSplit && attempt.Depth < config.Safety.MaxSplitDepth && config.Safety.SplitByJob && len(attempt.Jobs) > 1:
		*retryCount++
		log.Printf("[EXPORT][ADAPTIVE] decision=split_by_job retries=%d kind=%s jobs=%s window=%s..%s",
			*retryCount,
			kind,
			formatJobsForLog(attempt.Jobs),
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
		)
		progress := AdaptiveRetryProgress{
			Retries:   *retryCount,
			TimeRange: attempt.TimeRange,
			ErrorKind: string(kind),
			Strategy:  "split_by_job",
		}
		ReportAdaptiveRetry(ctx, progress)
		recordAdaptiveDecision(stats, progress)
		children := make([]exportAttempt, 0, len(attempt.Jobs))
		for _, job := range attempt.Jobs {
			child, ok := s.buildJobSplitAttempt(config, attempt, job)
			if !ok {
				return 0, enrichAdaptiveError(kind, err, attempt, config.Safety.MinWindowSeconds)
			}
			child.Depth = attempt.Depth + 1
			children = append(children, child)
		}
		return s.runSplitAttemptsAtomically(ctx, client, config, children, staging, proc, retryCount, stats)
	case kind == vm.ErrorKindTransient && attempt.TransientRetries < len(transientRetryDelays):
		delay := transientRetryDelays[attempt.TransientRetries]
		*retryCount++
		log.Printf("[EXPORT][ADAPTIVE] decision=retry retries=%d kind=%s wait=%s window=%s..%s",
			*retryCount,
			kind,
			delay,
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
		)
		progress := AdaptiveRetryProgress{
			Retries:     *retryCount,
			TimeRange:   attempt.TimeRange,
			ErrorKind:   string(kind),
			Strategy:    "retry",
			StepSeconds: attempt.StepSeconds,
		}
		ReportAdaptiveRetry(ctx, progress)
		recordAdaptiveDecision(stats, progress)
		if err := sleepContext(ctx, delay); err != nil {
			return 0, err
		}
		next := attempt
		next.TransientRetries++
		return s.exportWindowAdaptive(ctx, client, config, next, staging, proc, retryCount, stats)
	case kind == vm.ErrorKindQueryTimeout:
		maxDepth := config.Safety.MaxSplitDepth
		if !attempt.UseQueryRange && maxDepth > maxExportTimeoutSplitDepth {
			maxDepth = maxExportTimeoutSplitDepth
		}
		if config.Safety.AutoSplit && attempt.Depth < maxDepth {
			align := time.Second
			if attempt.UseQueryRange && attempt.StepSeconds > 0 {
				align = time.Duration(attempt.StepSeconds) * time.Second
			}
			left, right, ok := splitTimeRange(attempt.TimeRange, config.Safety.MinWindowSeconds, align)
			if ok {
				*retryCount++
				log.Printf("[EXPORT][ADAPTIVE] decision=split_by_time retries=%d kind=%s step=%ds window=%s..%s -> [%s..%s] + [%s..%s]",
					*retryCount,
					kind,
					attempt.StepSeconds,
					attempt.TimeRange.Start.Format(time.RFC3339),
					attempt.TimeRange.End.Format(time.RFC3339),
					left.Start.Format(time.RFC3339),
					left.End.Format(time.RFC3339),
					right.Start.Format(time.RFC3339),
					right.End.Format(time.RFC3339),
				)
				progress := AdaptiveRetryProgress{
					Retries:     *retryCount,
					TimeRange:   attempt.TimeRange,
					ErrorKind:   string(kind),
					Strategy:    "split_by_time",
					StepSeconds: attempt.StepSeconds,
				}
				ReportAdaptiveRetry(ctx, progress)
				recordAdaptiveDecision(stats, progress)
				leftAttempt := attempt
				leftAttempt.TimeRange = left
				leftAttempt.Depth = attempt.Depth + 1
				leftAttempt.TransientRetries = 0
				rightAttempt := leftAttempt
				rightAttempt.TimeRange = right

				return s.runSplitAttemptsAtomically(ctx, client, config, []exportAttempt{leftAttempt, rightAttempt}, staging, proc, retryCount, stats)
			}
		}
		if !attempt.UseQueryRange {
			break
		}
		if nextStep, ok := nextAutopilotStep(attempt.StepSeconds, config.Safety); ok {
			*retryCount++
			log.Printf("[EXPORT][ADAPTIVE] decision=increase_step retries=%d kind=%s from_step=%ds to_step=%ds window=%s..%s",
				*retryCount,
				kind,
				attempt.StepSeconds,
				nextStep,
				attempt.TimeRange.Start.Format(time.RFC3339),
				attempt.TimeRange.End.Format(time.RFC3339),
			)
			progress := AdaptiveRetryProgress{
				Retries:     *retryCount,
				TimeRange:   attempt.TimeRange,
				ErrorKind:   string(kind),
				Strategy:    "increase_step",
				StepSeconds: nextStep,
			}
			ReportAdaptiveRetry(ctx, progress)
			recordAdaptiveDecision(stats, progress)
			next := attempt
			next.StepSeconds = nextStep
			next.Depth = 0
			next.Strategy = "query_range"
			return s.exportWindowAdaptive(ctx, client, config, next, staging, proc, retryCount, stats)
		}
	default:
	}

	logAdaptiveFailure(config, attempt, kind, err)
	return 0, enrichAdaptiveError(kind, err, attempt, config.Safety.MinWindowSeconds)
}

// runSplitAttemptsAtomically runs the parts of a split window. If one part fails, the output of the
// parts that already succeeded is rolled back too, so the window is either complete or absent.
func (s *exportServiceImpl) runSplitAttemptsAtomically(
	ctx context.Context,
	client *vm.Client,
	config domain.ExportConfig,
	children []exportAttempt,
	staging *stagingFile,
	proc *lineProcessor,
	retryCount *int,
	stats *adaptiveExportStats,
) (int, error) {
	start, err := staging.mark()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, child := range children {
		count, err := s.exportWindowAdaptive(ctx, client, config, child, staging, proc, retryCount, stats)
		if err != nil {
			if rbErr := staging.rollback(start); rbErr != nil {
				return 0, rbErr
			}
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (s *exportServiceImpl) fetchAndProcessAttempt(
	ctx context.Context,
	client *vm.Client,
	config domain.ExportConfig,
	attempt exportAttempt,
	proc *lineProcessor,
	staging *stagingFile,
	stats *adaptiveExportStats,
) (int, error) {
	batchCtx, cancelBatch := attemptContext(ctx, attempt.UseQueryRange)
	defer cancelBatch()

	exportReader, err := s.fetchAttemptReader(batchCtx, client, attempt)
	if err != nil {
		return 0, err
	}
	count, err := proc.process(exportReader, staging)
	if closeErr := exportReader.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	recordAttemptSuccess(stats, attempt)
	return count, nil
}

// attemptContext bounds a query_range attempt by defaultBatchTimeout. /api/v1/export attempts get no
// total deadline: vm.Client cancels them when the stream goes idle, so big healthy streams are not cut.
func attemptContext(ctx context.Context, useQueryRange bool) (context.Context, context.CancelFunc) {
	if useQueryRange {
		return context.WithTimeout(ctx, defaultBatchTimeout)
	}
	return context.WithCancel(ctx)
}

// buildSelector builds the series selector for cluster mode. ValidateExportConfig guarantees at least
// one job, so the selector is never a whole-database match.
func (s *exportServiceImpl) buildSelector(jobs []string) string {
	return buildJobFilterSelector(jobs)
}

// ValidateExportConfig rejects configs that would export the whole database by accident:
// cluster mode without jobs and custom mode without a query.
func ValidateExportConfig(config domain.ExportConfig) error {
	if config.Mode == domain.ExportModeCustom {
		if strings.TrimSpace(config.Query) == "" {
			return fmt.Errorf("custom mode requires a selector or MetricsQL query")
		}
		if config.MDXFilter {
			return fmt.Errorf("the MDX filter works only in cluster mode")
		}
		return nil
	}
	if len(uniqueStrings(config.Jobs)) == 0 {
		return fmt.Errorf("no jobs selected: select at least one job, or use custom mode with an explicit selector")
	}
	return nil
}

// sleepContext waits for d or until ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *exportServiceImpl) buildExportQuery(config domain.ExportConfig) (string, bool) {
	if config.Mode == domain.ExportModeCustom && config.Query != "" {
		switch config.QueryType {
		case domain.QueryModeSelector:
			selector := config.Query
			if len(config.Jobs) > 0 {
				if rewritten, ok := applyJobMatcherToSelector(selector, config.Jobs); ok {
					return rewritten, false
				}
				filter := buildJobFilterSelector(config.Jobs)
				selector = fmt.Sprintf("(%s) and on(job) %s", selector, filter)
				return selector, true
			}
			return selector, false
		case domain.QueryModeMetricsQL:
			return config.Query, true
		default:
			return config.Query, false
		}
	}

	return s.buildSelector(config.Jobs), false
}

func (s *exportServiceImpl) buildJobSplitAttempt(config domain.ExportConfig, parent exportAttempt, job string) (exportAttempt, bool) {
	child := exportAttempt{
		TimeRange:   parent.TimeRange,
		Jobs:        []string{job},
		StepSeconds: parent.StepSeconds,
	}

	switch {
	case config.Mode == domain.ExportModeCustom && config.QueryType == domain.QueryModeSelector:
		if rewritten, ok := applyJobMatcherToSelector(config.Query, []string{job}); ok {
			child.Selector = rewritten
			child.UseQueryRange = false
			child.Strategy = "export"
			return child, true
		}
		filter := buildJobFilterSelector([]string{job})
		child.Selector = fmt.Sprintf("(%s) and on(job) %s", config.Query, filter)
		child.UseQueryRange = true
		child.Strategy = "query_range"
		return child, true
	case config.Mode == domain.ExportModeCluster:
		child.Selector = s.buildSelector([]string{job})
		child.UseQueryRange = false
		child.Strategy = "export"
		return child, true
	default:
		return exportAttempt{}, false
	}
}

func applyJobMatcherToSelector(selector string, jobs []string) (string, bool) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" || len(jobs) == 0 {
		return "", false
	}
	jobMatcher := buildJobFilterSelector(jobs)
	jobMatcher = strings.TrimPrefix(strings.TrimSuffix(jobMatcher, "}"), "{")

	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		inside := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "{"), "}"))
		if containsJobMatcher(inside) {
			return "", false
		}
		if inside == "" {
			return fmt.Sprintf("{%s}", jobMatcher), true
		}
		return fmt.Sprintf("{%s,%s}", inside, jobMatcher), true
	}

	if matches := metricSelectorPattern.FindStringSubmatch(trimmed); len(matches) == 3 {
		metricName := matches[1]
		inside := strings.TrimSpace(matches[2])
		if containsJobMatcher(inside) {
			return "", false
		}
		if inside == "" {
			return fmt.Sprintf("%s{%s}", metricName, jobMatcher), true
		}
		return fmt.Sprintf("%s{%s,%s}", metricName, inside, jobMatcher), true
	}

	if metricNamePattern.MatchString(trimmed) {
		return fmt.Sprintf("%s{%s}", trimmed, jobMatcher), true
	}

	return "", false
}

func containsJobMatcher(selectorBody string) bool {
	return jobMatcherPattern.MatchString(selectorBody)
}

func nextAutopilotStep(current int, safety domain.ExportSafetyConfig) (int, bool) {
	if safety.Mode != domain.ExportAdaptivityAutopilot {
		return 0, false
	}
	for _, step := range normalizeStepLadder(safety.StepLadderSeconds, safety.MaxStepSeconds) {
		if step > current {
			return step, true
		}
	}
	return 0, false
}

func recordAdaptiveDecision(stats *adaptiveExportStats, progress AdaptiveRetryProgress) {
	if stats == nil {
		return
	}
	stats.Decisions = append(stats.Decisions, archive.AdaptiveDecision{
		Strategy:    progress.Strategy,
		ErrorKind:   progress.ErrorKind,
		TimeRange:   progress.TimeRange,
		StepSeconds: progress.StepSeconds,
	})
	if progress.StepSeconds > stats.MaxStepSecondsUsed {
		stats.MaxStepSecondsUsed = progress.StepSeconds
	}
	if progress.Strategy == "increase_step" {
		stats.Sampled = true
	}
}

func recordAttemptSuccess(stats *adaptiveExportStats, attempt exportAttempt) {
	if stats == nil {
		return
	}
	if attempt.UseQueryRange {
		stats.Sampled = true
	}
	if attempt.StepSeconds > stats.MaxStepSecondsUsed {
		stats.MaxStepSecondsUsed = attempt.StepSeconds
	}
}

// buildArchiveMetadata builds archive metadata from export config
func (s *exportServiceImpl) buildArchiveMetadata(
	exportID string,
	config domain.ExportConfig,
	metricsCount int,
	adaptiveStats *adaptiveExportStats,
) archive.ArchiveMetadata {
	metadata := archive.ArchiveMetadata{
		ExportID:             exportID,
		ExportDate:           time.Now().UTC(),
		TimeRange:            config.TimeRange,
		Components:           uniqueStrings(config.Components),
		Jobs:                 metadataJobs(config),
		MetricsCount:         metricsCount,
		Obfuscated:           config.Obfuscation.Enabled,
		MDXFiltered:          config.MDXFilter,
		AdaptiveMode:         string(config.Safety.Mode),
		MetricStepSeconds:    config.MetricStepSeconds,
		MaxMetricStepSeconds: config.Safety.MaxStepSeconds,
		VMGatherVersion:      s.vmGatherVersion,
	}
	if adaptiveStats != nil {
		metadata.AdaptiveMode = string(adaptiveStats.Mode)
		metadata.Sampled = adaptiveStats.Sampled
		if adaptiveStats.Sampled {
			metadata.MetricStepSeconds = adaptiveStats.MaxStepSecondsUsed
		} else {
			metadata.MetricStepSeconds = 0
		}
		metadata.AdaptiveDecisions = append([]archive.AdaptiveDecision(nil), adaptiveStats.Decisions...)
	}
	return metadata
}

// metadataJobs returns the job list for metadata.json. When the job label is obfuscated, the names are
// obfuscated the same way, so the archive does not carry real job names next to the hashed labels.
func metadataJobs(config domain.ExportConfig) []string {
	jobs := uniqueStrings(config.Jobs)
	if !slices.Contains(config.Obfuscation.LabelNames(), "job") {
		return jobs
	}
	for i, job := range jobs {
		jobs[i] = obfuscation.Value(job)
	}
	return jobs
}

// isMissingRouteError checks if error is due to missing export route
func (s *exportServiceImpl) isMissingRouteError(err error) bool {
	return vm.ErrorKindOf(err) == vm.ErrorKindMissingRoute
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

// splitTimeRange splits a window into two halves. While a half is at least align long, the midpoint is a
// multiple of align from the start, so query_range halves stay on the step grid; smaller halves split on
// whole seconds. The left half ends 1ms before the right one starts, so a point on the border is evaluated
// once. A window that already ends 1ms early (borderSafeWindow) splits on a whole multiple too: its length
// plus 1ms is halved.
func splitTimeRange(tr domain.TimeRange, minWindowSeconds int, align time.Duration) (domain.TimeRange, domain.TimeRange, bool) {
	minWindow := time.Duration(minWindowSeconds) * time.Second
	if minWindowSeconds <= 0 {
		minWindow = 5 * time.Second
	}
	duration := tr.End.Sub(tr.Start)
	if duration <= minWindow {
		return domain.TimeRange{}, domain.TimeRange{}, false
	}

	half := (duration + time.Millisecond) / 2
	if align > time.Second && half >= align {
		half = half.Truncate(align)
	} else {
		half = half.Truncate(time.Second)
	}
	mid := tr.Start.Add(half)
	if !mid.After(tr.Start) || !tr.End.After(mid) {
		return domain.TimeRange{}, domain.TimeRange{}, false
	}
	return domain.TimeRange{Start: tr.Start, End: mid.Add(-time.Millisecond)}, domain.TimeRange{Start: mid, End: tr.End}, true
}

// borderSafeWindow ends a window 1ms before the next one starts. VictoriaMetrics treats start and end
// as inclusive, so without it a sample exactly on a border (for example a vmalert recording rule result
// at :00) is exported twice. The last window keeps its end, so the requested range stays inclusive.
func borderSafeWindow(window domain.TimeRange, rangeEnd time.Time) domain.TimeRange {
	if window.End.Before(rangeEnd) {
		window.End = window.End.Add(-time.Millisecond)
	}
	return window
}

func enrichAdaptiveError(kind vm.ErrorKind, err error, attempt exportAttempt, minWindowSeconds int) error {
	switch kind {
	case vm.ErrorKindQueryTimeout:
		if !attempt.UseQueryRange {
			return fmt.Errorf("export timed out and splitting the time range did not help or is disabled; narrow the selector or choose a smaller batch window: %w", err)
		}
		return fmt.Errorf("query still exceeds VictoriaMetrics -search.maxQueryDuration after splitting down to %ds windows and sampling at %ds; narrow selector/query or use raw selector export: %w", minWindowSeconds, attempt.StepSeconds, err)
	case vm.ErrorKindTooManySeries:
		if len(attempt.Jobs) <= 1 {
			return fmt.Errorf("query exceeds VictoriaMetrics series limits and vmgather cannot split the request further by job; narrow selector/query or adjust VictoriaMetrics -search.maxExportSeries / -search.maxUniqueTimeseries: %w", err)
		}
		return fmt.Errorf("query exceeds VictoriaMetrics series limits; narrow selector/query or adjust VictoriaMetrics -search.maxExportSeries / -search.maxUniqueTimeseries: %w", err)
	default:
		if attempt.UseQueryRange {
			return fmt.Errorf("adaptive export failed while using query_range: %w", err)
		}
		return err
	}
}

func logAdaptiveFailure(config domain.ExportConfig, attempt exportAttempt, kind vm.ErrorKind, err error) {
	switch kind {
	case vm.ErrorKindTooManySeries:
		log.Printf("[EXPORT][LIMIT] too_many_series unrecoverable jobs=%d split_by_job=%v use_query_range=%v selector=%s err=%v",
			len(attempt.Jobs),
			config.Safety.SplitByJob,
			attempt.UseQueryRange,
			formatSelectorForLog(attempt.Selector),
			err,
		)
	case vm.ErrorKindQueryTimeout:
		log.Printf("[EXPORT][LIMIT] query_timeout unrecoverable min_window_seconds=%d step=%ds depth=%d selector=%s err=%v",
			config.Safety.MinWindowSeconds,
			attempt.StepSeconds,
			attempt.Depth,
			formatSelectorForLog(attempt.Selector),
			err,
		)
	default:
		log.Printf("[EXPORT][FAIL] unrecoverable kind=%s strategy=%s depth=%d selector=%s err=%v",
			kind,
			attempt.Strategy,
			attempt.Depth,
			formatSelectorForLog(attempt.Selector),
			err,
		)
	}
}

func determineQueryRangeStep(tr domain.TimeRange, overrideSeconds int) time.Duration {
	if overrideSeconds > 0 {
		step := time.Duration(overrideSeconds) * time.Second
		if step < minBatchInterval {
			return minBatchInterval
		}
		if step > maxBatchInterval {
			return maxBatchInterval
		}
		return step
	}
	return recommendedIntervalForDuration(tr.End.Sub(tr.Start))
}

// exportViaQueryRange exports metrics using query_range as fallback when /api/v1/export is not available
// This method queries all series matching the selector and reconstructs export format
// It uses streaming and time chunking to avoid OOM on large time ranges
func (s *exportServiceImpl) exportViaQueryRange(ctx context.Context, client *vm.Client, selector string, timeRange domain.TimeRange, overrideSeconds int) (io.ReadCloser, error) {
	step := determineQueryRangeStep(timeRange, overrideSeconds)

	// Create a pipe to stream results
	pr, pw := io.Pipe()

	go func() {
		// Chunk size: 1 hour (balance between request count and memory usage)
		chunkSize := 1 * time.Hour

		currentStart := timeRange.Start
		totalPoints := 0
		var line []byte

		fmt.Printf("Starting streaming query_range fallback (chunk size: %v)\n", chunkSize)

		for currentStart.Before(timeRange.End) {
			// Check context cancellation
			if ctx.Err() != nil {
				_ = pw.CloseWithError(ctx.Err())
				return
			}

			currentEnd := currentStart.Add(chunkSize)
			if currentEnd.After(timeRange.End) {
				currentEnd = timeRange.End
			}

			// Execute query_range for this chunk
			// We use a separate context for the request to ensure we can cancel it
			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			chunk := borderSafeWindow(domain.TimeRange{Start: currentStart, End: currentEnd}, timeRange.End)
			result, err := client.QueryRange(reqCtx, selector, chunk.Start, chunk.End, step)
			cancel()

			if err != nil {
				fmt.Printf("[FAIL] Query_range failed for chunk %s-%s: %v\n",
					currentStart.Format(time.RFC3339), currentEnd.Format(time.RFC3339), err)
				_ = pw.CloseWithError(fmt.Errorf("query_range chunk failed: %w", err))
				return
			}

			// Convert and stream results: one export-format line per series per chunk.
			for _, series := range result.Data.Result {
				values, timestamps := querySeriesPoints(series.Values)
				if len(values) == 0 {
					continue
				}
				var err error
				line, err = appendExportLine(line[:0], series.Metric, values, timestamps)
				if err == nil {
					_, err = pw.Write(line)
				}
				if err != nil {
					_ = pw.CloseWithError(fmt.Errorf("encode error: %w", err))
					return
				}
				totalPoints += len(values)
			}

			// Move to next chunk. The chunk above ended 1ms before currentEnd, so the point on the border
			// belongs to the next chunk only.
			currentStart = currentEnd
		}

		fmt.Printf("[OK] Streaming completed. Total points: %d\n", totalPoints)
		_ = pw.Close()
	}()

	return pr, nil
}

// querySeriesPoints converts query_range [timestamp, "value"] pairs into values and millisecond
// timestamps. Malformed points are skipped. "NaN", "+Inf" and "-Inf" parse as special floats.
func querySeriesPoints(points [][]interface{}) ([]float64, []int64) {
	values := make([]float64, 0, len(points))
	timestamps := make([]int64, 0, len(points))
	for _, point := range points {
		if len(point) < 2 {
			continue
		}
		ts, ok := point[0].(float64)
		if !ok {
			continue
		}
		raw, ok := point[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		values = append(values, v)
		timestamps = append(timestamps, int64(math.Round(ts*1000)))
	}
	return values, timestamps
}

// appendExportLine encodes one series in the /api/v1/export JSON line format (see vm.AppendJSONFloat for values).
func appendExportLine(dst []byte, metric map[string]string, values []float64, timestamps []int64) ([]byte, error) {
	labels, err := json.Marshal(metric)
	if err != nil {
		return dst, err
	}
	dst = append(dst, `{"metric":`...)
	dst = append(dst, labels...)
	dst = append(dst, `,"values":[`...)
	for i, v := range values {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = vm.AppendJSONFloat(dst, v)
	}
	dst = append(dst, `],"timestamps":[`...)
	for i, ts := range timestamps {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendInt(dst, ts, 10)
	}
	return append(dst, "]}\n"...), nil
}

func (s *exportServiceImpl) fetchBatch(ctx context.Context, client *vm.Client, selector string, tr domain.TimeRange, metricStepSeconds int, forceQueryRange bool) (io.ReadCloser, error) {
	fmt.Printf("Attempting export for batch: %s -> %s\n", tr.Start.Format(time.RFC3339), tr.End.Format(time.RFC3339))
	if forceQueryRange {
		fmt.Printf("[INFO] Using query_range export for custom query\n")
		return s.exportViaQueryRange(ctx, client, selector, tr, metricStepSeconds)
	}
	reader, err := client.Export(ctx, selector, tr.Start, tr.End)
	if err != nil && s.isMissingRouteError(err) {
		fmt.Printf("[WARN] Export API not available for current batch, falling back to query_range\n")
		return s.exportViaQueryRange(ctx, client, selector, tr, metricStepSeconds)
	}
	if err != nil {
		return nil, fmt.Errorf("export failed: %w", err)
	}
	return reader, nil
}

func (s *exportServiceImpl) fetchAttemptReader(ctx context.Context, client *vm.Client, attempt exportAttempt) (io.ReadCloser, error) {
	fmt.Printf("Attempting export for batch: %s -> %s\n", attempt.TimeRange.Start.Format(time.RFC3339), attempt.TimeRange.End.Format(time.RFC3339))
	if attempt.UseQueryRange {
		fmt.Printf("[INFO] Using query_range export for custom query at %ds step\n", attempt.StepSeconds)
		return s.exportViaQueryRange(ctx, client, attempt.Selector, attempt.TimeRange, attempt.StepSeconds)
	}
	reader, err := client.Export(ctx, attempt.Selector, attempt.TimeRange.Start, attempt.TimeRange.End)
	if err != nil {
		return nil, fmt.Errorf("export failed: %w", err)
	}
	return reader, nil
}

// generateExportID generates a unique export ID
func (s *exportServiceImpl) generateExportID() string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("export-%d", timestamp)
}
